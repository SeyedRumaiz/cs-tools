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
	"testing"
	"time"
)

func TestSweepStaleAcceptedSessions_EndsIdleAcceptedSessionAndFreesCapacity(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	userID := testUserID(t, r, pool, "stale-owner")
	caseID := testCaseID(t, pool, "stale-idle")
	fixture := assignFixture(t, r, pool, userID, caseID)
	if _, err := r.Accept(ctx, userID, caseID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE chat_conversation SET updated_at = now() - interval '2 days' WHERE case_id = $1`, caseID); err != nil {
		t.Fatalf("backdate accepted session: %v", err)
	}

	results, err := r.SweepStaleAcceptedSessions(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStaleAcceptedSessions: %v", err)
	}
	found := false
	for _, res := range results {
		if res.CaseID == caseID {
			found = true
			if res.AssigneeID != userID {
				t.Errorf("expected AssigneeID %s, got %s", userID, res.AssigneeID)
			}
		}
	}
	if !found {
		t.Fatalf("expected the idle accepted session to be ended, got %+v", results)
	}

	row := getConversationRow(t, pool, caseID)
	if row.SessionEndedAt == nil {
		t.Errorf("expected session_ended_at to be set, got %+v", row)
	}

	// Capacity must actually be freed. Checked directly via GetPresence
	// rather than by racing a fresh Escalate call against this shared
	// dev/CI database's own other real AVAILABLE engineers, which could
	// legitimately win the assignment themselves (same caveat
	// abandon_test.go's own ambush test calls out).
	presence, err := r.GetPresence(ctx, userID)
	if err != nil {
		t.Fatalf("GetPresence: %v", err)
	}
	if presence.ActiveChats != 0 {
		t.Errorf("expected the freed engineer to have 0 active chats, got %+v", presence)
	}
	_ = fixture
}

func TestSweepStaleAcceptedSessions_LeavesRecentAndUnacceptedRowsAlone(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	// A PENDING (assigned, not yet accepted) case must never be touched by
	// this sweep -- that's SweepExpiredPending's job.
	pendingUser := testUserID(t, r, pool, "stale-pending-owner")
	pendingCase := testCaseID(t, pool, "stale-pending")
	pendingFixture := assignFixture(t, r, pool, pendingUser, pendingCase)
	if _, err := pool.Exec(ctx, `UPDATE chat_conversation SET updated_at = now() - interval '2 days' WHERE case_id = $1`, pendingCase); err != nil {
		t.Fatalf("backdate pending case: %v", err)
	}

	// A recently-active accepted case must survive even with an old
	// accepted_at, since AddComment keeps bumping updated_at.
	recentUser := testUserID(t, r, pool, "stale-recent-owner")
	recentCase := testCaseID(t, pool, "stale-recent")
	assignFixture(t, r, pool, recentUser, recentCase)
	if _, err := r.Accept(ctx, recentUser, recentCase); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE chat_conversation SET accepted_at = now() - interval '2 days' WHERE case_id = $1`, recentCase); err != nil {
		t.Fatalf("backdate accepted_at only: %v", err)
	}
	if err := r.AddComment(ctx, recentCase, "engineer@example.com", "still here"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	results, err := r.SweepStaleAcceptedSessions(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStaleAcceptedSessions: %v", err)
	}
	for _, res := range results {
		if res.CaseID == pendingCase {
			t.Errorf("a PENDING (not yet accepted) case must not be touched by this sweep, got %+v", results)
		}
		if res.CaseID == recentCase {
			t.Errorf("a case kept active by a recent AddComment must not be ended, got %+v", results)
		}
	}

	pendingRow := getConversationRow(t, pool, pendingCase)
	if pendingRow.SessionEndedAt != nil {
		t.Errorf("pending case must remain open, got %+v", pendingRow)
	}
	recentRow := getConversationRow(t, pool, recentCase)
	if recentRow.SessionEndedAt != nil {
		t.Errorf("recently-active case must remain open, got %+v", recentRow)
	}

	_ = pendingFixture
}
