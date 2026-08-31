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

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Router is the engineer-availability/queue state machine, backed by this
// service's own PostgreSQL database (see migrations/). Every exported
// method runs as a single transaction, matching the "one state transition,
// one atomic unit" shape the original in-memory prototype's single mutex
// gave for free -- see each method's own doc comment for what it does
// inside that transaction.
type Router struct {
	db *pgxpool.Pool
}

// NewRouter constructs a Router backed by db. Does not itself run
// migrations -- see migrations/ and this service's README for how to apply
// them; db.Ping has already been called by internal/db.NewPool by the time
// this is constructed (see cmd/server/main.go).
func NewRouter(db *pgxpool.Pool) *Router {
	return &Router{db: db}
}

// withTx runs fn inside a transaction, committing on success and rolling
// back (a no-op if fn already committed nothing) on any error, including a
// panic recovered elsewhere up the stack.
func (r *Router) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("router: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("router: commit transaction: %w", err)
	}
	return nil
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

// Escalate assigns c to an engineer using this priority, or appends it to
// the waiting queue if nobody qualifies:
//
//  1. The engineer c.CustomerEmail was most recently assigned to (see
//     customer_engineer_assignments / assignCaseToEngineer), if that
//     engineer is AVAILABLE and idle right now. A BUSY or OFFLINE sticky
//     engineer is skipped entirely -- this is a "reconnect them if
//     possible" preference, not a guarantee, so the customer never waits
//     on one specific person when someone else is free.
//  2. Otherwise, whichever AVAILABLE engineer has been assigned the fewest
//     chats today, ties broken by who has been AVAILABLE the longest (see
//     popAvailableEngineer).
func (r *Router) Escalate(ctx context.Context, c CaseInfo) (EscalateResult, error) {
	caseInfoJSON, err := json.Marshal(c)
	if err != nil {
		return EscalateResult{}, fmt.Errorf("router: marshal case info: %w", err)
	}

	var result EscalateResult
	err = r.withTx(ctx, func(tx pgx.Tx) error {
		email, ok, err := stickyOrLeastBusyEngineer(ctx, tx, c.CustomerEmail)
		if err != nil {
			return err
		}
		if !ok {
			position, err := enqueueCase(ctx, tx, c, caseInfoJSON, false)
			if err != nil {
				return err
			}
			result = EscalateResult{Queued: true, Position: position}
			return nil
		}

		if err := assignCaseToEngineer(ctx, tx, email, c, caseInfoJSON); err != nil {
			return err
		}
		result = EscalateResult{EngineerEmail: email}
		return nil
	})
	if err != nil {
		return EscalateResult{}, err
	}
	return result, nil
}

// PresenceResult is SetPresence's outcome.
type PresenceResult struct {
	Applied bool `json:"applied"`
	// PendingOffline echoes the engineer's resulting PendingOffline flag --
	// meaningful only when they were mid-session at the time of the call.
	PendingOffline bool `json:"pendingOffline,omitempty"`
	// AssignedCase is set when this presence change immediately drained the
	// queue (transitioning to AVAILABLE with a non-empty queue assigns the
	// head to this same engineer).
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// SetPresence applies an engineer's requested status change, creating their
// row (defaulting to OFFLINE) on first contact if this is the first time
// this service has heard from them.
//
// Mid-session (current_case_id IS NOT NULL): capacity is a single dedicated
// session, so the session itself is never affected here. The only thing a
// request can change is pending_offline -- requesting OFFLINE sets it (the
// engineer will be removed once Completed(email) is called instead of
// rejoining the pool); requesting AVAILABLE or BUSY clears it (the engineer
// changed their mind about leaving).
//
// Idle: AVAILABLE joins the pool (available_since = now()) and, if the
// queue is non-empty, immediately pops and assigns the head (the engineer
// goes straight to BUSY with that case, still counted as "applied" rather
// than a separate step the caller has to notice). BUSY is a manual "do not
// disturb" -- leaves/stays out of the pool with no case. OFFLINE also
// leaves/stays out of the pool.
func (r *Router) SetPresence(ctx context.Context, email string, want Status) (PresenceResult, error) {
	var result PresenceResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		row, err := ensureAndLockEngineer(ctx, tx, email)
		if err != nil {
			return err
		}

		if row.CurrentCaseID != nil {
			pendingOffline := want == StatusOffline
			if _, err := tx.Exec(ctx, `
				UPDATE engineers SET pending_offline = $1, updated_at = now()
				WHERE email = $2
			`, pendingOffline, email); err != nil {
				return fmt.Errorf("update pending_offline: %w", err)
			}
			result = PresenceResult{Applied: true, PendingOffline: pendingOffline}
			return nil
		}

		switch want {
		case StatusAvailable:
			if _, err := tx.Exec(ctx, `
				UPDATE engineers
				SET status = 'AVAILABLE', pending_offline = false,
				    available_since = now(), updated_at = now()
				WHERE email = $1
			`, email); err != nil {
				return fmt.Errorf("set available: %w", err)
			}

			c, ok, err := popQueueHead(ctx, tx)
			if err != nil {
				return err
			}
			if !ok {
				result = PresenceResult{Applied: true}
				return nil
			}

			caseInfoJSON, err := json.Marshal(c)
			if err != nil {
				return fmt.Errorf("marshal queued case: %w", err)
			}
			if err := assignCaseToEngineer(ctx, tx, email, c, caseInfoJSON); err != nil {
				return err
			}
			assigned := c
			result = PresenceResult{Applied: true, AssignedCase: &assigned}
			return nil

		case StatusBusy:
			if _, err := tx.Exec(ctx, `
				UPDATE engineers
				SET status = 'BUSY', pending_offline = false,
				    available_since = NULL, updated_at = now()
				WHERE email = $1
			`, email); err != nil {
				return fmt.Errorf("set busy: %w", err)
			}
			result = PresenceResult{Applied: true}
			return nil

		case StatusOffline:
			if _, err := tx.Exec(ctx, `
				UPDATE engineers
				SET status = 'OFFLINE', pending_offline = false,
				    available_since = NULL, updated_at = now()
				WHERE email = $1
			`, email); err != nil {
				return fmt.Errorf("set offline: %w", err)
			}
			result = PresenceResult{Applied: true}
			return nil

		default:
			result = PresenceResult{Applied: false}
			return nil
		}
	})
	if err != nil {
		return PresenceResult{}, err
	}
	return result, nil
}

// CompletedResult is Completed's outcome.
type CompletedResult struct {
	// Removed is true when the engineer had requested OFFLINE while
	// mid-session (pending_offline) -- they are now fully OFFLINE and did
	// NOT rejoin the available pool or receive a queued case. This is the
	// "removed after the current session completes" requirement.
	Removed bool `json:"removed,omitempty"`
	// Rejoined is true when the engineer went back to AVAILABLE (the
	// non-Removed path) -- possibly immediately BUSY again if AssignedCase
	// is also set.
	Rejoined     bool      `json:"rejoined,omitempty"`
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// Completed clears the engineer's current case (the session they just
// ended) and either removes them entirely (if they'd asked to go OFFLINE
// while busy) or returns them to AVAILABLE -- immediately assigning the
// next queued case to them, if any, exactly like SetPresence's own
// queue-drain-on-AVAILABLE behavior. A no-op (zero CompletedResult) if
// email has no row at all.
func (r *Router) Completed(ctx context.Context, email string) (CompletedResult, error) {
	var result CompletedResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var pendingOffline bool
		err := tx.QueryRow(ctx, `
			SELECT pending_offline FROM engineers WHERE email = $1 FOR UPDATE
		`, email).Scan(&pendingOffline)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			result = CompletedResult{}
			return nil
		case err != nil:
			return fmt.Errorf("lock engineer: %w", err)
		}

		if pendingOffline {
			if _, err := tx.Exec(ctx, `
				UPDATE engineers
				SET status = 'OFFLINE', pending_offline = false,
				    current_case_id = NULL, current_case = NULL,
				    available_since = NULL, updated_at = now()
				WHERE email = $1
			`, email); err != nil {
				return fmt.Errorf("clear session (removed): %w", err)
			}
			result = CompletedResult{Removed: true}
			return nil
		}

		if _, err := tx.Exec(ctx, `
			UPDATE engineers
			SET status = 'AVAILABLE', current_case_id = NULL, current_case = NULL,
			    available_since = now(), updated_at = now()
			WHERE email = $1
		`, email); err != nil {
			return fmt.Errorf("clear session (rejoin): %w", err)
		}

		c, ok, err := popQueueHead(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			result = CompletedResult{Rejoined: true}
			return nil
		}

		caseInfoJSON, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("marshal queued case: %w", err)
		}
		if err := assignCaseToEngineer(ctx, tx, email, c, caseInfoJSON); err != nil {
			return err
		}
		assigned := c
		result = CompletedResult{Rejoined: true, AssignedCase: &assigned}
		return nil
	})
	if err != nil {
		return CompletedResult{}, err
	}
	return result, nil
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
// (before accepting it). Treats the decline like Completed for the
// declining engineer (same pending_offline handling), then tries to hand
// caseID to whichever other AVAILABLE engineer has handled the fewest
// chats today (same fallback ranking Escalate uses, excluding the
// decliner -- this does not re-check that customer's sticky engineer);
// if none are free, pushes it back onto the FRONT of the queue -- the
// customer already waited once, so they should not end up behind newer
// arrivals. A no-op (zero DeclineResult) if email has no row, or isn't
// currently holding caseID.
func (r *Router) Decline(ctx context.Context, email, caseID string) (DeclineResult, error) {
	var result DeclineResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var (
			currentCaseID  *string
			currentCase    []byte
			pendingOffline bool
		)
		err := tx.QueryRow(ctx, `
			SELECT current_case_id, current_case, pending_offline
			FROM engineers WHERE email = $1 FOR UPDATE
		`, email).Scan(&currentCaseID, &currentCase, &pendingOffline)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			result = DeclineResult{}
			return nil
		case err != nil:
			return fmt.Errorf("lock engineer: %w", err)
		}
		if currentCaseID == nil || *currentCaseID != caseID {
			result = DeclineResult{}
			return nil
		}

		var declined CaseInfo
		if err := json.Unmarshal(currentCase, &declined); err != nil {
			return fmt.Errorf("decode current case: %w", err)
		}

		if pendingOffline {
			if _, err := tx.Exec(ctx, `
				UPDATE engineers
				SET status = 'OFFLINE', pending_offline = false,
				    current_case_id = NULL, current_case = NULL,
				    available_since = NULL, updated_at = now()
				WHERE email = $1
			`, email); err != nil {
				return fmt.Errorf("clear declined session (offline): %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE engineers
				SET status = 'AVAILABLE', current_case_id = NULL, current_case = NULL,
				    available_since = now(), updated_at = now()
				WHERE email = $1
			`, email); err != nil {
				return fmt.Errorf("clear declined session (available): %w", err)
			}
		}

		declinedJSON, err := json.Marshal(declined)
		if err != nil {
			return fmt.Errorf("marshal declined case: %w", err)
		}

		candidate, ok, err := popAvailableEngineer(ctx, tx, email)
		if err != nil {
			return err
		}
		if ok {
			if err := assignCaseToEngineer(ctx, tx, candidate, declined, declinedJSON); err != nil {
				return err
			}
			assigned := declined
			result = DeclineResult{ReassignedTo: candidate, AssignedCase: &assigned}
			return nil
		}

		if _, err := enqueueCase(ctx, tx, declined, declinedJSON, true); err != nil {
			return err
		}
		result = DeclineResult{Requeued: true}
		return nil
	})
	if err != nil {
		return DeclineResult{}, err
	}
	return result, nil
}

// GetPresence returns email's current status, defaulting to OFFLINE for an
// engineer this database has never seen a presence update from.
func (r *Router) GetPresence(ctx context.Context, email string) (Status, error) {
	var status Status
	err := r.db.QueryRow(ctx, `SELECT status FROM engineers WHERE email = $1`, email).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return StatusOffline, nil
	case err != nil:
		return "", fmt.Errorf("router: get presence: %w", err)
	default:
		return status, nil
	}
}

// DebugEngineer is one engineer's row in DebugState's dump.
type DebugEngineer struct {
	Email          string    `json:"email"`
	Status         Status    `json:"status"`
	PendingOffline bool      `json:"pendingOffline"`
	CurrentCase    *CaseInfo `json:"currentCase,omitempty"`
	// ChatsToday is 0 if the engineer hasn't been assigned a case yet today
	// (see assignCaseToEngineer's lazy per-day reset) even if a stale
	// nonzero count from a previous day is still sitting in the row.
	ChatsToday int `json:"chatsToday"`
}

// DebugState is the full dump GET /route/debug/state returns.
// Verification-only -- no real caller (csm-portal/backend or otherwise)
// depends on this endpoint; it exists purely so the end-to-end test plan
// can assert on internal state directly instead of inferring it from SSE
// side effects alone.
type DebugState struct {
	Engineers []DebugEngineer `json:"engineers"`
	Available []string        `json:"available"`
	Queue     []CaseInfo      `json:"queue"`
}

// DebugState snapshots the current engineer registry, available pool, and
// queue. Three separate read-only queries rather than one transaction --
// this is a debug/test endpoint, not a state transition, so a slightly
// stale cross-query view is an acceptable tradeoff for not holding locks.
func (r *Router) DebugState(ctx context.Context) (DebugState, error) {
	engineers, err := r.debugEngineers(ctx)
	if err != nil {
		return DebugState{}, err
	}
	available, err := r.debugAvailable(ctx)
	if err != nil {
		return DebugState{}, err
	}
	queue, err := r.debugQueue(ctx)
	if err != nil {
		return DebugState{}, err
	}
	return DebugState{Engineers: engineers, Available: available, Queue: queue}, nil
}

func (r *Router) debugEngineers(ctx context.Context) ([]DebugEngineer, error) {
	rows, err := r.db.Query(ctx, `
		SELECT email, status, pending_offline, current_case,
		       CASE WHEN chats_today_date = CURRENT_DATE THEN chats_today ELSE 0 END
		FROM engineers ORDER BY email
	`)
	if err != nil {
		return nil, fmt.Errorf("debug state: query engineers: %w", err)
	}
	defer rows.Close()

	engineers := []DebugEngineer{}
	for rows.Next() {
		var (
			email          string
			status         Status
			pendingOffline bool
			currentCase    []byte
			chatsToday     int
		)
		if err := rows.Scan(&email, &status, &pendingOffline, &currentCase, &chatsToday); err != nil {
			return nil, fmt.Errorf("debug state: scan engineer: %w", err)
		}
		var cc *CaseInfo
		if currentCase != nil {
			var c CaseInfo
			if err := json.Unmarshal(currentCase, &c); err != nil {
				return nil, fmt.Errorf("debug state: decode current case: %w", err)
			}
			cc = &c
		}
		engineers = append(engineers, DebugEngineer{
			Email: email, Status: status, PendingOffline: pendingOffline, CurrentCase: cc,
			ChatsToday: chatsToday,
		})
	}
	return engineers, rows.Err()
}

func (r *Router) debugAvailable(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT email FROM engineers
		WHERE status = 'AVAILABLE' AND current_case_id IS NULL
		ORDER BY
			CASE WHEN chats_today_date = CURRENT_DATE THEN chats_today ELSE 0 END ASC,
			available_since ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("debug state: query available: %w", err)
	}
	defer rows.Close()

	available := []string{}
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("debug state: scan available: %w", err)
		}
		available = append(available, email)
	}
	return available, rows.Err()
}

func (r *Router) debugQueue(ctx context.Context) ([]CaseInfo, error) {
	rows, err := r.db.Query(ctx, `
		SELECT case_info FROM escalation_queue ORDER BY order_key ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("debug state: query queue: %w", err)
	}
	defer rows.Close()

	queue := []CaseInfo{}
	for rows.Next() {
		var caseInfoJSON []byte
		if err := rows.Scan(&caseInfoJSON); err != nil {
			return nil, fmt.Errorf("debug state: scan queue row: %w", err)
		}
		var c CaseInfo
		if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
			return nil, fmt.Errorf("debug state: decode queued case: %w", err)
		}
		queue = append(queue, c)
	}
	return queue, rows.Err()
}

// stickyOrLeastBusyEngineer implements Escalate's assignment priority: try
// customerEmail's sticky engineer first (skipped entirely if customerEmail
// is empty, or that engineer isn't AVAILABLE and idle right now), then fall
// back to popAvailableEngineer's least-busy-today ranking. ok=false means
// neither found anyone -- Escalate should queue the case instead.
func stickyOrLeastBusyEngineer(ctx context.Context, tx pgx.Tx, customerEmail string) (string, bool, error) {
	if customerEmail != "" {
		sticky, found, err := stickyEngineerFor(ctx, tx, customerEmail)
		if err != nil {
			return "", false, err
		}
		if found {
			available, err := lockEngineerIfAvailable(ctx, tx, sticky)
			if err != nil {
				return "", false, err
			}
			if available {
				return sticky, true, nil
			}
		}
	}
	return popAvailableEngineer(ctx, tx, "")
}

// stickyEngineerFor returns the engineer customerEmail was most recently
// assigned to, if any (see assignCaseToEngineer, which records this on
// every assignment -- sticky match, load-balanced fallback, or a Decline
// reassignment all count).
func stickyEngineerFor(ctx context.Context, tx pgx.Tx, customerEmail string) (string, bool, error) {
	var email string
	err := tx.QueryRow(ctx, `
		SELECT engineer_email FROM customer_engineer_assignments WHERE customer_email = $1
	`, customerEmail).Scan(&email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("look up sticky engineer: %w", err)
	default:
		return email, true, nil
	}
}

// lockEngineerIfAvailable locks email's row FOR UPDATE (if it exists) and
// reports whether they're AVAILABLE and idle right now. BUSY, OFFLINE, and
// "no row at all" (this service has never heard from them) all report
// false -- per this feature's design, a sticky engineer who can't take the
// case immediately is treated the same as no sticky engineer at all.
func lockEngineerIfAvailable(ctx context.Context, tx pgx.Tx, email string) (bool, error) {
	var (
		status        Status
		currentCaseID *string
	)
	err := tx.QueryRow(ctx, `
		SELECT status, current_case_id FROM engineers WHERE email = $1 FOR UPDATE
	`, email).Scan(&status, &currentCaseID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("lock sticky engineer: %w", err)
	default:
		return status == StatusAvailable && currentCaseID == nil, nil
	}
}

// engineerRow is the subset of an engineers row SetPresence needs, locked
// FOR UPDATE for the rest of its transaction.
type engineerRow struct {
	CurrentCaseID *string
}

// ensureAndLockEngineer makes sure email has a row (defaulting to OFFLINE,
// exactly like the original in-memory Router's getOrCreate), then locks and
// returns it FOR UPDATE for the rest of tx.
func ensureAndLockEngineer(ctx context.Context, tx pgx.Tx, email string) (engineerRow, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO engineers (email) VALUES ($1)
		ON CONFLICT (email) DO NOTHING
	`, email); err != nil {
		return engineerRow{}, fmt.Errorf("ensure engineer row: %w", err)
	}

	var row engineerRow
	if err := tx.QueryRow(ctx, `
		SELECT current_case_id FROM engineers WHERE email = $1 FOR UPDATE
	`, email).Scan(&row.CurrentCaseID); err != nil {
		return engineerRow{}, fmt.Errorf("lock engineer row: %w", err)
	}
	return row, nil
}

// popAvailableEngineer locks and returns an AVAILABLE, idle engineer other
// than exclude (pass "" to exclude no one): whichever has been assigned the
// fewest chats today, ties broken by who has been AVAILABLE the longest --
// or ok=false if none are free. FOR UPDATE SKIP LOCKED lets concurrent
// callers each grab a different engineer instead of blocking on each
// other. Used as Escalate's (via stickyOrLeastBusyEngineer) and Decline's
// fallback once a sticky/self match isn't usable.
func popAvailableEngineer(ctx context.Context, tx pgx.Tx, exclude string) (email string, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT email FROM engineers
		WHERE status = 'AVAILABLE' AND current_case_id IS NULL AND email != $1
		ORDER BY
			CASE WHEN chats_today_date = CURRENT_DATE THEN chats_today ELSE 0 END ASC,
			available_since ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, exclude).Scan(&email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("pop available engineer: %w", err)
	default:
		return email, true, nil
	}
}

// assignCaseToEngineer marks email BUSY with c as their current case,
// bumps their daily chat count by one (resetting it first if the last
// increment wasn't today -- see migrations/000003), and, when
// c.CustomerEmail is set, records this customer as now belonging to email
// for the next time they escalate (see stickyEngineerFor /
// stickyOrLeastBusyEngineer).
func assignCaseToEngineer(ctx context.Context, tx pgx.Tx, email string, c CaseInfo, caseInfoJSON []byte) error {
	if _, err := tx.Exec(ctx, `
		UPDATE engineers
		SET status = 'BUSY', current_case_id = $1, current_case = $2::jsonb,
		    available_since = NULL,
		    chats_today = CASE WHEN chats_today_date = CURRENT_DATE THEN chats_today + 1 ELSE 1 END,
		    chats_today_date = CURRENT_DATE,
		    updated_at = now()
		WHERE email = $3
	`, c.CaseID, caseInfoJSON, email); err != nil {
		return fmt.Errorf("assign case to engineer: %w", err)
	}

	if c.CustomerEmail != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO customer_engineer_assignments (customer_email, engineer_email, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (customer_email) DO UPDATE
			SET engineer_email = excluded.engineer_email, updated_at = now()
		`, c.CustomerEmail, email); err != nil {
			return fmt.Errorf("record sticky assignment: %w", err)
		}
	}
	return nil
}

// popQueueHead removes and returns the oldest queued case (by order_key,
// then id to break ties), or ok=false if the queue is empty. FOR UPDATE
// SKIP LOCKED inside the subquery, same job-queue idiom as
// popAvailableEngineer.
func popQueueHead(ctx context.Context, tx pgx.Tx) (CaseInfo, bool, error) {
	var caseInfoJSON []byte
	err := tx.QueryRow(ctx, `
		DELETE FROM escalation_queue
		WHERE id = (
			SELECT id FROM escalation_queue
			ORDER BY order_key ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING case_info
	`).Scan(&caseInfoJSON)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return CaseInfo{}, false, nil
	case err != nil:
		return CaseInfo{}, false, fmt.Errorf("pop queue head: %w", err)
	}

	var c CaseInfo
	if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
		return CaseInfo{}, false, fmt.Errorf("decode queued case: %w", err)
	}
	return c, true, nil
}

// enqueueCase appends c to the end of the queue (front=false) or re-queues
// it at the front (front=true, used by Decline -- that customer already
// waited once). Table-locked for the duration of the order_key computation
// so two concurrent enqueues can't compute the same key.
func enqueueCase(ctx context.Context, tx pgx.Tx, c CaseInfo, caseInfoJSON []byte, front bool) (position int, err error) {
	if _, err := tx.Exec(ctx, "LOCK TABLE escalation_queue IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return 0, fmt.Errorf("lock escalation_queue: %w", err)
	}

	orderExpr := "COALESCE(MAX(order_key), 0) + 1"
	if front {
		orderExpr = "COALESCE(MIN(order_key), 0) - 1"
	}
	var orderKey int64
	if err := tx.QueryRow(ctx, "SELECT "+orderExpr+" FROM escalation_queue").Scan(&orderKey); err != nil {
		return 0, fmt.Errorf("compute order key: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO escalation_queue (order_key, case_id, case_info)
		VALUES ($1, $2, $3::jsonb)
	`, orderKey, c.CaseID, caseInfoJSON); err != nil {
		return 0, fmt.Errorf("insert queue row: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM escalation_queue WHERE order_key <= $1
	`, orderKey).Scan(&position); err != nil {
		return 0, fmt.Errorf("compute queue position: %w", err)
	}
	return position, nil
}
