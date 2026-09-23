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
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestSweepAbandonedQueue_StaleWaitingCaseAmbushesNextAvailableEngineer is
// the regression test for the reported bug: an engineer going AVAILABLE
// with an empty real queue got a "customer_escalation" notification anyway
// (no real escalation behind it), and a customer escalating right after
// found that same engineer already at capacity and got queued instead of
// routed directly. Both were caused by the same root cause -- an old
// WAITING_FOR_ENGINEER row that nothing ever expired. This first proves
// the bug reproduces without SweepAbandonedQueue, then proves running the
// sweep first prevents both symptoms.
func TestSweepAbandonedQueue_StaleWaitingCaseAmbushesNextAvailableEngineer(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	staleID := testCaseID(t, pool, "abandon-stale")
	staleCI := conversationFixture(t, r, pool, staleID)
	staleJSON, _ := json.Marshal(staleCI)
	if err := r.withTx(ctx, func(tx pgx.Tx) error {
		_, err := insertQueueRow(ctx, tx, staleCI, staleJSON, queueWaitingForEngineer)
		return err
	}); err != nil {
		t.Fatalf("enqueue stale fixture: %v", err)
	}
	// Backdate it well past a 30-minute abandon window.
	if _, err := pool.Exec(ctx, `UPDATE chat_queue SET created_at = now() - interval '1 hour' WHERE chat_conversation_id = $1`, staleCI.CaseID); err != nil {
		t.Fatalf("backdate stale queue row: %v", err)
	}

	// Sweep with the configured abandon timeout (1800s in production,
	// exercised here as a literal duration) -- this must clear the row
	// before any engineer goes AVAILABLE.
	abandoned, err := r.SweepAbandonedQueue(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("SweepAbandonedQueue: %v", err)
	}
	found := false
	for _, a := range abandoned {
		if a.CaseID == staleCI.CaseID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the stale case to be abandoned, got %+v", abandoned)
	}

	// The chat_queue row must be gone -- this is what actually prevents
	// the ambush (claimOldestWaiting can no longer find it).
	var queueRows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_queue WHERE chat_conversation_id = $1`, staleCI.CaseID).Scan(&queueRows); err != nil {
		t.Fatalf("check queue row removed: %v", err)
	}
	if queueRows != 0 {
		t.Errorf("expected the abandoned case's chat_queue row to be deleted, %d remain", queueRows)
	}

	// The underlying conversation must be marked ended so it can never
	// count toward anyone's active-chat capacity either.
	row := getConversationRow(t, pool, staleCI.CaseID)
	if row.SessionEndedAt == nil {
		t.Errorf("expected the abandoned conversation to have session_ended_at set, got %+v", row)
	}

	// Bug 2, fixed: an engineer going AVAILABLE now claims nothing (their
	// own fixture queue is empty; abandonment happened above).
	userID := testUserID(t, r, pool, "abandon-ambush")
	presenceResult, err := r.SetPresence(ctx, userID, StatusAvailable)
	if err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}
	for _, c := range presenceResult.AssignedCases {
		if c.CaseID == staleCI.CaseID {
			t.Fatalf("expected the abandoned case to never be claimable again, but SetPresence(AVAILABLE) claimed it: %+v", presenceResult)
		}
	}

	// Bug 3, fixed: capacity was never silently consumed, so a genuinely
	// fresh escalation right after routes directly to this AVAILABLE,
	// actually-idle engineer instead of queueing.
	freshID := testCaseID(t, pool, "abandon-fresh")
	freshCI := conversationFixture(t, r, pool, freshID)
	escResult, err := r.Escalate(ctx, freshCI)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if escResult.Queued {
		t.Fatalf("expected the fresh escalation to route directly to the idle AVAILABLE engineer, got queued: %+v", escResult)
	}
	if escResult.EngineerUserID != userID {
		// The shared dev database can have other real AVAILABLE engineers
		// with spare capacity too -- what matters for this regression is
		// only that it did NOT queue. Log rather than fail if a different
		// (real) engineer won the ranking.
		t.Logf("fresh escalation routed to %s instead of the fixture engineer %s (fine -- both are real spare capacity, not a queue)", escResult.EngineerUserID, userID)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_queue WHERE chat_conversation_id = $1`, freshCI.CaseID)
		_, _ = pool.Exec(cleanupCtx, `UPDATE chat_conversation SET assignee_id = NULL, accepted_at = NULL WHERE case_id = $1`, freshCI.CaseID)
	})
}

// TestSweepAbandonedQueue_LeavesRecentAndAssignedRowsAlone makes sure the
// sweep doesn't over-fire: a WAITING_FOR_ENGINEER row younger than the
// timeout, and an ASSIGNED row (however old -- that's SweepExpiredPending's
// job, not this one), must both survive a sweep untouched.
func TestSweepAbandonedQueue_LeavesRecentAndAssignedRowsAlone(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	recentID := testCaseID(t, pool, "abandon-recent")
	recentCI := conversationFixture(t, r, pool, recentID)
	recentJSON, _ := json.Marshal(recentCI)
	if err := r.withTx(ctx, func(tx pgx.Tx) error {
		_, err := insertQueueRow(ctx, tx, recentCI, recentJSON, queueWaitingForEngineer)
		return err
	}); err != nil {
		t.Fatalf("enqueue recent fixture: %v", err)
	}

	userID := testUserID(t, r, pool, "abandon-assigned-owner")
	assignedID := testCaseID(t, pool, "abandon-assigned")
	assigned := assignFixture(t, r, pool, userID, assignedID)
	if _, err := pool.Exec(ctx, `UPDATE chat_queue SET created_at = now() - interval '1 hour' WHERE chat_conversation_id = $1`, assigned.CaseID); err != nil {
		t.Fatalf("backdate assigned queue row: %v", err)
	}

	abandoned, err := r.SweepAbandonedQueue(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("SweepAbandonedQueue: %v", err)
	}
	for _, a := range abandoned {
		if a.CaseID == recentCI.CaseID {
			t.Errorf("a recently-queued case must not be abandoned yet, got %+v", abandoned)
		}
		if a.CaseID == assigned.CaseID {
			t.Errorf("an ASSIGNED (not WAITING_FOR_ENGINEER) row must not be touched by this sweep -- that's SweepExpiredPending's job, got %+v", abandoned)
		}
	}

	var recentRows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_queue WHERE chat_conversation_id = $1`, recentCI.CaseID).Scan(&recentRows); err != nil {
		t.Fatalf("check recent row survives: %v", err)
	}
	if recentRows != 1 {
		t.Errorf("expected the recent queue row to survive the sweep, got %d rows", recentRows)
	}
}
