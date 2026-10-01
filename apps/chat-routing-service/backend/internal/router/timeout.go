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
)

// TimeoutResult is one conversation's outcome from a timeout sweep,
// mirroring DeclineResult's shape: exactly one of ReassignedTo (with
// AssignedCase) or Requeued applies. Only the timed-out conversation is
// affected — the engineer's other cases and chat_status are untouched.
type TimeoutResult struct {
	// UserID is the engineer who was PENDING on CaseID past the timeout.
	UserID       string    `json:"userId"`
	CaseID       string    `json:"caseId"`
	ReassignedTo string    `json:"reassignedTo,omitempty"`
	Requeued     bool      `json:"requeued,omitempty"`
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

type pendingCandidate struct {
	userID string
	caseID string
}

// SweepExpiredPending finds every chat_conversation that's been assigned
// to an engineer for at least timeout without being confirmed via Accept,
// and treats each one like an explicit Decline: reassign to the next
// available engineer with spare capacity, excluding the unresponsive one,
// or flip the case back to WAITING_FOR_ENGINEER.
func (r *Router) SweepExpiredPending(ctx context.Context, timeout time.Duration) ([]TimeoutResult, error) {
	rows, err := r.db.Query(ctx, `
		SELECT assignee_id, case_id FROM chat_conversation
		WHERE assignee_id IS NOT NULL AND state = 'OPEN' AND accepted_at IS NULL
		  AND session_ended_at IS NULL AND updated_at < now() - make_interval(secs => $1)
	`, timeout.Seconds())
	if err != nil {
		return nil, fmt.Errorf("router: scan expired pending: %w", err)
	}
	var candidates []pendingCandidate
	for rows.Next() {
		var c pendingCandidate
		if err := rows.Scan(&c.userID, &c.caseID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("router: scan expired pending row: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: scan expired pending: %w", err)
	}

	var results []TimeoutResult
	for _, c := range candidates {
		result, err := r.timeoutOne(ctx, c.userID, c.caseID, timeout)
		if err != nil {
			return results, fmt.Errorf("router: time out %s on %s: %w", c.userID, c.caseID, err)
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results, nil
}

// timeoutOne re-verifies and applies a single conversation's timeout under
// its own row lock — a no-op if the engineer already accepted, declined,
// or was reassigned since SweepExpiredPending's unlocked scan.
func (r *Router) timeoutOne(ctx context.Context, userID, caseID string, timeout time.Duration) (*TimeoutResult, error) {
	var result *TimeoutResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var (
			caseInfoJSON []byte
			assigneeID   *string
			state        string
			acceptedAt   *time.Time
			updatedAt    time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT case_info, assignee_id, state, accepted_at, updated_at
			FROM chat_conversation WHERE case_id = $1 AND session_ended_at IS NULL
			FOR UPDATE
		`, caseID).Scan(&caseInfoJSON, &assigneeID, &state, &acceptedAt, &updatedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("lock conversation: %w", err)
		}
		if assigneeID == nil || *assigneeID != userID || !isPending(state, acceptedAt) || time.Since(updatedAt) < timeout {
			return nil
		}

		var timedOut CaseInfo
		if caseInfoJSON != nil {
			if err := json.Unmarshal(caseInfoJSON, &timedOut); err != nil {
				return fmt.Errorf("decode case info: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE chat_conversation
			SET assignee_id = NULL, accepted_at = NULL, updated_at = now()
			WHERE case_id = $1
		`, caseID); err != nil {
			return fmt.Errorf("clear timed-out conversation: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO chat_queue_engineer_assignment (case_id, engineer_id, status)
			VALUES ($1, $2, 'TIMED_OUT')
		`, caseID, userID); err != nil {
			return fmt.Errorf("record timeout outcome: %w", err)
		}

		candidate, ok, err := popAvailableEngineer(ctx, tx, userID)
		if err != nil {
			return err
		}
		if ok {
			if err := assignCaseToEngineer(ctx, tx, candidate, timedOut); err != nil {
				return err
			}
			assigned := timedOut
			result = &TimeoutResult{UserID: userID, CaseID: caseID, ReassignedTo: candidate, AssignedCase: &assigned}
			return nil
		}

		if err := requeueWaiting(ctx, tx, caseID); err != nil {
			return err
		}
		result = &TimeoutResult{UserID: userID, CaseID: caseID, Requeued: true}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AbandonedResult is one case SweepAbandonedQueue gave up on — it sat in
// chat_queue as WAITING_FOR_ENGINEER (never assigned to anyone at all,
// unlike TimeoutResult's PENDING-but-unconfirmed case) for longer than the
// configured abandon timeout.
type AbandonedResult struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
}

// abandonedCandidate is one row from SweepAbandonedQueue's initial,
// unlocked scan, re-verified under lock by abandonOne. caseID (not
// conversationID) since chat_queue.chat_conversation_id is keyed by
// CaseID.
type abandonedCandidate struct {
	caseID string
}

// SweepAbandonedQueue gives up on every chat_queue row that's sat
// WAITING_FOR_ENGINEER (never assigned to anyone) past timeout: deletes
// the queue row and marks the conversation ended, so it can't later be
// claimed by mistake by the next engineer to go AVAILABLE. Polled
// alongside SweepExpiredPending; each call is a snapshot.
func (r *Router) SweepAbandonedQueue(ctx context.Context, timeout time.Duration) ([]AbandonedResult, error) {
	rows, err := r.db.Query(ctx, `
		SELECT chat_conversation_id FROM chat_queue
		WHERE status = 'WAITING_FOR_ENGINEER' AND created_at < now() - make_interval(secs => $1)
	`, timeout.Seconds())
	if err != nil {
		return nil, fmt.Errorf("router: scan abandoned queue rows: %w", err)
	}
	var candidates []abandonedCandidate
	for rows.Next() {
		var c abandonedCandidate
		if err := rows.Scan(&c.caseID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("router: scan abandoned queue row: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: scan abandoned queue rows: %w", err)
	}

	var results []AbandonedResult
	for _, c := range candidates {
		result, err := r.abandonOne(ctx, c.caseID, timeout)
		if err != nil {
			return results, fmt.Errorf("router: abandon %s: %w", c.caseID, err)
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results, nil
}

// abandonOne re-verifies and applies a single queue row's abandonment
// under its own row lock — a no-op if the row was claimed or accepted
// since SweepAbandonedQueue's unlocked scan.
func (r *Router) abandonOne(ctx context.Context, caseID string, timeout time.Duration) (*AbandonedResult, error) {
	var result *AbandonedResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var (
			caseInfoJSON []byte
			status       queueStatus
			createdAt    time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT case_info, status, created_at FROM chat_queue
			WHERE chat_conversation_id = $1
			FOR UPDATE
		`, caseID).Scan(&caseInfoJSON, &status, &createdAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("lock queue row: %w", err)
		}
		if status != queueWaitingForEngineer || time.Since(createdAt) < timeout {
			return nil
		}

		var c CaseInfo
		if caseInfoJSON != nil {
			if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
				return fmt.Errorf("decode case info: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM chat_queue WHERE chat_conversation_id = $1`, caseID); err != nil {
			return fmt.Errorf("delete abandoned queue row: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE chat_conversation SET session_ended_at = now(), updated_at = now()
			WHERE case_id = $1 AND session_ended_at IS NULL
		`, c.CaseID); err != nil {
			return fmt.Errorf("mark abandoned conversation ended: %w", err)
		}

		// Use the decoded c.ConversationID, not the queue-key caseID param
		// (this row's CaseID) — the two are not guaranteed equal.
		result = &AbandonedResult{CaseID: c.CaseID, ConversationID: c.ConversationID}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// StaleSessionResult is one accepted session SweepStaleAcceptedSessions
// force-ended for going idle too long.
type StaleSessionResult struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	// AssigneeID is the engineer who was holding the session -- their
	// capacity is freed the same way an explicit Completed call frees it.
	AssigneeID string `json:"assigneeId"`
	// AssignedCase is set when freeing that capacity immediately drained
	// the waiting queue into it, same semantics as CompletedResult's own
	// field of this name.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

type staleCandidate struct {
	caseID string
}

// SweepStaleAcceptedSessions ends every accepted session (state ACTIVE)
// idle for at least timeout, freeing the assigned engineer's capacity and
// backfilling from the queue the same way Completed would — catching a
// session an engineer forgot to complete, which would otherwise stay open
// indefinitely and block that customer's next escalation via
// CreateWorkItem's duplicate-open-chat check.
func (r *Router) SweepStaleAcceptedSessions(ctx context.Context, timeout time.Duration) ([]StaleSessionResult, error) {
	rows, err := r.db.Query(ctx, `
		SELECT case_id FROM chat_conversation
		WHERE state = 'ACTIVE' AND accepted_at IS NOT NULL AND session_ended_at IS NULL
		  AND updated_at < now() - make_interval(secs => $1)
	`, timeout.Seconds())
	if err != nil {
		return nil, fmt.Errorf("router: scan stale accepted sessions: %w", err)
	}
	var candidates []staleCandidate
	for rows.Next() {
		var c staleCandidate
		if err := rows.Scan(&c.caseID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("router: scan stale accepted session row: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: scan stale accepted sessions: %w", err)
	}

	var results []StaleSessionResult
	for _, c := range candidates {
		result, err := r.staleSessionOne(ctx, c.caseID, timeout)
		if err != nil {
			return results, fmt.Errorf("router: end stale session %s: %w", c.caseID, err)
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results, nil
}

// staleSessionOne re-verifies and applies a single session's staleness
// under its own row lock — a no-op if the session got a new message, was
// completed, or reassigned since SweepStaleAcceptedSessions's unlocked
// scan.
func (r *Router) staleSessionOne(ctx context.Context, caseID string, timeout time.Duration) (*StaleSessionResult, error) {
	var result *StaleSessionResult
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		var (
			caseInfoJSON []byte
			assigneeID   *string
			state        string
			acceptedAt   *time.Time
			updatedAt    time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT case_info, assignee_id, state, accepted_at, updated_at
			FROM chat_conversation WHERE case_id = $1 AND session_ended_at IS NULL
			FOR UPDATE
		`, caseID).Scan(&caseInfoJSON, &assigneeID, &state, &acceptedAt, &updatedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("lock conversation: %w", err)
		}
		if assigneeID == nil || state != "ACTIVE" || acceptedAt == nil || time.Since(updatedAt) < timeout {
			return nil
		}

		var c CaseInfo
		if caseInfoJSON != nil {
			if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
				return fmt.Errorf("decode case info: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE chat_conversation SET session_ended_at = now(), updated_at = now()
			WHERE case_id = $1
		`, caseID); err != nil {
			return fmt.Errorf("mark stale session ended: %w", err)
		}

		completed, err := endSessionAndBackfill(ctx, tx, assigneeID)
		if err != nil {
			return err
		}

		result = &StaleSessionResult{
			CaseID:         caseID,
			ConversationID: c.ConversationID,
			AssigneeID:     *assigneeID,
			AssignedCase:   completed.AssignedCase,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
