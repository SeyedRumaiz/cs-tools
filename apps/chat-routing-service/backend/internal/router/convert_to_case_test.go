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

	"github.com/jackc/pgx/v5/pgxpool"
)

// acceptedFixture builds caseID's conversation, assigns it to userID, and
// accepts it -- the ACTIVE precondition ConvertToCase requires, matching
// the assignFixture-then-Accept pattern the Accept tests already use.
func acceptedFixture(t *testing.T, r *Router, pool *pgxpool.Pool, userID, caseID string) CaseInfo {
	t.Helper()
	ci := assignFixture(t, r, pool, userID, caseID)
	if _, err := r.Accept(context.Background(), userID, caseID); err != nil {
		t.Fatalf("acceptedFixture: Accept: %v", err)
	}
	return ci
}

func TestConvertToCase_EndsSessionAndRecordsEntityCaseID(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	userID := testUserID(t, r, pool, "convert-happy")
	caseID := testCaseID(t, pool, "convert-happy")
	acceptedFixture(t, r, pool, userID, caseID)

	result, err := r.ConvertToCase(ctx, userID, caseID, "CS-1234")
	if err != nil {
		t.Fatalf("ConvertToCase: %v", err)
	}
	if result.AssignedCase != nil {
		t.Errorf("expected no backfill with an empty queue, got %+v", result.AssignedCase)
	}

	row := getConversationRow(t, pool, caseID)
	if row.State != "CONVERTED_CASE" {
		t.Errorf("expected state CONVERTED_CASE, got %q", row.State)
	}

	var (
		entityCaseID   *string
		sessionEndedAt *string
	)
	if err := pool.QueryRow(ctx, `
		SELECT entity_case_id, session_ended_at::text FROM chat_conversation WHERE case_id = $1
	`, caseID).Scan(&entityCaseID, &sessionEndedAt); err != nil {
		t.Fatalf("query converted row: %v", err)
	}
	if entityCaseID == nil || *entityCaseID != "CS-1234" {
		t.Errorf("expected entity_case_id CS-1234, got %v", entityCaseID)
	}
	if sessionEndedAt == nil {
		t.Error("expected session_ended_at to be set, exactly like Completed sets it")
	}

	// activeCaseCount takes a pgx.Tx, not a pool -- query the same condition
	// directly against the pool instead of opening a throwaway transaction.
	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_conversation
		WHERE assignee_id = $1 AND state IN ('OPEN', 'ACTIVE') AND session_ended_at IS NULL
	`, userID).Scan(&activeCount); err != nil {
		t.Fatalf("count active cases: %v", err)
	}
	if activeCount != 0 {
		t.Errorf("expected a converted chat to no longer count as active, got %d", activeCount)
	}
}

func TestConvertToCase_BackfillsQueueWhenCapacityFrees(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	userID := testUserID(t, r, pool, "convert-backfill")
	setMaxConcurrent(t, pool, userID, 1)
	if _, err := r.SetPresence(ctx, userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}

	caseID := testCaseID(t, pool, "convert-backfill-active")
	acceptedFixture(t, r, pool, userID, caseID)

	// A second case queues behind the first, since capacity is 1.
	waitingID := testCaseID(t, pool, "convert-backfill-waiting")
	waitingCI := conversationFixture(t, r, pool, waitingID)
	escalated, err := r.Escalate(ctx, waitingCI)
	if err != nil {
		t.Fatalf("Escalate (waiting case): %v", err)
	}
	if !escalated.Queued {
		t.Fatalf("expected the second case to queue behind a full-capacity engineer, got %+v", escalated)
	}

	result, err := r.ConvertToCase(ctx, userID, caseID, "CS-5678")
	if err != nil {
		t.Fatalf("ConvertToCase: %v", err)
	}
	if result.AssignedCase == nil || result.AssignedCase.CaseID != waitingID {
		t.Fatalf("expected converting to free a slot and backfill the waiting case, got %+v", result)
	}
}

func TestConvertToCase_RejectsWrongEngineer(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	owner := testUserID(t, r, pool, "convert-wrong-owner")
	other := testUserID(t, r, pool, "convert-wrong-other")
	caseID := testCaseID(t, pool, "convert-wrong-engineer")
	acceptedFixture(t, r, pool, owner, caseID)

	_, err := r.ConvertToCase(ctx, other, caseID, "CS-0000")
	if !errors.Is(err, ErrNotConversationOwner) {
		t.Fatalf("expected ErrNotConversationOwner for a non-owning engineer, got: %v", err)
	}
}

func TestConvertToCase_RejectsWhenNotYetAccepted(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	userID := testUserID(t, r, pool, "convert-not-accepted")
	caseID := testCaseID(t, pool, "convert-not-accepted")
	// Assigned but never Accept()-ed -- still state OPEN.
	assignFixture(t, r, pool, userID, caseID)

	_, err := r.ConvertToCase(ctx, userID, caseID, "CS-0001")
	if !errors.Is(err, ErrNotConversationOwner) {
		t.Fatalf("expected ErrNotConversationOwner for an unaccepted (OPEN) case, got: %v", err)
	}
}

func TestConvertToCase_RejectsDoubleConversion(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	userID := testUserID(t, r, pool, "convert-double")
	caseID := testCaseID(t, pool, "convert-double")
	acceptedFixture(t, r, pool, userID, caseID)

	if _, err := r.ConvertToCase(ctx, userID, caseID, "CS-1111"); err != nil {
		t.Fatalf("first ConvertToCase: %v", err)
	}

	_, err := r.ConvertToCase(ctx, userID, caseID, "CS-2222")
	if !errors.Is(err, ErrAlreadyConverted) {
		t.Fatalf("expected ErrAlreadyConverted on a second conversion attempt, got: %v", err)
	}
}

func TestConvertToCase_RejectsUnknownCase(t *testing.T) {
	r, _ := newTestRouter(t)
	_, err := r.ConvertToCase(context.Background(), "nobody", "no-such-case", "CS-0000")
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("expected ErrConversationNotFound for an unknown case, got: %v", err)
	}
}

func TestGetCaseInfo_ReturnsStoredCaseInfo(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	caseID := testCaseID(t, pool, "getcaseinfo")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		ProjectID: "proj-123", Subject: "Needs escalation",
		CustomerEmail: "customer@example.com", CustomerName: "A Customer",
		Message: "please help",
	}
	if err := r.CreateWorkItem(ctx, ci); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	t.Cleanup(func() {
		var workItemID string
		_ = pool.QueryRow(context.Background(), `SELECT work_item_id FROM chat_conversation WHERE case_id = $1`, caseID).Scan(&workItemID)
		if workItemID != "" {
			_, _ = pool.Exec(context.Background(), `DELETE FROM chat_queue_engineer_assignment WHERE case_id = $1`, caseID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM chat_queue WHERE chat_conversation_id = $1`, caseID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM comment WHERE work_item_id = $1`, workItemID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM chat_conversation WHERE work_item_id = $1`, workItemID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM work_item WHERE id = $1`, workItemID)
		}
	})

	got, err := r.GetCaseInfo(ctx, caseID)
	if err != nil {
		t.Fatalf("GetCaseInfo: %v", err)
	}
	if got.ProjectID != ci.ProjectID || got.Subject != ci.Subject || got.CustomerEmail != ci.CustomerEmail ||
		got.CustomerName != ci.CustomerName || got.Message != ci.Message {
		t.Errorf("GetCaseInfo mismatch: got %+v, want %+v", got, ci)
	}
}

func TestGetCaseInfo_UnknownCase(t *testing.T) {
	r, _ := newTestRouter(t)
	_, err := r.GetCaseInfo(context.Background(), "no-such-case")
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("expected ErrConversationNotFound for an unknown case, got: %v", err)
	}
}
