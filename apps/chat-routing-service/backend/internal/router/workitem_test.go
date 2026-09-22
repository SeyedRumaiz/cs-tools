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

// Integration tests for the pre-escalation chat history persistence
// feature: CreateWorkItem's PriorMessages handling, AddComment's append
// behavior on top of it, and GetCaseInfo's fresh read-back. Same conventions
// as state_test.go (see that file's own doc comment) -- a real Postgres
// database via newTestRouter, t.Skip when one isn't configured, and fixtures
// that only ever touch case/user IDs they generate themselves.
package router

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// workItemFixture calls CreateWorkItem for ci exactly as given -- unlike
// state_test.go's conversationFixture, which always builds a fixed-shape
// CaseInfo, this lets a test set PriorMessages/Message itself -- and
// registers cleanup of the work_item/chat_conversation/comment rows it
// creates, mirroring conversationFixture's own cleanup block.
func workItemFixture(t *testing.T, r *Router, pool *pgxpool.Pool, ci CaseInfo) {
	t.Helper()
	if err := r.CreateWorkItem(context.Background(), ci); err != nil {
		t.Fatalf("workItemFixture: CreateWorkItem: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var workItemID string
		_ = pool.QueryRow(cleanupCtx, `SELECT work_item_id FROM chat_conversation WHERE case_id = $1`, ci.CaseID).Scan(&workItemID)
		if workItemID == "" {
			return
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = $1`, workItemID); err != nil {
			t.Logf("cleanup: delete comments for %s: %v", ci.CaseID, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE work_item_id = $1`, workItemID); err != nil {
			t.Logf("cleanup: delete chat_conversation for %s: %v", ci.CaseID, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM work_item WHERE id = $1`, workItemID); err != nil {
			t.Logf("cleanup: delete work_item for %s: %v", ci.CaseID, err)
		}
	})
}

// TestCreateWorkItem_PersistsPriorMessagesInOrderWithRoleMapping covers three
// of the feature's requirements at once, since they're all asserted over the
// exact same persisted rows: escalating with a mix of customer and assistant
// messages persists every one of them, in the same chronological order they
// were given (ahead of the triggering ci.Message, which keeps the comment
// table's default now()), with each one's Role correctly mapped to
// comment.created_by (Assistant -> "Novera", Customer -> the customer's own
// email).
func TestCreateWorkItem_PersistsPriorMessagesInOrderWithRoleMapping(t *testing.T) {
	r, pool := newTestRouter(t)
	caseID := testCaseID(t, pool, "workitem-prior-order")
	customerEmail := "workitem-prior-order@example.com"

	base := time.Now().Add(-10 * time.Minute)
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: customerEmail,
		Message: "please help",
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "first customer message", CreatedAt: base.Format(time.RFC3339)},
			{Role: PriorMessageRoleAssistant, Content: "novera reply", CreatedAt: base.Add(1 * time.Minute).Format(time.RFC3339)},
			{Role: PriorMessageRoleCustomer, Content: "second customer message", CreatedAt: base.Add(2 * time.Minute).Format(time.RFC3339)},
		},
	}
	workItemFixture(t, r, pool, ci)

	got, err := commentsForCase(context.Background(), pool, caseID)
	if err != nil {
		t.Fatalf("commentsForCase: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 comments (3 prior messages + 1 triggering message), got %d: %+v", len(got), got)
	}

	wantContents := []string{"first customer message", "novera reply", "second customer message", "please help"}
	wantRoles := []PriorMessageRole{
		PriorMessageRoleCustomer, PriorMessageRoleAssistant, PriorMessageRoleCustomer, PriorMessageRoleCustomer,
	}
	for i, c := range got {
		if c.Content != wantContents[i] {
			t.Errorf("comment[%d].Content = %q, want %q (chronological order broken)", i, c.Content, wantContents[i])
		}
		if c.Role != wantRoles[i] {
			t.Errorf("comment[%d].Role = %s, want %s (role mapping wrong for %q)", i, c.Role, wantRoles[i], c.Content)
		}
	}
}

// TestCreateWorkItem_EmptyPriorMessagesStillWorks confirms an escalation
// with no prior AI-chatbot history at all (nil/empty PriorMessages) is still
// handled cleanly -- this was the universal case before this feature
// existed, and must keep working exactly as before.
func TestCreateWorkItem_EmptyPriorMessagesStillWorks(t *testing.T) {
	r, pool := newTestRouter(t)
	caseID := testCaseID(t, pool, "workitem-empty-prior")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: "workitem-empty-prior@example.com",
		Message: "hello",
		// PriorMessages deliberately left nil.
	}
	workItemFixture(t, r, pool, ci)

	got, err := commentsForCase(context.Background(), pool, caseID)
	if err != nil {
		t.Fatalf("commentsForCase: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("expected exactly the triggering message with no prior messages, got %+v", got)
	}
}

// TestCreateWorkItem_DuplicateCallDoesNotDuplicatePriorMessages is the
// idempotency regression test: a retried escalation (or a duplicate
// CreateWorkItem call for a case that already exists) must be a pure no-op,
// never re-inserting the prior transcript a second time -- see
// CreateWorkItem's own doc comment on why this must never be solved by
// deleting and reinserting.
func TestCreateWorkItem_DuplicateCallDoesNotDuplicatePriorMessages(t *testing.T) {
	r, pool := newTestRouter(t)
	caseID := testCaseID(t, pool, "workitem-dup-prior")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: "workitem-dup-prior@example.com",
		Message: "hello",
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "one"},
			{Role: PriorMessageRoleAssistant, Content: "two"},
		},
	}
	workItemFixture(t, r, pool, ci)

	// Simulate exactly what a retried escalation looks like from
	// csm-portal/backend's HandleEscalate: the same CaseInfo (including the
	// same PriorMessages) submitted again for a case that already exists.
	if err := r.CreateWorkItem(context.Background(), ci); err != nil {
		t.Fatalf("duplicate CreateWorkItem: %v", err)
	}

	got, err := commentsForCase(context.Background(), pool, caseID)
	if err != nil {
		t.Fatalf("commentsForCase: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected exactly 3 comments (2 prior + 1 initial) after a duplicate CreateWorkItem call, got %d: %+v", len(got), got)
	}
}

// TestAddComment_LiveCustomerMessageAppendsAfterPriorHistory covers
// HandleCustomerMessage's own persistence path (AddComment): once a session
// is live, a new customer message must land after the pre-escalation
// transcript, not disturb it.
func TestAddComment_LiveCustomerMessageAppendsAfterPriorHistory(t *testing.T) {
	r, pool := newTestRouter(t)
	caseID := testCaseID(t, pool, "addcomment-customer")
	customerEmail := "addcomment-customer@example.com"
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: customerEmail,
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "prior question"},
			{Role: PriorMessageRoleAssistant, Content: "prior answer"},
		},
	}
	workItemFixture(t, r, pool, ci)

	if err := r.AddComment(context.Background(), caseID, customerEmail, "live customer message"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	detail, err := r.DebugWorkItem(context.Background(), caseID)
	if err != nil {
		t.Fatalf("DebugWorkItem: %v", err)
	}
	if len(detail.Comments) != 3 {
		t.Fatalf("expected 3 comments (2 prior + 1 live), got %d: %+v", len(detail.Comments), detail.Comments)
	}
	last := detail.Comments[len(detail.Comments)-1]
	if last.Content != "live customer message" || last.CreatedBy != customerEmail {
		t.Errorf("expected the live customer message appended last (after the prior history), got %+v", last)
	}
}

// TestAddComment_EngineerMessageAppendsAfterPriorHistory covers
// HandleEngineerMessage's own persistence path (also AddComment): so the DB
// history reads pre-escalation customer/assistant messages, then the live
// customer message, then the engineer's reply -- see this file's own
// package-level intent in the project's chat-persistence-mapping-plan.md.
func TestAddComment_EngineerMessageAppendsAfterPriorHistory(t *testing.T) {
	r, pool := newTestRouter(t)
	caseID := testCaseID(t, pool, "addcomment-engineer")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: "addcomment-engineer-customer@example.com",
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "prior question"},
			{Role: PriorMessageRoleAssistant, Content: "prior answer"},
		},
	}
	workItemFixture(t, r, pool, ci)

	engineerEmail := "addcomment-engineer@example.com"
	if err := r.AddComment(context.Background(), caseID, engineerEmail, "engineer response"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	detail, err := r.DebugWorkItem(context.Background(), caseID)
	if err != nil {
		t.Fatalf("DebugWorkItem: %v", err)
	}
	if len(detail.Comments) != 3 {
		t.Fatalf("expected 3 comments (2 prior + 1 engineer reply), got %d: %+v", len(detail.Comments), detail.Comments)
	}
	last := detail.Comments[len(detail.Comments)-1]
	if last.Content != "engineer response" || last.CreatedBy != engineerEmail {
		t.Errorf("expected the engineer's message appended last with their own identity, got %+v", last)
	}
}

// TestGetCaseInfo_PriorMessagesReflectCommentsTableNotStaleSnapshot ties
// CreateWorkItem, AddComment, and GetCaseInfo together: GetCaseInfo's
// PriorMessages must always reflect chat_routing.comment fresh (via
// commentsForCase), including anything added live after the original
// escalation -- never the case_info JSONB blob's original snapshot.
func TestGetCaseInfo_PriorMessagesReflectCommentsTableNotStaleSnapshot(t *testing.T) {
	r, pool := newTestRouter(t)
	caseID := testCaseID(t, pool, "getcaseinfo-prior")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: "getcaseinfo-prior@example.com",
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "q1"},
			{Role: PriorMessageRoleAssistant, Content: "a1"},
		},
	}
	workItemFixture(t, r, pool, ci)

	if err := r.AddComment(context.Background(), caseID, ci.CustomerEmail, "q2"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	got, err := r.GetCaseInfo(context.Background(), caseID)
	if err != nil {
		t.Fatalf("GetCaseInfo: %v", err)
	}
	if len(got.PriorMessages) != 3 {
		t.Fatalf("expected 3 prior messages (2 original + 1 added live), got %d: %+v", len(got.PriorMessages), got.PriorMessages)
	}
	last := got.PriorMessages[2]
	if last.Content != "q2" || last.Role != PriorMessageRoleCustomer {
		t.Errorf("expected the live-added comment last with role customer, got %+v", last)
	}
}

// TestEscalateAndAccept_StillWorkAfterCreateWorkItemWithPriorMessages is the
// regression check for requirement 8 of this feature ("existing
// escalation/routing behavior remains unchanged"): a case created with a
// non-empty PriorMessages must still flow through the existing
// assign/Accept machinery exactly like any other case. Bypasses Escalate's
// own engineer-selection ranking (shared dev DB, same reasoning as
// state_test.go's assignFixture) by assigning directly.
func TestEscalateAndAccept_StillWorkAfterCreateWorkItemWithPriorMessages(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "prior-escalate")
	caseID := testCaseID(t, pool, "prior-escalate")

	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: "prior-escalate@example.com",
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "hi"},
		},
	}
	workItemFixture(t, r, pool, ci)

	caseInfoJSON, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("marshal case info: %v", err)
	}
	err = r.withTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := insertQueueRow(context.Background(), tx, ci, caseInfoJSON, queueAssigned); err != nil {
			return err
		}
		return assignCaseToEngineer(context.Background(), tx, userID, ci)
	})
	if err != nil {
		t.Fatalf("assign fixture: %v", err)
	}

	result, err := r.Accept(context.Background(), userID, caseID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !result.Applied {
		t.Fatalf("expected Accept to apply for a case created with PriorMessages, got %+v", result)
	}

	row := getConversationRow(t, pool, caseID)
	if row.State != "ACTIVE" || row.AcceptedAt == nil {
		t.Errorf("expected ACTIVE with accepted_at set after Accept, got %+v", row)
	}
}
