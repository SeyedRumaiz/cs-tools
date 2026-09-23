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

// Integration tests for the caseId/conversationId identity split (see
// migration 000021_split_case_and_conversation_identity and this package's
// CaseInfo.ConversationID doc comment). Same conventions as this package's
// other integration tests -- newTestRouter, t.Skip when no database is
// configured, conversationFixture/testCaseID for isolated fixtures.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// identityFixtureCustomer returns a (customerEmail, projectId) pair unique
// to this call, for tests that specifically need to control (not just
// avoid colliding on) those two fields -- unlike conversationFixture's
// default of a caseID-derived email and empty projectId.
func identityFixtureCustomer(tag string) (email, projectID string) {
	now := time.Now().UnixNano()
	return fmt.Sprintf("identity-%s-%d@example.com", tag, now), fmt.Sprintf("proj-%s-%d", tag, now)
}

// TestCreateWorkItem_CaseIDAndConversationIDAreStoredDistinctly is the
// baseline check for the whole identity split: a CaseInfo with different
// CaseID and ConversationID values round-trips through CreateWorkItem and
// DebugWorkItem without either field being silently coerced to match the
// other (the exact assumption this feature replaces -- see backend-v2's
// old HandleEscalate, before this change, which forced CaseID =
// ConversationID at the point of origin).
func TestCreateWorkItem_CaseIDAndConversationIDAreStoredDistinctly(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	caseID := testCaseID(t, pool, "identity-distinct")
	email, projectID := identityFixtureCustomer("distinct")
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, ci); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, caseID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, caseID)
	})

	detail, err := r.DebugWorkItem(ctx, caseID)
	if err != nil {
		t.Fatalf("DebugWorkItem: %v", err)
	}
	if detail.Conversation.CaseID != caseID {
		t.Errorf("expected CaseID %q, got %q", caseID, detail.Conversation.CaseID)
	}
	if detail.Conversation.ConversationID != ci.ConversationID {
		t.Errorf("expected ConversationID %q, got %q", ci.ConversationID, detail.Conversation.ConversationID)
	}
	if detail.Conversation.CaseID == detail.Conversation.ConversationID {
		t.Fatalf("CaseID and ConversationID must be distinct for this fixture, got both %q", detail.Conversation.CaseID)
	}
}

// TestCreateWorkItem_RejectsSecondOpenChatForSameCustomerAndProject is the
// application-level half of the duplicate-open-chat guard (see
// ErrDuplicateOpenChat's own doc comment): a brand-new caseId for a
// customerEmail+projectId that already has a non-ended chat_conversation
// row must be rejected, not silently create a second, competing
// escalation.
func TestCreateWorkItem_RejectsSecondOpenChatForSameCustomerAndProject(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	email, projectID := identityFixtureCustomer("dup")
	firstCaseID := testCaseID(t, pool, "identity-dup-first")
	first := CaseInfo{
		CaseID: firstCaseID, ConversationID: "conv-" + firstCaseID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, first); err != nil {
		t.Fatalf("CreateWorkItem(first): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, firstCaseID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, firstCaseID)
	})

	secondCaseID := testCaseID(t, pool, "identity-dup-second")
	second := CaseInfo{
		CaseID: secondCaseID, ConversationID: "conv-" + secondCaseID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	err := r.CreateWorkItem(ctx, second)
	if err == nil {
		t.Fatal("expected CreateWorkItem to reject a second open chat for the same customer+project, got nil error")
	}
	if !errors.Is(err, ErrDuplicateOpenChat) {
		t.Fatalf("expected errors.Is(err, ErrDuplicateOpenChat), got: %v", err)
	}

	// No work_item/chat_conversation row must exist for the rejected
	// second caseId -- the whole transaction must have rolled back.
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_conversation WHERE case_id = $1`, secondCaseID).Scan(&count); err != nil {
		t.Fatalf("count chat_conversation rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the rejected second escalation to create no chat_conversation row, found %d", count)
	}
}

// TestCreateWorkItem_AllowsNewChatAfterPreviousOneEnded makes sure the
// duplicate-open-chat guard is scoped to WHERE session_ended_at IS NULL:
// once a customer's previous live chat for a project has ended, a brand
// new caseId for the same customer+project must succeed, not be treated
// as a duplicate.
func TestCreateWorkItem_AllowsNewChatAfterPreviousOneEnded(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	email, projectID := identityFixtureCustomer("reescalate")
	firstCaseID := testCaseID(t, pool, "identity-reescalate-first")
	first := CaseInfo{
		CaseID: firstCaseID, ConversationID: "conv-shared-" + firstCaseID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, first); err != nil {
		t.Fatalf("CreateWorkItem(first): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, firstCaseID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, firstCaseID)
	})

	// End the first chat, exactly like Completed would.
	if _, err := pool.Exec(ctx, `UPDATE chat_conversation SET session_ended_at = now() WHERE case_id = $1`, firstCaseID); err != nil {
		t.Fatalf("end first chat: %v", err)
	}

	// Same conversationId (this is the SAME Novera conversation continuing
	// after the customer talks to the AI again), but a brand-new caseId --
	// exactly scenario 8 from the identity-split design (re-escalation
	// after end must succeed with a genuinely different caseId, same
	// stable conversationId).
	secondCaseID := testCaseID(t, pool, "identity-reescalate-second")
	second := CaseInfo{
		CaseID: secondCaseID, ConversationID: "conv-shared-" + firstCaseID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, second); err != nil {
		t.Fatalf("expected CreateWorkItem to allow a new escalation after the previous one ended, got: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, secondCaseID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, secondCaseID)
	})

	if firstCaseID == secondCaseID {
		t.Fatal("test fixture bug: expected distinct case IDs")
	}
	detail, err := r.DebugWorkItem(ctx, secondCaseID)
	if err != nil {
		t.Fatalf("DebugWorkItem(second): %v", err)
	}
	if detail.Conversation.ConversationID != first.ConversationID {
		t.Errorf("expected the second case to keep the same conversationId %q, got %q", first.ConversationID, detail.Conversation.ConversationID)
	}
}

// TestEscalate_RejectsAlreadyEndedCase covers Escalate's own guard (queue
// safety, item 5 of the identity-split design): Escalate must reject an
// already-ended caseId with ErrCaseAlreadyEnded rather than creating a
// zombie chat_queue row nothing will ever accept.
func TestEscalate_RejectsAlreadyEndedCase(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	caseID := testCaseID(t, pool, "identity-ended-escalate")
	ci := conversationFixture(t, r, pool, caseID)

	if _, err := pool.Exec(ctx, `UPDATE chat_conversation SET session_ended_at = now() WHERE case_id = $1`, caseID); err != nil {
		t.Fatalf("end conversation: %v", err)
	}

	_, err := r.Escalate(ctx, ci)
	if err == nil {
		t.Fatal("expected Escalate to reject an already-ended case, got nil error")
	}
	if !errors.Is(err, ErrCaseAlreadyEnded) {
		t.Fatalf("expected errors.Is(err, ErrCaseAlreadyEnded), got: %v", err)
	}

	// No zombie chat_queue row -- the whole transaction must have rolled
	// back before ever reaching insertQueueRow.
	var queueRows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_queue WHERE chat_conversation_id = $1`, caseID).Scan(&queueRows); err != nil {
		t.Fatalf("count chat_queue rows: %v", err)
	}
	if queueRows != 0 {
		t.Fatalf("expected no chat_queue row for an already-ended case, found %d", queueRows)
	}
}

// TestReEscalation_SameConversationIDDifferentCaseIDBothAccept exercises
// the full "MUST work" scenario from the identity-split design end to end:
// escalate (caseId X) -> accept -> end; then, still the same Novera
// conversation, escalate again (caseId Y) -> accept. X and Y must differ;
// the conversationId must not.
func TestReEscalation_SameConversationIDDifferentCaseIDBothAccept(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	userID := testUserID(t, r, pool, "identity-reescalate-engineer")
	if _, err := r.SetPresence(ctx, userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}

	email, projectID := identityFixtureCustomer("full-cycle")
	sharedConversationID := "conv-shared-" + testCaseID(t, pool, "identity-full-cycle-conv")

	// First escalation: caseId X.
	caseX := testCaseID(t, pool, "identity-full-cycle-x")
	ciX := CaseInfo{
		CaseID: caseX, ConversationID: sharedConversationID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, ciX); err != nil {
		t.Fatalf("CreateWorkItem(X): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, caseX)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, caseX)
	})
	escX, err := r.Escalate(ctx, ciX)
	if err != nil {
		t.Fatalf("Escalate(X): %v", err)
	}
	if escX.Queued {
		t.Fatalf("expected X to route directly to the idle AVAILABLE engineer, got queued: %+v", escX)
	}
	if _, err := r.Accept(ctx, userID, caseX); err != nil {
		t.Fatalf("Accept(X): %v", err)
	}
	if _, err := r.Completed(ctx, userID, caseX); err != nil {
		t.Fatalf("Completed(X): %v", err)
	}

	// Second escalation, same Novera conversation, must get a genuinely
	// different caseId (Y) -- minted by the caller in production
	// (backend-v2's HandleEscalate), simulated here by simply using a
	// second, distinct test case ID.
	caseY := testCaseID(t, pool, "identity-full-cycle-y")
	if caseY == caseX {
		t.Fatal("test fixture bug: expected distinct case IDs")
	}
	ciY := CaseInfo{
		CaseID: caseY, ConversationID: sharedConversationID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, ciY); err != nil {
		t.Fatalf("expected CreateWorkItem(Y) to succeed now that X has ended, got: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, caseY)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, caseY)
	})
	escY, err := r.Escalate(ctx, ciY)
	if err != nil {
		t.Fatalf("Escalate(Y): %v", err)
	}
	if escY.Queued {
		t.Fatalf("expected Y to route directly (the engineer freed capacity when X completed), got queued: %+v", escY)
	}
	if _, err := r.Accept(ctx, userID, caseY); err != nil {
		t.Fatalf("Accept(Y): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `UPDATE chat_conversation SET session_ended_at = now() WHERE case_id = $1`, caseY)
	})

	rowX := getConversationRow(t, pool, caseX)
	rowY := getConversationRow(t, pool, caseY)
	if rowX.SessionEndedAt == nil {
		t.Errorf("expected X's session to be ended, got %+v", rowX)
	}
	if rowY.State != "ACTIVE" {
		t.Errorf("expected Y to be ACTIVE after Accept, got state %q", rowY.State)
	}

	detailX, err := r.DebugWorkItem(ctx, caseX)
	if err != nil {
		t.Fatalf("DebugWorkItem(X): %v", err)
	}
	detailY, err := r.DebugWorkItem(ctx, caseY)
	if err != nil {
		t.Fatalf("DebugWorkItem(Y): %v", err)
	}
	if detailX.Conversation.ConversationID != sharedConversationID || detailY.Conversation.ConversationID != sharedConversationID {
		t.Fatalf("expected both cases to share conversationId %q, got X=%q Y=%q", sharedConversationID, detailX.Conversation.ConversationID, detailY.Conversation.ConversationID)
	}
}

// TestEscalate_ConcurrentDuplicateEscalationIsRejected simulates scenario 9
// from the identity-split design: two escalation attempts for the same
// customer+project racing CreateWorkItem at once. The partial unique index
// (uq_chat_conversation_open_customer_project) must be the backstop that
// catches whichever request loses the race even if it somehow got past the
// plain SELECT check -- exercised here directly by calling CreateWorkItem
// twice with pre-existing rows already committed (the same end state a
// race converges to), asserting the loser gets ErrDuplicateOpenChat and
// never a raw, untranslated unique-violation error.
func TestEscalate_ConcurrentDuplicateEscalationIsRejected(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	email, projectID := identityFixtureCustomer("race")
	winnerID := testCaseID(t, pool, "identity-race-winner")
	winner := CaseInfo{
		CaseID: winnerID, ConversationID: "conv-" + winnerID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	if err := r.CreateWorkItem(ctx, winner); err != nil {
		t.Fatalf("CreateWorkItem(winner): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, winnerID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, winnerID)
	})

	loserID := testCaseID(t, pool, "identity-race-loser")
	loser := CaseInfo{
		CaseID: loserID, ConversationID: "conv-" + loserID,
		Subject: "test", CustomerEmail: email, ProjectID: projectID,
	}
	err := r.CreateWorkItem(ctx, loser)
	if err == nil {
		t.Fatal("expected the loser's CreateWorkItem to be rejected, got nil error")
	}
	if !errors.Is(err, ErrDuplicateOpenChat) {
		t.Fatalf("expected errors.Is(err, ErrDuplicateOpenChat) -- a raw unique-violation must be translated, not leaked -- got: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_conversation WHERE case_id = $1`, loserID).Scan(&count); err != nil {
		t.Fatalf("count chat_conversation rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no chat_conversation row for the rejected loser, found %d", count)
	}

	// Sanity: the pool connection used for the losing call must still be
	// usable afterwards -- a failed transaction that wasn't cleanly rolled
	// back would otherwise poison the pool for every later test.
	var pingResult int
	if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&pingResult); err != nil {
		t.Fatalf("pool unusable after rejected CreateWorkItem: %v", err)
	}
}

// TestGetPresence_CasesCarryDistinctCaseAndConversationIDs makes sure the
// read path an engineer's own UI rehydrates from (GetPresence, used by
// csm-portal's chat handlers) reports CaseID and ConversationID as the two
// distinct values they were escalated with, not silently collapsed to one.
func TestGetPresence_CasesCarryDistinctCaseAndConversationIDs(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	userID := testUserID(t, r, pool, "identity-presence-engineer")
	caseID := testCaseID(t, pool, "identity-presence-case")
	assigned := assignFixture(t, r, pool, userID, caseID)
	if assigned.CaseID == assigned.ConversationID {
		t.Fatal("test fixture bug: expected distinct case/conversation IDs")
	}

	detail, err := r.GetPresence(ctx, userID)
	if err != nil {
		t.Fatalf("GetPresence: %v", err)
	}
	var found *CaseStatus
	for i := range detail.Cases {
		if detail.Cases[i].CaseID == caseID {
			found = &detail.Cases[i]
		}
	}
	if found == nil {
		t.Fatalf("expected case %s in GetPresence's Cases, got %+v", caseID, detail.Cases)
	}
	if found.ConversationID != assigned.ConversationID {
		t.Errorf("expected ConversationID %q, got %q", assigned.ConversationID, found.ConversationID)
	}
}

// Ensures json.Marshal/Unmarshal round-trips both ID fields independently
// -- a pure unit test (no database) guarding the wire shape every service
// boundary in this feature depends on.
func TestCaseInfo_JSONRoundTripKeepsBothIDsDistinct(t *testing.T) {
	ci := CaseInfo{CaseID: "case-1", ConversationID: "conv-1", ProjectID: "proj-1"}
	raw, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded CaseInfo
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.CaseID != "case-1" || decoded.ConversationID != "conv-1" {
		t.Fatalf("expected CaseID=case-1 ConversationID=conv-1, got %+v", decoded)
	}
}

// TestCreateWorkItem_MissingCustomerEmailOrProjectIDSkipsAppLevelCheck
// documents current behavior at the application-level check: a request
// missing either field is let through the SELECT-based check in
// CreateWorkItem (nothing meaningful to compare), relying on the partial
// unique index's COALESCE-to-” behavior as the real backstop for that
// case -- see the migration's own doc comment. This is not a scenario a
// real caller should ever produce (csm-portal/backend's HandleEscalate
// requires CustomerEmail before calling CreateWorkItem at all), so this
// test only pins today's documented behavior, not a requirement.
func TestCreateWorkItem_MissingProjectIDSkipsAppLevelCheck(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	email := fmt.Sprintf("identity-missing-project-%d@example.com", time.Now().UnixNano())
	firstID := testCaseID(t, pool, "identity-missing-project-first")
	first := CaseInfo{CaseID: firstID, ConversationID: "conv-" + firstID, Subject: "test", CustomerEmail: email}
	if err := r.CreateWorkItem(ctx, first); err != nil {
		t.Fatalf("CreateWorkItem(first): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = (SELECT work_item_id FROM chat_conversation WHERE case_id = $1)`, firstID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE case_id = $1`, firstID)
	})

	secondID := testCaseID(t, pool, "identity-missing-project-second")
	second := CaseInfo{CaseID: secondID, ConversationID: "conv-" + secondID, Subject: "test", CustomerEmail: email}
	err := r.CreateWorkItem(ctx, second)
	// Both have ProjectID == "", so the database-level partial unique
	// index (COALESCE(...,'')) still catches this pair even though the
	// application-level check above skipped it -- see this test's own doc
	// comment.
	if err == nil {
		t.Fatal("expected the database-level unique index to reject this pair even without ProjectID, got nil error")
	}
	if !errors.Is(err, ErrDuplicateOpenChat) {
		t.Fatalf("expected errors.Is(err, ErrDuplicateOpenChat) from the index backstop, got: %v", err)
	}
}
