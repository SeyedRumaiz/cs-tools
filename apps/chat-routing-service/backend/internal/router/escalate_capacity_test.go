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
	"errors"
	"testing"
)

// TestEscalate_FourRealEscalationsLandOnCapacityFourEngineer exercises the
// full concurrent-capacity path end to end through the real Escalate()
// entry point (not the assignFixture shortcut used by
// TestConcurrentCapacity_SecondCaseAssignedWithoutQueueingWhenCapacityTwo),
// at a capacity higher than the 2 already covered elsewhere. An engineer
// configured for max_concurrent_chats = 4 must actually receive all 4
// escalations directly (no queueing), and a 5th, over-capacity escalation
// must not land on them.
func TestEscalate_FourRealEscalationsLandOnCapacityFourEngineer(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	userID := testUserID(t, r, pool, "capacity-four-escalate")
	setMaxConcurrent(t, pool, userID, 4)
	if _, err := r.SetPresence(ctx, userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}

	for i := 0; i < 4; i++ {
		id := testCaseID(t, pool, "capacity-four-case")
		ci := conversationFixture(t, r, pool, id)
		res, err := r.Escalate(ctx, ci)
		if err != nil {
			t.Fatalf("Escalate #%d: %v", i+1, err)
		}
		if res.Queued {
			t.Errorf("Escalate #%d: expected direct routing (capacity 4, %d active so far), got queued: %+v", i+1, i, res)
		}
	}

	// A 5th, over capacity, must not land on this already-full engineer.
	fifthID := testCaseID(t, pool, "capacity-four-fifth")
	fifthCI := conversationFixture(t, r, pool, fifthID)
	fifthRes, err := r.Escalate(ctx, fifthCI)
	if err != nil {
		t.Fatalf("Escalate #5: %v", err)
	}
	if fifthRes.EngineerUserID == userID {
		t.Errorf("expected the 5th escalation to NOT land on the already-at-capacity engineer, got: %+v", fifthRes)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_conversation
		WHERE assignee_id = $1 AND state IN ('OPEN','ACTIVE') AND session_ended_at IS NULL
	`, userID).Scan(&activeCount); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if activeCount != 4 {
		t.Fatalf("expected exactly 4 active chats actually assigned to the capacity-4 engineer, got %d", activeCount)
	}
}

// TestEscalate_ErrorsInsteadOfSilentlyNoOpingWhenConversationRowMissing
// guards against the exact gap a live manual walkthrough caught (see the
// project's db-schema-review-2026-09-07-outcomes.md, "Manual capacity
// walkthrough" section): a caller that reaches Escalate without first
// creating the case's chat_conversation row (e.g. csm-portal/backend's
// CreateWorkItem call failed or was skipped) used to get back a
// success-shaped EscalateResult{EngineerUserID: ...} while
// assignCaseToEngineer silently updated zero rows -- the case was never
// actually recorded as assigned to anyone. Escalate must now surface
// ErrConversationNotFound instead of pretending it worked.
func TestEscalate_ErrorsInsteadOfSilentlyNoOpingWhenConversationRowMissing(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	userID := testUserID(t, r, pool, "missing-conversation")
	if _, err := r.SetPresence(ctx, userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}

	// Deliberately skip conversationFixture -- no chat_conversation row
	// exists for this case, unlike every other test in this package.
	caseID := testCaseID(t, pool, "missing-conversation-case")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: "router-test@example.com",
	}

	_, err := r.Escalate(ctx, ci)
	if err == nil {
		t.Fatal("expected Escalate to fail when no chat_conversation row exists for the case, got nil error")
	}
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("expected errors.Is(err, ErrConversationNotFound), got: %v", err)
	}

	// The whole transaction -- including the chat_queue row Escalate had
	// inserted before hitting the error -- must have rolled back. Silently
	// leaving that row behind as ASSIGNED with no real assignee would be
	// its own, quieter version of the same bug.
	var queueRowCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_queue WHERE chat_conversation_id = $1
	`, ci.CaseID).Scan(&queueRowCount); err != nil {
		t.Fatalf("count chat_queue rows: %v", err)
	}
	if queueRowCount != 0 {
		t.Fatalf("expected the failed Escalate's chat_queue insert to roll back, found %d row(s)", queueRowCount)
	}
}
