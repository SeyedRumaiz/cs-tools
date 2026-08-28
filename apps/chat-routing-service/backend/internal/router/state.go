// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package router

import "sync"

// engineerState is one engineer's live availability record.
//
//   - CurrentCase != nil means the engineer is mid-session — capacity is a
//     single dedicated session per engineer, so no engineer can ever have
//     more than one.
//   - PendingOffline is only ever meaningful while CurrentCase != nil: it
//     records that the engineer asked to go OFFLINE while busy, deferring
//     the actual transition to when Completed(email) is called (see its
//     doc comment) rather than changing Status immediately.
type engineerState struct {
	Email          string
	Status         Status
	PendingOffline bool
	CurrentCase    *CaseInfo
}

// Router is the engineer-availability/queue state machine. Safe for
// concurrent use — every exported method takes the same mutex for its
// entire duration, matching the "small critical section, no held locks
// across I/O" shape of stream.BroadcastHub in csm-portal/backend (this
// service never does I/O inside the lock).
type Router struct {
	mu        sync.Mutex
	engineers map[string]*engineerState
	// available is a FIFO of emails currently AVAILABLE with CurrentCase
	// == nil: assignment pops the head, becoming available appends to the
	// tail. This is round-robin by construction — least-recently-available
	// goes first — without needing a separate index/pointer that would
	// have to be kept in sync as engineers join and leave dynamically.
	available []string
	// queue is a FIFO of not-yet-assigned escalations, oldest first.
	// Decline pushes back onto the FRONT (see Decline) since that customer
	// already waited once.
	queue []CaseInfo
}

// NewRouter constructs an empty Router — no engineers, no queue.
func NewRouter() *Router {
	return &Router{engineers: make(map[string]*engineerState)}
}

func (r *Router) getOrCreate(email string) *engineerState {
	e, ok := r.engineers[email]
	if !ok {
		e = &engineerState{Email: email, Status: StatusOffline}
		r.engineers[email] = e
	}
	return e
}

// removeFromAvailable is idempotent — a no-op if email isn't present.
// Called before every append to r.available so an engineer can never appear
// in it twice (e.g. a duplicate "AVAILABLE" presence call in a row).
func (r *Router) removeFromAvailable(email string) {
	for i, e := range r.available {
		if e == email {
			r.available = append(r.available[:i], r.available[i+1:]...)
			return
		}
	}
}

// EscalateResult is Escalate's outcome: exactly one of EngineerEmail (set)
// or Queued (true) applies.
type EscalateResult struct {
	EngineerEmail string `json:"engineerEmail,omitempty"`
	Queued        bool   `json:"queued,omitempty"`
	// Position is 1-based ("you are #1 in the queue"), only meaningful when
	// Queued is true.
	Position int `json:"position,omitempty"`
}

// Escalate assigns c to the head of the available-engineer FIFO if one
// exists (marking that engineer BUSY with c as their CurrentCase), or
// appends it to the waiting queue otherwise.
func (r *Router) Escalate(c CaseInfo) EscalateResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.available) > 0 {
		email := r.available[0]
		r.available = r.available[1:]
		e := r.engineers[email]
		e.Status = StatusBusy
		caseCopy := c
		e.CurrentCase = &caseCopy
		return EscalateResult{EngineerEmail: email}
	}

	r.queue = append(r.queue, c)
	return EscalateResult{Queued: true, Position: len(r.queue)}
}

// PresenceResult is SetPresence's outcome.
type PresenceResult struct {
	Applied bool `json:"applied"`
	// PendingOffline echoes the engineer's resulting PendingOffline flag —
	// meaningful only when they were mid-session at the time of the call.
	PendingOffline bool `json:"pendingOffline,omitempty"`
	// AssignedCase is set when this presence change immediately drained the
	// queue (transitioning to AVAILABLE with a non-empty queue assigns the
	// head to this same engineer).
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// SetPresence applies an engineer's requested status change.
//
// Mid-session (CurrentCase != nil): capacity is a single dedicated session,
// so the session itself is never affected here. The only thing a request
// can change is PendingOffline — requesting OFFLINE sets it (the engineer
// will be removed once Completed(email) is called instead of rejoining the
// pool); requesting AVAILABLE or BUSY clears it (the engineer changed their
// mind about leaving).
//
// Idle (CurrentCase == nil): AVAILABLE joins the tail of the available FIFO
// and, if the queue is non-empty, immediately pops and assigns the head
// (the engineer goes straight to BUSY with that case, still counted as
// "applied" rather than a separate step the caller has to notice). BUSY is
// a manual "do not disturb" — leaves/stays out of the available pool with
// no case. OFFLINE also leaves/stays out of the pool.
func (r *Router) SetPresence(email string, want Status) PresenceResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	e := r.getOrCreate(email)

	if e.CurrentCase != nil {
		e.PendingOffline = want == StatusOffline
		return PresenceResult{Applied: true, PendingOffline: e.PendingOffline}
	}

	switch want {
	case StatusAvailable:
		e.Status = StatusAvailable
		e.PendingOffline = false
		r.removeFromAvailable(email)
		if len(r.queue) > 0 {
			next := r.queue[0]
			r.queue = r.queue[1:]
			e.Status = StatusBusy
			nextCopy := next
			e.CurrentCase = &nextCopy
			return PresenceResult{Applied: true, AssignedCase: &nextCopy}
		}
		r.available = append(r.available, email)
		return PresenceResult{Applied: true}
	case StatusBusy:
		e.Status = StatusBusy
		e.PendingOffline = false
		r.removeFromAvailable(email)
		return PresenceResult{Applied: true}
	case StatusOffline:
		e.Status = StatusOffline
		e.PendingOffline = false
		r.removeFromAvailable(email)
		return PresenceResult{Applied: true}
	default:
		return PresenceResult{Applied: false}
	}
}

// CompletedResult is Completed's outcome.
type CompletedResult struct {
	// Removed is true when the engineer had requested OFFLINE while
	// mid-session (PendingOffline) — they are now fully OFFLINE and did
	// NOT rejoin the available pool or receive a queued case. This is the
	// "removed after the current session completes" requirement.
	Removed bool `json:"removed,omitempty"`
	// Rejoined is true when the engineer went back to AVAILABLE (the
	// non-Removed path) — possibly immediately BUSY again if AssignedCase
	// is also set.
	Rejoined     bool      `json:"rejoined,omitempty"`
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// Completed clears the engineer's CurrentCase (the session they just ended)
// and either removes them entirely (if they'd asked to go OFFLINE while
// busy) or returns them to AVAILABLE — immediately assigning the next
// queued case to them, if any, exactly like SetPresence's own
// queue-drain-on-AVAILABLE behavior.
func (r *Router) Completed(email string) CompletedResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.engineers[email]
	if !ok {
		return CompletedResult{}
	}
	e.CurrentCase = nil

	if e.PendingOffline {
		e.Status = StatusOffline
		e.PendingOffline = false
		return CompletedResult{Removed: true}
	}

	e.Status = StatusAvailable
	r.removeFromAvailable(email) // defensive; should not already be present
	if len(r.queue) > 0 {
		next := r.queue[0]
		r.queue = r.queue[1:]
		e.Status = StatusBusy
		nextCopy := next
		e.CurrentCase = &nextCopy
		return CompletedResult{Rejoined: true, AssignedCase: &nextCopy}
	}
	r.available = append(r.available, email)
	return CompletedResult{Rejoined: true}
}

// DeclineResult is Decline's outcome: exactly one of ReassignedTo (set,
// with AssignedCase also set) or Requeued (true) applies, unless the
// decline itself was a no-op (the engineer wasn't actually holding that
// case), in which case all three are zero.
type DeclineResult struct {
	ReassignedTo string `json:"reassignedTo,omitempty"`
	Requeued     bool   `json:"requeued,omitempty"`
	// AssignedCase is the declined case, set alongside ReassignedTo so the
	// caller (csm-portal/backend) can deliver it to that other engineer the
	// same way a fresh escalation would be, without having to remember the
	// case's own fields itself.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// Decline handles an engineer dismissing a case they were just assigned
// (before accepting it). Necessary because escalations are now routed to
// exactly one engineer rather than broadcast to all of them (see this
// service's package doc comment and csm-portal/backend's chat.go) — an
// unhandled dismiss would otherwise strand the customer with nobody ever
// seeing their request again.
//
// Treats the decline like Completed for the declining engineer (same
// PendingOffline handling), then tries to hand caseID to a different
// available engineer immediately; if none are free, pushes it back onto the
// FRONT of the queue — the customer already waited once, so they should not
// end up behind newer arrivals.
func (r *Router) Decline(email, caseID string) DeclineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.engineers[email]
	if !ok || e.CurrentCase == nil || e.CurrentCase.CaseID != caseID {
		return DeclineResult{}
	}
	declined := *e.CurrentCase
	e.CurrentCase = nil

	if e.PendingOffline {
		e.Status = StatusOffline
		e.PendingOffline = false
	} else {
		e.Status = StatusAvailable
		r.removeFromAvailable(email)
		r.available = append(r.available, email)
	}

	for i, candidate := range r.available {
		if candidate == email {
			continue
		}
		r.available = append(r.available[:i], r.available[i+1:]...)
		ce := r.engineers[candidate]
		ce.Status = StatusBusy
		declinedCopy := declined
		ce.CurrentCase = &declinedCopy
		return DeclineResult{ReassignedTo: candidate, AssignedCase: &declinedCopy}
	}

	r.queue = append([]CaseInfo{declined}, r.queue...)
	return DeclineResult{Requeued: true}
}

// GetPresence returns email's current status, defaulting to OFFLINE for an
// engineer this Router has never seen a presence update from.
func (r *Router) GetPresence(email string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.engineers[email]; ok {
		return e.Status
	}
	return StatusOffline
}

// DebugEngineer is one engineer's row in DebugState's dump.
type DebugEngineer struct {
	Email          string    `json:"email"`
	Status         Status    `json:"status"`
	PendingOffline bool      `json:"pendingOffline"`
	CurrentCase    *CaseInfo `json:"currentCase,omitempty"`
}

// DebugState is the full in-memory dump GET /route/debug/state returns.
// Verification-only — no real caller (csm-portal/backend or otherwise)
// depends on this endpoint; it exists purely so the end-to-end test plan
// can assert on internal state directly instead of inferring it from SSE
// side effects alone.
type DebugState struct {
	Engineers []DebugEngineer `json:"engineers"`
	Available []string        `json:"available"`
	Queue     []CaseInfo      `json:"queue"`
}

// DebugState snapshots the current registry, available FIFO, and queue.
func (r *Router) DebugState() DebugState {
	r.mu.Lock()
	defer r.mu.Unlock()

	ds := DebugState{
		Available: append([]string{}, r.available...),
		Queue:     append([]CaseInfo{}, r.queue...),
	}
	for _, e := range r.engineers {
		ds.Engineers = append(ds.Engineers, DebugEngineer{
			Email:          e.Email,
			Status:         e.Status,
			PendingOffline: e.PendingOffline,
			CurrentCase:    e.CurrentCase,
		})
	}
	return ds
}
