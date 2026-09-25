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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Router is the engineer-availability/queue state machine, backed by this
// service's own PostgreSQL database. Every exported method runs as a
// single transaction, so each state transition is atomic.
type Router struct {
	db *pgxpool.Pool
}

// NewRouter constructs a Router backed by db. Doesn't run migrations
// itself -- see migrations/ and the README for that.
func NewRouter(db *pgxpool.Pool) *Router {
	return &Router{db: db}
}

// withTx runs fn inside a transaction, committing on success and rolling
// back on any error (a no-op if fn already committed nothing).
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

// queueStatus mirrors chat_queue.status: WAITING_FOR_ENGINEER for a case
// nobody's been assigned yet, ASSIGNED from the moment someone is (whether
// immediately, via a queue drain, or a Decline/timeout reassignment) until
// Router.Accept confirms them and the row is deleted.
type queueStatus string

const (
	queueWaitingForEngineer queueStatus = "WAITING_FOR_ENGINEER"
	queueAssigned           queueStatus = "ASSIGNED"
)

// EscalateResult is Escalate's outcome: exactly one of EngineerUserID (set)
// or Queued (true) applies.
type EscalateResult struct {
	// EngineerUserID is the IdP "userid" claim of the engineer this case was
	// assigned to -- empty when Queued.
	EngineerUserID string `json:"engineerId,omitempty"`
	Queued         bool   `json:"queued,omitempty"`
	// Position is 1-based ("you are #1 in the queue"), only meaningful when
	// Queued is true.
	Position int `json:"position,omitempty"`
}

// ErrCaseAlreadyEnded is returned by Escalate when c.CaseID's
// chat_conversation row already has session_ended_at set. Escalate must
// never create a (zombie) chat_queue row for a case that has already
// ended -- see the caller-facing origin of this: CreateWorkItem is
// idempotent by case_id (a deliberate, necessary property for retries),
// but that idempotency previously let a caller who retried Escalate itself
// for an already-ended case slip straight through Escalate too, silently
// creating a queue row pointing at a conversation nothing will ever accept
// (its chat_conversation row is done). Under the caseId/conversationId
// identity split, a legitimate re-escalation of the same Novera
// conversation always mints a brand-new caseId (see customer-portal/
// backend-v2's HandleEscalate), so a real client should never hit this in
// practice -- this guard exists as defense in depth against a retry or a
// bug that reuses an old caseId, not as the normal re-escalation path.
var ErrCaseAlreadyEnded = errors.New("this case has already ended and cannot be escalated again")

// Escalate assigns c to whichever AVAILABLE engineer with spare concurrent-
// chat capacity has taken the fewest chats today (ties broken by fewest
// currently-active chats, then who's been AVAILABLE longest -- see
// popAvailableEngineer), or appends it to the waiting queue if nobody
// qualifies.
//
// A chat_queue row is created for c either way (ASSIGNED or
// WAITING_FOR_ENGINEER) and lives until Router.Accept confirms the
// engineer. Requires c's chat_conversation row (see workitem.go's
// CreateWorkItem) to already exist -- csm-portal/backend creates it before
// calling this, so assignCaseToEngineer below has a row to record the
// assignment on. Rejects with ErrCaseAlreadyEnded, before touching
// anything else, if that row's session has already ended -- see that
// error's own doc comment.
func (r *Router) Escalate(ctx context.Context, c CaseInfo) (EscalateResult, error) {
	caseInfoJSON, err := json.Marshal(c)
	if err != nil {
		return EscalateResult{}, fmt.Errorf("router: marshal case info: %w", err)
	}

	var result EscalateResult
	err = r.withTx(ctx, func(tx pgx.Tx) error {
		var sessionEndedAt *time.Time
		err := tx.QueryRow(ctx, `
			SELECT session_ended_at FROM chat_conversation WHERE case_id = $1 FOR UPDATE
		`, c.CaseID).Scan(&sessionEndedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: case_id=%s", ErrConversationNotFound, c.CaseID)
		case err != nil:
			return fmt.Errorf("check conversation state: %w", err)
		}
		if sessionEndedAt != nil {
			return fmt.Errorf("%w: case_id=%s", ErrCaseAlreadyEnded, c.CaseID)
		}

		userID, ok, err := popAvailableEngineer(ctx, tx, "")
		if err != nil {
			return err
		}
		if !ok {
			position, err := insertQueueRow(ctx, tx, c, caseInfoJSON, queueWaitingForEngineer)
			if err != nil {
				return err
			}
			result = EscalateResult{Queued: true, Position: position}
			return nil
		}

		if _, err := insertQueueRow(ctx, tx, c, caseInfoJSON, queueAssigned); err != nil {
			return err
		}
		if err := assignCaseToEngineer(ctx, tx, userID, c); err != nil {
			return err
		}
		result = EscalateResult{EngineerUserID: userID}
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
	// AssignedCases is set when this presence change immediately drained
	// the queue -- transitioning to AVAILABLE claims cases off the queue
	// until either it's empty or the engineer's own capacity is full, so
	// (unlike Completed/Decline/a timeout, which each free at most one
	// slot) more than one case can land here at once.
	AssignedCases []CaseInfo `json:"assignedCases,omitempty"`
}

// SetPresence applies an engineer's requested chat_status change, creating
// their row (defaulting to OFFLINE, capacity 1) on first contact. userID is
// the IdP's stable per-account "userid" claim -- cs_engineer_status is
// keyed by it directly, so there's nothing else a caller needs to supply.
//
// chat_status is a plain manual toggle, independent of how many cases the
// engineer currently holds (see the package doc comment): AVAILABLE means
// open to new work, BUSY is a do-not-disturb that takes none, OFFLINE is
// gone. None of the three touch cases already assigned -- those are only
// ever ended via Completed, handed off via Decline, or reassigned by a
// timeout.
//
// Requesting AVAILABLE additionally drains the waiting queue into this
// engineer's own now-open capacity: it claims the oldest waiting case,
// assigns it, and repeats until either the queue is empty or the engineer's
// max_concurrent_chats is reached (see AssignedCases). BUSY and OFFLINE
// never claim anything.
func (r *Router) SetPresence(ctx context.Context, userID string, want Status) (PresenceResult, error) {
	var result PresenceResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		maxConcurrent, err := ensureAndLockEngineer(ctx, tx, userID)
		if err != nil {
			return err
		}

		switch want {
		case StatusAvailable:
			if _, err := tx.Exec(ctx, `
				UPDATE cs_engineer_status
				SET chat_status = 'AVAILABLE', available_since = now(), updated_at = now()
				WHERE user_id = $1
			`, userID); err != nil {
				return fmt.Errorf("set available: %w", err)
			}

			activeCount, err := activeCaseCount(ctx, tx, userID)
			if err != nil {
				return err
			}
			assigned, err := drainQueueUpTo(ctx, tx, userID, activeCount, maxConcurrent)
			if err != nil {
				return err
			}
			result = PresenceResult{Applied: true, AssignedCases: assigned}
			return nil

		case StatusBusy:
			if _, err := tx.Exec(ctx, `
				UPDATE cs_engineer_status
				SET chat_status = 'BUSY', available_since = NULL, updated_at = now()
				WHERE user_id = $1
			`, userID); err != nil {
				return fmt.Errorf("set busy: %w", err)
			}
			result = PresenceResult{Applied: true}
			return nil

		case StatusOffline:
			if _, err := tx.Exec(ctx, `
				UPDATE cs_engineer_status
				SET chat_status = 'OFFLINE', available_since = NULL, updated_at = now()
				WHERE user_id = $1
			`, userID); err != nil {
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
	// Ended is true when caseID was actually an open (not already-ended)
	// conversation assigned to userID -- false is a no-op, guarding against
	// a duplicate call for a session that already ended (a UI can fire this
	// twice for the same case).
	Ended bool `json:"ended,omitempty"`
	// AssignedCase is set when ending this conversation freed a slot that
	// was immediately backfilled from the waiting queue.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
	// AssignedEngineerID is the IdP "userid" claim of the engineer
	// AssignedCase was just assigned to -- always the same engineer whose
	// slot was freed (Completed's own caller already knows this is
	// themselves; EndByTenant's caller does not, since it authenticates by
	// tenantSlug rather than engineer userID, so this field exists for that
	// caller to know who to deliver AssignedCase to). Empty whenever
	// AssignedCase is nil.
	AssignedEngineerID string `json:"assignedEngineerId,omitempty"`
}

// Completed ends userID's session on caseID: marks that specific
// chat_conversation row's session_ended_at, then -- if the engineer is
// still chat_status AVAILABLE and now has spare capacity -- claims the next
// queued case for them, the same way SetPresence's own queue-drain does.
// Unlike SetPresence, at most one slot is being freed here, so at most one
// case is claimed. A no-op if caseID isn't currently an open conversation
// assigned to userID.
//
// Deliberately never touches chat_status itself -- that's purely a manual
// toggle now (see SetPresence's doc comment), so ending one of an
// engineer's several concurrent sessions has no reason to change it.
func (r *Router) Completed(ctx context.Context, userID, caseID string) (CompletedResult, error) {
	var result CompletedResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var freedAssignee *string
		err := tx.QueryRow(ctx, `
			UPDATE chat_conversation
			SET session_ended_at = now(), updated_at = now()
			WHERE case_id = $1 AND assignee_id = $2 AND session_ended_at IS NULL
			RETURNING assignee_id
		`, caseID, userID).Scan(&freedAssignee)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			result = CompletedResult{}
			return nil
		case err != nil:
			return fmt.Errorf("end session: %w", err)
		}

		var err2 error
		result, err2 = endSessionAndBackfill(ctx, tx, freedAssignee)
		return err2
	})
	if err != nil {
		return CompletedResult{}, err
	}
	return result, nil
}

// EndByTenant ends the session on caseID on behalf of tenantSlug (a
// customer/tenant-initiated completeChat, via console-chat-bridge's
// POST /v1/{tenant}/chats/{caseId}/complete -- see that route's
// requireTenantCase authorization, which this SQL-level tenantSlug scoping
// backs up belt-and-suspenders) rather than a specific engineer userID
// (contrast Completed, which scopes by assignee_id). Shares every other
// side effect with Completed via endSessionAndBackfill.
//
// Unlike Completed, the ended case may never have been assigned to anyone
// at all -- a customer can cancel a still-queued chat. When that's so,
// freedAssignee comes back NULL: endSessionAndBackfill skips the
// engineer-capacity backfill entirely and this method instead removes the
// case's chat_queue row (mirroring Accept's own deleteQueueRow), so it
// stops being eligible for a future claim.
func (r *Router) EndByTenant(ctx context.Context, caseID, tenantSlug string) (CompletedResult, error) {
	var result CompletedResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var freedAssignee *string
		err := tx.QueryRow(ctx, `
			UPDATE chat_conversation
			SET session_ended_at = now(), updated_at = now()
			WHERE case_id = $1 AND case_info ->> 'tenantSlug' = $2 AND session_ended_at IS NULL
			RETURNING assignee_id
		`, caseID, tenantSlug).Scan(&freedAssignee)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			result = CompletedResult{}
			return nil
		case err != nil:
			return fmt.Errorf("end session by tenant: %w", err)
		}

		if freedAssignee == nil {
			// Never assigned -- nothing to backfill, just remove the queue
			// row so an abandoned/cancelled queued case can't later be
			// claimed by a draining engineer.
			if err := deleteQueueRow(ctx, tx, caseID); err != nil {
				return err
			}
			result = CompletedResult{Ended: true}
			return nil
		}

		var err2 error
		result, err2 = endSessionAndBackfill(ctx, tx, freedAssignee)
		return err2
	})
	if err != nil {
		return CompletedResult{}, err
	}
	return result, nil
}

// endSessionAndBackfill runs every side effect Completed and EndByTenant
// share, given the case_id has already been matched and ended by the
// caller's own authorization-scoped UPDATE (which also returned the freed
// assignee_id). freedAssignee is non-nil here -- EndByTenant handles its
// own NULL case (an unassigned, still-queued case) itself, before ever
// calling this.
//
// Locks that engineer's cs_engineer_status row and, if they're still
// chat_status AVAILABLE and now under their configured
// max_concurrent_chats, claims the oldest waiting case and assigns it to
// them -- identical to Completed's pre-refactor inline logic.
func endSessionAndBackfill(ctx context.Context, tx pgx.Tx, freedAssignee *string) (CompletedResult, error) {
	result := CompletedResult{Ended: true}
	if freedAssignee == nil {
		return result, nil
	}
	userID := *freedAssignee

	var (
		chatStatus    Status
		maxConcurrent int
	)
	err := tx.QueryRow(ctx, `
		SELECT chat_status, max_concurrent_chats FROM cs_engineer_status WHERE user_id = $1 FOR UPDATE
	`, userID).Scan(&chatStatus, &maxConcurrent)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No engineer row is unexpected (this case WAS assigned to this
		// engineer), but not a reason to fail this call -- the session end
		// already succeeded.
		return result, nil
	case err != nil:
		return CompletedResult{}, fmt.Errorf("lock engineer: %w", err)
	}
	if chatStatus != StatusAvailable {
		return result, nil
	}

	activeCount, err := activeCaseCount(ctx, tx, userID)
	if err != nil {
		return CompletedResult{}, err
	}
	if activeCount >= maxConcurrent {
		return result, nil
	}

	c, ok, err := claimOldestWaiting(ctx, tx)
	if err != nil {
		return CompletedResult{}, err
	}
	if !ok {
		return result, nil
	}
	if err := assignCaseToEngineer(ctx, tx, userID, c); err != nil {
		return CompletedResult{}, err
	}
	assigned := c
	result.AssignedCase = &assigned
	result.AssignedEngineerID = userID
	return result, nil
}

// ErrNotConversationOwner is returned by ConvertToCase when caseID isn't
// currently an ACTIVE conversation held by userID.
var ErrNotConversationOwner = errors.New("case is not an active conversation held by this engineer")

// ErrAlreadyConverted is returned by ConvertToCase when caseID has already
// been converted to a case or otherwise ended.
var ErrAlreadyConverted = errors.New("chat_conversation is already converted to a case or otherwise ended")

// ErrConversationEnded is returned by AddComment when caseID's
// chat_conversation already has session_ended_at set (via Completed or
// ConvertToCase) -- see AddComment's own doc comment for why silently
// accepting a comment on an already-ended session is exactly the bug this
// guards against.
var ErrConversationEnded = errors.New("chat session has already ended for this case")

// ConvertToCaseResult mirrors CompletedResult -- converting a chat ends its
// session like Completed does, so it can backfill a freed capacity slot.
type ConvertToCaseResult struct {
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// diagnoseConvertFailure runs after ConvertToCase's UPDATE affects zero
// rows, to turn that into a specific, useful error -- a single UPDATE's
// WHERE clause can't say whether the row doesn't exist, belongs to another
// engineer, isn't accepted yet, or was already ended/converted.
func diagnoseConvertFailure(ctx context.Context, tx pgx.Tx, caseID, userID string) error {
	var (
		assigneeID      *string
		state           string
		sessionEndedAt  *time.Time
		entityCaseIDPtr *string
	)
	err := tx.QueryRow(ctx, `
		SELECT assignee_id, state, session_ended_at, entity_case_id
		FROM chat_conversation WHERE case_id = $1
	`, caseID).Scan(&assigneeID, &state, &sessionEndedAt, &entityCaseIDPtr)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: case_id=%s", ErrConversationNotFound, caseID)
	case err != nil:
		return fmt.Errorf("convert to case: diagnose: %w", err)
	}
	if sessionEndedAt != nil || entityCaseIDPtr != nil {
		return fmt.Errorf("%w: case_id=%s", ErrAlreadyConverted, caseID)
	}
	if assigneeID == nil || *assigneeID != userID || state != "ACTIVE" {
		return fmt.Errorf("%w: case_id=%s", ErrNotConversationOwner, caseID)
	}
	// Row matched every predicate on re-check -- a concurrent change lost a
	// race with the original UPDATE between the two queries. Rare, but
	// report it as a conflict rather than a false "not found".
	return fmt.Errorf("%w: case_id=%s (concurrent update)", ErrAlreadyConverted, caseID)
}

// ConvertToCase ends userID's session on caseID by converting it into a
// real case (entityCaseID, already created by the caller). Only the
// engineer currently holding an accepted (ACTIVE) caseID can convert it.
// The chat does not continue after conversion -- it sets session_ended_at
// and backfills the freed slot exactly like Completed does.
func (r *Router) ConvertToCase(ctx context.Context, userID, caseID, entityCaseID string) (ConvertToCaseResult, error) {
	var result ConvertToCaseResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE chat_conversation
			SET entity_case_id = $1, state = 'CONVERTED_CASE', session_ended_at = now(), updated_at = now()
			WHERE case_id = $2 AND assignee_id = $3 AND state = 'ACTIVE' AND session_ended_at IS NULL AND entity_case_id IS NULL
		`, entityCaseID, caseID, userID)
		if err != nil {
			return fmt.Errorf("convert to case: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return diagnoseConvertFailure(ctx, tx, caseID, userID)
		}

		// Mirrors Completed's own backfill exactly -- a slot just freed up.
		var (
			chatStatus    Status
			maxConcurrent int
		)
		err = tx.QueryRow(ctx, `
			SELECT chat_status, max_concurrent_chats FROM cs_engineer_status WHERE user_id = $1 FOR UPDATE
		`, userID).Scan(&chatStatus, &maxConcurrent)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("lock engineer: %w", err)
		}
		if chatStatus != StatusAvailable {
			return nil
		}
		activeCount, err := activeCaseCount(ctx, tx, userID)
		if err != nil {
			return err
		}
		if activeCount >= maxConcurrent {
			return nil
		}
		c, ok, err := claimOldestWaiting(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := assignCaseToEngineer(ctx, tx, userID, c); err != nil {
			return err
		}
		assigned := c
		result.AssignedCase = &assigned
		return nil
	})
	if err != nil {
		return ConvertToCaseResult{}, err
	}
	return result, nil
}

// DeclineResult is Decline's outcome: exactly one of ReassignedTo (set,
// with AssignedCase also set) or Requeued (true) applies, unless the
// decline itself was a no-op, in which case all three are zero.
type DeclineResult struct {
	// ReassignedTo is the user ID of the engineer the case was handed to
	// instead.
	ReassignedTo string `json:"reassignedTo,omitempty"`
	Requeued     bool   `json:"requeued,omitempty"`
	// AssignedCase is the declined case, set alongside ReassignedTo so the
	// caller can deliver it to that other engineer without having to
	// remember the case's own fields itself.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// Decline handles an engineer dismissing a case they were just assigned,
// before accepting it. Reassigns caseID to whichever other AVAILABLE
// engineer with spare capacity has handled the fewest chats today (same
// ranking Escalate uses, excluding the decliner); if none are free, flips
// the case's chat_queue row back to WAITING_FOR_ENGINEER, keeping its
// original queue position rather than sending the customer to the back of
// the line a second time. A no-op if caseID isn't currently an
// unconfirmed conversation assigned to userID -- declining doesn't affect
// any of userID's other concurrent cases.
func (r *Router) Decline(ctx context.Context, userID, caseID string) (DeclineResult, error) {
	var result DeclineResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		conv, ok, err := lockPendingConversation(ctx, tx, caseID, userID)
		if err != nil {
			return err
		}
		if !ok {
			result = DeclineResult{}
			return nil
		}

		if _, err := tx.Exec(ctx, `
			UPDATE chat_conversation
			SET assignee_id = NULL, accepted_at = NULL, updated_at = now()
			WHERE case_id = $1
		`, caseID); err != nil {
			return fmt.Errorf("clear declined conversation: %w", err)
		}

		// Audit trail: userID's ping on this conversation is settled as
		// REJECTED, independent of what happens to the case next.
		if _, err := tx.Exec(ctx, `
			INSERT INTO chat_queue_engineer_assignment (case_id, engineer_id, status)
			VALUES ($1, $2, 'REJECTED')
		`, conv.CaseID, userID); err != nil {
			return fmt.Errorf("record decline outcome: %w", err)
		}

		candidate, ok, err := popAvailableEngineer(ctx, tx, userID)
		if err != nil {
			return err
		}
		if ok {
			if err := assignCaseToEngineer(ctx, tx, candidate, conv); err != nil {
				return err
			}
			assigned := conv
			result = DeclineResult{ReassignedTo: candidate, AssignedCase: &assigned}
			return nil
		}

		if err := requeueWaiting(ctx, tx, conv.CaseID); err != nil {
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

// AcceptResult is Accept's outcome.
type AcceptResult struct {
	// Applied is true when userID had an unconfirmed case on exactly caseID
	// and has now been confirmed on it. False means the accept is stale --
	// the case was already declined/reassigned/requeued, or never held at
	// all -- and the caller should surface a "no longer available" response
	// rather than proceeding.
	Applied bool `json:"applied"`
}

// Accept confirms userID is actually accepting caseID: sets that specific
// chat_conversation row's accepted_at/state when it's still assigned to
// userID and unconfirmed. Only that one case is affected -- any other
// concurrent case userID holds is untouched either way. Any other state
// reports Applied: false rather than erroring -- "the thing you tried to
// accept isn't there anymore" is an expected race, not a server fault.
//
// Also deletes the case's chat_queue row -- a row lives from Escalate
// until exactly this moment, not until mere assignment.
func (r *Router) Accept(ctx context.Context, userID, caseID string) (AcceptResult, error) {
	var result AcceptResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		conv, ok, err := lockPendingConversation(ctx, tx, caseID, userID)
		if err != nil {
			return err
		}
		if !ok {
			result = AcceptResult{}
			return nil
		}

		if _, err := tx.Exec(ctx, `
			UPDATE chat_conversation SET state = 'ACTIVE', accepted_at = now(), updated_at = now()
			WHERE case_id = $1
		`, caseID); err != nil {
			return fmt.Errorf("accept case: %w", err)
		}

		if err := deleteQueueRow(ctx, tx, conv.CaseID); err != nil {
			return err
		}

		// Audit trail: userID's ping on this conversation is settled as
		// CONNECTED.
		if _, err := tx.Exec(ctx, `
			INSERT INTO chat_queue_engineer_assignment (case_id, engineer_id, status)
			VALUES ($1, $2, 'CONNECTED')
		`, conv.CaseID, userID); err != nil {
			return fmt.Errorf("record accept outcome: %w", err)
		}

		result = AcceptResult{Applied: true}
		return nil
	})
	if err != nil {
		return AcceptResult{}, err
	}
	return result, nil
}

// PresenceDetail is GetPresence's result: the engineer's manual chat_status,
// their concurrent-chat capacity and current load, and every case they're
// currently holding (pending or accepted alike) so a caller whose own UI
// state was lost -- a refresh, a closed tab -- can rehydrate all of it
// instead of leaving the engineer stuck with nothing to act on.
type PresenceDetail struct {
	ChatStatus         Status       `json:"chatStatus"`
	ActiveChats        int          `json:"activeChats"`
	MaxConcurrentChats int          `json:"maxConcurrentChats"`
	AtCapacity         bool         `json:"atCapacity"`
	Cases              []CaseStatus `json:"cases,omitempty"`
}

// GetPresence returns userID's current chat_status, capacity, and every
// case they're currently holding -- defaulting to OFFLINE/capacity 1/no
// cases for an engineer this database has never seen a presence update
// from.
func (r *Router) GetPresence(ctx context.Context, userID string) (PresenceDetail, error) {
	var (
		chatStatus    Status
		maxConcurrent int
	)
	err := r.db.QueryRow(ctx, `
		SELECT chat_status, max_concurrent_chats FROM cs_engineer_status WHERE user_id = $1
	`, userID).Scan(&chatStatus, &maxConcurrent)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return PresenceDetail{ChatStatus: StatusOffline, MaxConcurrentChats: 1}, nil
	case err != nil:
		return PresenceDetail{}, fmt.Errorf("router: get presence: %w", err)
	}

	cases, err := engineerCases(ctx, r.db, userID)
	if err != nil {
		return PresenceDetail{}, fmt.Errorf("router: get presence: %w", err)
	}
	return PresenceDetail{
		ChatStatus: chatStatus, ActiveChats: len(cases), MaxConcurrentChats: maxConcurrent,
		AtCapacity: len(cases) >= maxConcurrent, Cases: cases,
	}, nil
}

// ErrInvalidCapacity is returned by SetMaxConcurrentChats when max is
// outside cs_engineer_status.max_concurrent_chats's own CHECK constraint
// (1-10, lowered from 1-20 by 000020_lower_max_concurrent_chats -- Sajith
// confirmed 10 as the intended maximum) -- checked here too so a caller
// gets a clean, typed rejection instead of a raw constraint-violation
// error from Postgres.
var ErrInvalidCapacity = errors.New("max_concurrent_chats must be between 1 and 10")

// SetCapacityResult is SetMaxConcurrentChats's outcome.
type SetCapacityResult struct {
	// AssignedCases is set when raising the limit immediately drained the
	// waiting queue into this engineer's newly-opened capacity -- same
	// queue-drain semantics as PresenceResult.AssignedCases (more than one
	// case can land here at once), gated the same way SetPresence's own
	// drain is: only while this engineer is chat_status AVAILABLE. Lowering
	// the limit, or raising it while BUSY/OFFLINE, never claims anything --
	// see this method's own doc comment.
	AssignedCases []CaseInfo `json:"assignedCases,omitempty"`
}

// SetMaxConcurrentChats sets userID's configurable concurrent-chat
// capacity, creating their row (defaulting to OFFLINE, capacity 1) on
// first contact just like SetPresence does. This is the admin-facing
// counterpart to the manual `UPDATE cs_engineer_status` every engineer's
// capacity change went through before this existed (see the project's
// db-schema-review-2026-09-07-outcomes.md) -- now exposed so an engineer
// can set their own limit from the CSM portal's status menu.
//
// Never drops anything already assigned: lowering the limit below the
// current active count just stops new work from routing to them (via
// popAvailableEngineer/SetPresence's queue-drain, both of which compare
// against this same column) until they fall back under it.
//
// Raising the limit, however, can open up spare capacity immediately --
// and unlike a plain column update, this now drains the waiting queue into
// it right away (same as SetPresence's own transition to AVAILABLE), gated
// on chat_status already being AVAILABLE: an engineer who is BUSY or
// OFFLINE takes nothing here no matter how high they raise their limit,
// consistent with those statuses never claiming anything anywhere else.
// Without this, a queued customer who escalated while an engineer was at
// capacity would sit waiting even after that engineer freed up room for
// them, with nothing to prompt a claim until the engineer's next
// unrelated state change (going AVAILABLE again, completing another
// case) happened to trigger one.
func (r *Router) SetMaxConcurrentChats(ctx context.Context, userID string, max int) (SetCapacityResult, error) {
	if max < 1 || max > 10 {
		return SetCapacityResult{}, ErrInvalidCapacity
	}
	var result SetCapacityResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO cs_engineer_status (user_id) VALUES ($1)
			ON CONFLICT (user_id) DO NOTHING
		`, userID); err != nil {
			return fmt.Errorf("ensure engineer row: %w", err)
		}

		// Lock the row before reading chat_status -- same discipline
		// ensureAndLockEngineer applies for SetPresence, needed here too
		// since a concurrent presence/completion call must not race this
		// one's own queue-drain below.
		var chatStatus Status
		if err := tx.QueryRow(ctx, `
			SELECT chat_status FROM cs_engineer_status WHERE user_id = $1 FOR UPDATE
		`, userID).Scan(&chatStatus); err != nil {
			return fmt.Errorf("lock engineer row: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE cs_engineer_status SET max_concurrent_chats = $1, updated_at = now() WHERE user_id = $2
		`, max, userID); err != nil {
			return fmt.Errorf("set max_concurrent_chats: %w", err)
		}

		if chatStatus != StatusAvailable {
			return nil
		}
		activeCount, err := activeCaseCount(ctx, tx, userID)
		if err != nil {
			return err
		}
		assigned, err := drainQueueUpTo(ctx, tx, userID, activeCount, max)
		if err != nil {
			return err
		}
		result = SetCapacityResult{AssignedCases: assigned}
		return nil
	})
	if err != nil {
		return SetCapacityResult{}, err
	}
	return result, nil
}

// pgxQuerier is the subset of *pgxpool.Pool that engineerCases and
// commentsForCase/commentsForWorkItem (workitem.go) need -- satisfied
// directly by *pgxpool.Pool and by pgx.Tx alike, declared here just to
// name the dependency and let those helpers run either standalone
// (r.db) or as part of a larger transaction (tx).
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// engineerCases returns every case currently held by userID (state OPEN or
// ACTIVE, session not yet ended), oldest-assigned first. Shared by
// GetPresence and debugEngineers. PriorMessages on each CaseInfo is
// populated fresh from chat_routing.comment (see commentsForWorkItem), not
// trusted from the case_info JSONB decode -- work_item_id is selected
// alongside case_info for exactly that, avoiding a redundant case_id
// lookup (see commentsForCase, which every other read path uses instead).
func engineerCases(ctx context.Context, q pgxQuerier, userID string) ([]CaseStatus, error) {
	rows, err := q.Query(ctx, `
		SELECT work_item_id, case_info, state, accepted_at, updated_at
		FROM chat_conversation
		WHERE assignee_id = $1 AND state IN ('OPEN', 'ACTIVE') AND session_ended_at IS NULL
		ORDER BY updated_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query cases: %w", err)
	}
	defer rows.Close()

	var cases []CaseStatus
	for rows.Next() {
		var (
			workItemID   string
			caseInfoJSON []byte
			state        string
			acceptedAt   *time.Time
			updatedAt    time.Time
		)
		if err := rows.Scan(&workItemID, &caseInfoJSON, &state, &acceptedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan case: %w", err)
		}
		var c CaseInfo
		if caseInfoJSON != nil {
			if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
				return nil, fmt.Errorf("decode case info: %w", err)
			}
		}
		priorMessages, err := commentsForWorkItem(ctx, q, workItemID, c.CustomerEmail)
		if err != nil {
			return nil, fmt.Errorf("engineer cases: %w", err)
		}
		c.PriorMessages = priorMessages
		cases = append(cases, CaseStatus{
			CaseInfo:   c,
			Pending:    isPending(state, acceptedAt),
			AssignedAt: updatedAt.Format(time.RFC3339),
		})
	}
	return cases, rows.Err()
}

// DebugEngineer is one engineer's row in DebugState's dump.
type DebugEngineer struct {
	UserID             string       `json:"userId"`
	ChatStatus         Status       `json:"chatStatus"`
	MaxConcurrentChats int          `json:"maxConcurrentChats"`
	Cases              []CaseStatus `json:"cases,omitempty"`
	// ChatsToday is a live COUNT(*) over chat_conversation for today,
	// computed fresh on every call rather than read from a stored column.
	ChatsToday int `json:"chatsToday"`
}

// DebugState is the full dump GET /route/debug/state returns.
// Verification-only -- lets tests assert on internal state directly
// instead of inferring it from SSE side effects.
type DebugState struct {
	Engineers []DebugEngineer `json:"engineers"`
	Available []string        `json:"available"`
	Queue     []CaseInfo      `json:"queue"`
}

// DebugState snapshots the current engineer registry, available pool, and
// waiting queue. Three separate read-only queries rather than one
// transaction -- this is a debug/test endpoint, not a state transition, so
// a slightly stale cross-query view is an acceptable tradeoff for not
// holding locks.
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
		SELECT e.user_id, e.chat_status, e.max_concurrent_chats,
		       COALESCE(t.today_count, 0)
		FROM cs_engineer_status e
		LEFT JOIN (
			SELECT assignee_id AS user_id, COUNT(*) AS today_count
			FROM chat_conversation
			WHERE assignee_id IS NOT NULL
			  AND updated_at >= CURRENT_DATE AND updated_at < CURRENT_DATE + 1
			GROUP BY assignee_id
		) t ON t.user_id = e.user_id
		ORDER BY e.user_id
	`)
	if err != nil {
		return nil, fmt.Errorf("debug state: query engineers: %w", err)
	}

	type row struct {
		userID        string
		chatStatus    Status
		maxConcurrent int
		chatsToday    int
	}
	var raw []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.userID, &rr.chatStatus, &rr.maxConcurrent, &rr.chatsToday); err != nil {
			rows.Close()
			return nil, fmt.Errorf("debug state: scan engineer: %w", err)
		}
		raw = append(raw, rr)
	}
	rerr := rows.Err()
	rows.Close()
	if rerr != nil {
		return nil, fmt.Errorf("debug state: query engineers: %w", rerr)
	}

	engineers := []DebugEngineer{}
	for _, rr := range raw {
		cases, err := engineerCases(ctx, r.db, rr.userID)
		if err != nil {
			return nil, fmt.Errorf("debug state: %w", err)
		}
		engineers = append(engineers, DebugEngineer{
			UserID: rr.userID, ChatStatus: rr.chatStatus, MaxConcurrentChats: rr.maxConcurrent,
			Cases: cases, ChatsToday: rr.chatsToday,
		})
	}
	return engineers, nil
}

func (r *Router) debugAvailable(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT e.user_id
		FROM cs_engineer_status e
		LEFT JOIN (
			SELECT assignee_id AS user_id, COUNT(*) AS active_count
			FROM chat_conversation
			WHERE assignee_id IS NOT NULL AND state IN ('OPEN', 'ACTIVE') AND session_ended_at IS NULL
			GROUP BY assignee_id
		) t ON t.user_id = e.user_id
		WHERE e.chat_status = 'AVAILABLE' AND COALESCE(t.active_count, 0) < e.max_concurrent_chats
		ORDER BY COALESCE(t.active_count, 0) ASC, e.available_since ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("debug state: query available: %w", err)
	}
	defer rows.Close()

	available := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("debug state: scan available: %w", err)
		}
		available = append(available, userID)
	}
	return available, rows.Err()
}

func (r *Router) debugQueue(ctx context.Context) ([]CaseInfo, error) {
	rows, err := r.db.Query(ctx, `
		SELECT case_info FROM chat_queue
		WHERE status = 'WAITING_FOR_ENGINEER'
		ORDER BY created_at ASC, chat_conversation_id ASC
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

// ensureAndLockEngineer makes sure userID has a row (defaulting to
// OFFLINE, capacity 1), then locks it FOR UPDATE for the rest of tx and
// returns its configured max_concurrent_chats.
func ensureAndLockEngineer(ctx context.Context, tx pgx.Tx, userID string) (maxConcurrent int, err error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO cs_engineer_status (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING
	`, userID); err != nil {
		return 0, fmt.Errorf("ensure engineer row: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		SELECT max_concurrent_chats FROM cs_engineer_status WHERE user_id = $1 FOR UPDATE
	`, userID).Scan(&maxConcurrent); err != nil {
		return 0, fmt.Errorf("lock engineer row: %w", err)
	}
	return maxConcurrent, nil
}

// isPending reports whether a chat_conversation row (given its own state
// and accepted_at) is still awaiting Router.Accept. A pure function --
// unlike the old single-case isPendingAccept/isStuckPending helpers this
// replaces, there is now only one pending predicate, since chat_status no
// longer affects whether a specific conversation counts as confirmed (see
// the package doc comment).
func isPending(state string, acceptedAt *time.Time) bool {
	return state == "OPEN" && acceptedAt == nil
}

// activeCaseCount counts userID's currently-held cases (state OPEN or
// ACTIVE, session not yet ended) -- what's compared against
// max_concurrent_chats everywhere capacity is checked.
func activeCaseCount(ctx context.Context, tx pgx.Tx, userID string) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_conversation
		WHERE assignee_id = $1 AND state IN ('OPEN', 'ACTIVE') AND session_ended_at IS NULL
	`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active cases: %w", err)
	}
	return count, nil
}

// lockPendingConversation locks and returns caseID's CaseInfo if its
// chat_conversation row is currently assigned to userID, still unconfirmed
// (state OPEN, accepted_at NULL), and not yet ended -- or ok=false
// otherwise (already accepted, reassigned elsewhere, or never held by
// userID at all). Shared by Accept and Decline, which both only ever act
// on a case in exactly this state. PriorMessages is populated fresh from
// chat_routing.comment (see commentsForCase) before returning, same as
// every other CaseInfo this package hands back.
func lockPendingConversation(ctx context.Context, tx pgx.Tx, caseID, userID string) (CaseInfo, bool, error) {
	var (
		caseInfoJSON []byte
		assigneeID   *string
		state        string
		acceptedAt   *time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT case_info, assignee_id, state, accepted_at
		FROM chat_conversation WHERE case_id = $1 AND session_ended_at IS NULL
		FOR UPDATE
	`, caseID).Scan(&caseInfoJSON, &assigneeID, &state, &acceptedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return CaseInfo{}, false, nil
	case err != nil:
		return CaseInfo{}, false, fmt.Errorf("lock conversation: %w", err)
	}
	if assigneeID == nil || *assigneeID != userID || !isPending(state, acceptedAt) {
		return CaseInfo{}, false, nil
	}

	var c CaseInfo
	if caseInfoJSON != nil {
		if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
			return CaseInfo{}, false, fmt.Errorf("decode case info: %w", err)
		}
	}
	priorMessages, err := commentsForCase(ctx, tx, caseID)
	if err != nil {
		return CaseInfo{}, false, fmt.Errorf("lock conversation: %w", err)
	}
	c.PriorMessages = priorMessages
	return c, true, nil
}

// popAvailableEngineer locks and returns an AVAILABLE engineer, other than
// exclude (pass "" to exclude no one), with spare concurrent-chat capacity:
// whichever qualifying engineer has the fewest currently-active chats (so
// work spreads out before anyone is doubled up), ties broken by fewest
// chats taken today, then by who's been AVAILABLE longest -- or ok=false if
// none qualify. FOR UPDATE OF e SKIP LOCKED (scoped to the cs_engineer_
// status side of the joins, since the counts come from aggregate subqueries
// that aren't themselves lockable) lets concurrent callers each grab a
// different engineer instead of blocking on each other. Used by Escalate
// directly and by Decline's reassignment fallback.
func popAvailableEngineer(ctx context.Context, tx pgx.Tx, exclude string) (userID string, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT e.user_id
		FROM cs_engineer_status e
		LEFT JOIN (
			SELECT assignee_id AS user_id, COUNT(*) AS active_count
			FROM chat_conversation
			WHERE assignee_id IS NOT NULL AND state IN ('OPEN', 'ACTIVE') AND session_ended_at IS NULL
			GROUP BY assignee_id
		) active ON active.user_id = e.user_id
		LEFT JOIN (
			SELECT assignee_id AS user_id, COUNT(*) AS today_count
			FROM chat_conversation
			WHERE assignee_id IS NOT NULL
			  AND updated_at >= CURRENT_DATE AND updated_at < CURRENT_DATE + 1
			GROUP BY assignee_id
		) today ON today.user_id = e.user_id
		WHERE e.chat_status = 'AVAILABLE' AND e.user_id != $1
		  AND COALESCE(active.active_count, 0) < e.max_concurrent_chats
		ORDER BY COALESCE(active.active_count, 0) ASC, COALESCE(today.today_count, 0) ASC, e.available_since ASC
		LIMIT 1
		FOR UPDATE OF e SKIP LOCKED
	`, exclude).Scan(&userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("pop available engineer: %w", err)
	default:
		return userID, true, nil
	}
}

// ErrConversationNotFound is returned by assignCaseToEngineer (and so by
// every method that calls it -- Escalate, SetPresence, Completed, Decline,
// and timeoutOne) when c's chat_conversation row doesn't exist, or is
// already ended, at assignment time. This is always a genuine bug rather
// than an expected condition: csm-portal/backend's CreateWorkItem is
// supposed to create the row before ever calling Escalate (see Escalate's
// own doc comment), and every other call site either reuses a row it just
// locked in the same transaction (Decline, timeoutOne) or one that was
// already required to exist when it entered the queue (SetPresence's and
// Completed's drain). Previously this was a silent no-op: Escalate still
// reported a successful assignment even though nothing was actually
// persisted, which is exactly the gap a real end-to-end walkthrough caught
// (see the project's db-schema-review-2026-09-07-outcomes.md, "Manual
// capacity walkthrough" section) -- a request missing customerEmail never
// created a chat_conversation row, so the follow-on Escalate call "succeeded"
// while quietly assigning nothing.
var ErrConversationNotFound = errors.New("chat_conversation row not found for this case")

// assignCaseToEngineer records userID as c's assignee, reserving one unit
// of their concurrent-chat capacity. Deliberately never touches
// cs_engineer_status.chat_status -- unlike the old single-case model,
// taking a case no longer implies anything about an engineer's own manual
// status (see SetPresence's doc comment); a case counts toward capacity
// purely by existing as an OPEN/ACTIVE, non-ended chat_conversation row
// with this assignee_id. Returns ErrConversationNotFound, instead of
// silently doing nothing, if the row doesn't exist (or is already ended) --
// see ErrConversationNotFound's own doc comment for why this must never be
// a quiet no-op.
func assignCaseToEngineer(ctx context.Context, tx pgx.Tx, userID string, c CaseInfo) error {
	tag, err := tx.Exec(ctx, `
		UPDATE chat_conversation
		SET assignee_id = $1, accepted_at = NULL, updated_at = now()
		WHERE case_id = $2 AND session_ended_at IS NULL
	`, userID, c.CaseID)
	if err != nil {
		return fmt.Errorf("assign case to engineer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: case_id=%s", ErrConversationNotFound, c.CaseID)
	}
	return nil
}

// insertQueueRow creates c's chat_queue row with the given initial status
// -- WAITING_FOR_ENGINEER when Escalate found nobody free, ASSIGNED when
// it assigned someone immediately. position is c's 1-based place among
// every currently-waiting row (0 for an ASSIGNED row, since nothing's
// waiting on it) -- only meaningful for a WAITING_FOR_ENGINEER row.
//
// Keyed by c.CaseID, not c.ConversationID: chat_queue.chat_conversation_id
// (its column name, unchanged since before the caseId/conversationId
// split -- see migration 000015_redesign_chat_queue) tracks one specific
// live-chat escalation instance's place in the queue, which is exactly
// what a case identifies. Keying it by ConversationID instead would have
// made a customer's second, later escalation (a fresh CaseID but the same
// stable ConversationID, after their first case ended) collide with -- or
// silently reuse the row of -- their first, since this column is this
// table's own PRIMARY KEY.
func insertQueueRow(ctx context.Context, tx pgx.Tx, c CaseInfo, caseInfoJSON []byte, status queueStatus) (position int, err error) {
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_queue (chat_conversation_id, case_info, status)
		VALUES ($1, $2::jsonb, $3)
		RETURNING created_at
	`, c.CaseID, caseInfoJSON, status).Scan(&createdAt); err != nil {
		return 0, fmt.Errorf("insert queue row: %w", err)
	}
	if status != queueWaitingForEngineer {
		return 0, nil
	}

	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_queue
		WHERE status = 'WAITING_FOR_ENGINEER'
		  AND (created_at, chat_conversation_id) <= ($1, $2)
	`, createdAt, c.CaseID).Scan(&position); err != nil {
		return 0, fmt.Errorf("compute queue position: %w", err)
	}
	return position, nil
}

// claimOldestWaiting flips the oldest WAITING_FOR_ENGINEER row (by
// created_at, then chat_conversation_id to break ties) to ASSIGNED and
// returns its case, or ok=false if nothing is waiting. FOR UPDATE SKIP
// LOCKED inside the subquery, same job-queue idiom popAvailableEngineer
// uses. The row is updated in place, not deleted -- it lives until Accept.
// PriorMessages is populated fresh from chat_routing.comment (see
// commentsForCase) before returning, same as every other CaseInfo this
// package hands back -- the chat_queue.case_info blob decoded below is
// only a point-in-time snapshot from Escalate time.
func claimOldestWaiting(ctx context.Context, tx pgx.Tx) (CaseInfo, bool, error) {
	var caseInfoJSON []byte
	err := tx.QueryRow(ctx, `
		UPDATE chat_queue
		SET status = 'ASSIGNED'
		WHERE chat_conversation_id = (
			SELECT chat_conversation_id FROM chat_queue
			WHERE status = 'WAITING_FOR_ENGINEER'
			ORDER BY created_at ASC, chat_conversation_id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING case_info
	`).Scan(&caseInfoJSON)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return CaseInfo{}, false, nil
	case err != nil:
		return CaseInfo{}, false, fmt.Errorf("claim oldest waiting case: %w", err)
	}

	var c CaseInfo
	if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
		return CaseInfo{}, false, fmt.Errorf("decode queued case: %w", err)
	}
	priorMessages, err := commentsForCase(ctx, tx, c.CaseID)
	if err != nil {
		return CaseInfo{}, false, fmt.Errorf("claim oldest waiting case: %w", err)
	}
	c.PriorMessages = priorMessages
	return c, true, nil
}

// drainQueueUpTo claims and assigns queued cases to userID until either the
// waiting queue is empty or activeCount reaches maxConcurrent, returning
// every case assigned this way -- the shared loop body behind both
// SetPresence's transition to AVAILABLE and SetMaxConcurrentChats's
// capacity increase, the two places where opening up more than one slot at
// once is possible. Completed, ConvertToCase, and Decline's reassignment
// each free or move at most one case, so they claim inline instead of
// using this.
func drainQueueUpTo(ctx context.Context, tx pgx.Tx, userID string, activeCount, maxConcurrent int) ([]CaseInfo, error) {
	var assigned []CaseInfo
	for activeCount < maxConcurrent {
		c, ok, err := claimOldestWaiting(ctx, tx)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if err := assignCaseToEngineer(ctx, tx, userID, c); err != nil {
			return nil, err
		}
		assigned = append(assigned, c)
		activeCount++
	}
	return assigned, nil
}

// requeueWaiting flips caseID's existing chat_queue row back to
// WAITING_FOR_ENGINEER -- used by Decline and SweepExpiredPending/
// SweepAbandonedQueue when no other engineer is free to take the case over
// immediately. created_at is left untouched, so the case keeps its
// original place ahead of anything that arrived after it. The row is
// assumed to already exist: every case gets one at Escalate time (keyed by
// CaseID -- see insertQueueRow), removed only by Accept -- which, by
// definition, hasn't happened for a case that's being declined or timed
// out.
func requeueWaiting(ctx context.Context, tx pgx.Tx, caseID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE chat_queue SET status = 'WAITING_FOR_ENGINEER' WHERE chat_conversation_id = $1
	`, caseID); err != nil {
		return fmt.Errorf("requeue case: %w", err)
	}
	return nil
}

// deleteQueueRow removes caseID's chat_queue row outright -- called only
// from Accept, the one point where the row should go away.
func deleteQueueRow(ctx context.Context, tx pgx.Tx, caseID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM chat_queue WHERE chat_conversation_id = $1`, caseID); err != nil {
		return fmt.Errorf("delete queue row: %w", err)
	}
	return nil
}
