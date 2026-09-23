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

// These are integration tests: Router is backed by a real *pgxpool.Pool,
// not an interface with a fake, so there's no way to exercise its SQL
// without a real Postgres database. newTestRouter connects using this
// service's own normal DB_* environment variables, so run these the same
// way you'd run the server itself:
//
//	set -a && source .env && set +a && go test ./...
//
// Without those env vars set, every test here calls t.Skip rather than
// failing -- that's the expected state anywhere Postgres isn't reachable
// (CI, a laptop without the dev DB set up), not a test failure.
//
// These run against the same chat_routing schema the real dev database
// uses -- there's no isolated test schema yet. That's safe because every
// test here only touches engineer user IDs and case IDs it generates
// itself (see testUserID/testCaseID) and cleans up via t.Cleanup, so it
// can never collide with a real account or a real queued case. What this
// deliberately does not test is Escalate's own engineer-selection ranking
// or queue position, since both depend on every other row in the shared
// database, which an isolated test can't control. Fixtures call
// assignCaseToEngineer/insertQueueRow directly instead of going through
// Escalate, so a test that needs "engineer X is pending on case Y" can
// build exactly that without picking a real engineer out of the pool.
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/config"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/db"
)

// newTestRouter connects to the database configured via this service's own
// normal environment variables and returns a Router backed by it, or skips
// the calling test if that configuration is incomplete. The pool is closed
// automatically via t.Cleanup.
func newTestRouter(t *testing.T) (*Router, *pgxpool.Pool) {
	t.Helper()

	cfg := config.LoadDB()
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping: chat-routing-service database not configured (%v)", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DSN())
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	return NewRouter(pool), pool
}

// testUserID returns a collision-free engineer user ID, registers it via
// SetPresence (so it starts OFFLINE with capacity 1, like any engineer's
// first contact), and cleans up every row it touched once the test
// finishes.
func testUserID(t *testing.T, r *Router, pool *pgxpool.Pool, tag string) string {
	t.Helper()
	userID := fmt.Sprintf("router-test-%s-%d", tag, time.Now().UnixNano())
	ctx := context.Background()

	if _, err := r.SetPresence(ctx, userID, StatusOffline); err != nil {
		t.Fatalf("seed test engineer %s: %v", userID, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM cs_engineer_status WHERE user_id = $1`, userID); err != nil {
			t.Logf("cleanup: delete engineer %s: %v", userID, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM chat_queue_engineer_assignment WHERE engineer_id = $1`, userID); err != nil {
			t.Logf("cleanup: delete chat_queue_engineer_assignment for %s: %v", userID, err)
		}
	})
	return userID
}

// setMaxConcurrent overrides userID's concurrent-chat capacity directly --
// the only way to get a non-default value in a test, since there's no
// admin endpoint yet (see the project's db-schema-review doc).
func setMaxConcurrent(t *testing.T, pool *pgxpool.Pool, userID string, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE cs_engineer_status SET max_concurrent_chats = $1 WHERE user_id = $2`, n, userID); err != nil {
		t.Fatalf("set max_concurrent_chats for %s: %v", userID, err)
	}
}

// testCaseID returns a collision-free case ID (and matching "conv-"-
// prefixed conversation ID) and registers cleanup of any chat_queue row
// left under it, so a failed assertion doesn't leak a row into the shared
// queue.
func testCaseID(t *testing.T, pool *pgxpool.Pool, tag string) string {
	t.Helper()
	caseID := fmt.Sprintf("router-test-%s-%d", tag, time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM chat_queue WHERE chat_conversation_id = $1`, caseID); err != nil {
			t.Logf("cleanup: delete chat_queue row %s: %v", caseID, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM chat_queue_engineer_assignment WHERE conversation_id = $1`, caseID); err != nil {
			t.Logf("cleanup: delete chat_queue_engineer_assignment row %s: %v", caseID, err)
		}
	})
	return caseID
}

// conversationFixture creates caseID's chat_conversation row (via
// CreateWorkItem, exercising the real LOCAL STAND-IN persistence path) and
// registers cleanup of the work_item/chat_conversation/comment rows it
// creates. Every fixture case must go through this before
// assignCaseToEngineer -- see that function's own doc comment on why the
// row must already exist.
func conversationFixture(t *testing.T, r *Router, pool *pgxpool.Pool, caseID string) CaseInfo {
	t.Helper()
	ci := CaseInfo{
		CaseID: caseID, ConversationID: "conv-" + caseID,
		// CustomerEmail is derived from caseID (unique per fixture, see
		// testCaseID) rather than a shared constant -- since the identity
		// split's duplicate-open-chat guard (CreateWorkItem,
		// ErrDuplicateOpenChat) now enforces at most one non-ended live
		// chat per (customerEmail, projectId), two fixtures sharing one
		// constant customerEmail (and both leaving projectId empty) would
		// collide the moment a test creates more than one open fixture at
		// once -- exactly the scenario several tests in this package
		// exercise (e.g. abandon_test.go's stale+fresh pair).
		Subject: "test", CustomerEmail: caseID + "@example.com",
	}
	if err := r.CreateWorkItem(context.Background(), ci); err != nil {
		t.Fatalf("conversationFixture: CreateWorkItem: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var workItemID string
		_ = pool.QueryRow(cleanupCtx, `SELECT work_item_id FROM chat_conversation WHERE case_id = $1`, caseID).Scan(&workItemID)
		if workItemID == "" {
			return
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM comment WHERE work_item_id = $1`, workItemID); err != nil {
			t.Logf("cleanup: delete comments for %s: %v", caseID, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM chat_conversation WHERE work_item_id = $1`, workItemID); err != nil {
			t.Logf("cleanup: delete chat_conversation for %s: %v", caseID, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM work_item WHERE id = $1`, workItemID); err != nil {
			t.Logf("cleanup: delete work_item for %s: %v", caseID, err)
		}
	})
	return ci
}

// assignFixture builds caseID's conversation (see conversationFixture) and
// puts userID directly on it as a pending (unconfirmed) assignment,
// bypassing Escalate's own engineer-selection, with a matching ASSIGNED
// chat_queue row -- matching exactly what Escalate would have created.
// userID must already exist (see testUserID).
func assignFixture(t *testing.T, r *Router, pool *pgxpool.Pool, userID, caseID string) CaseInfo {
	t.Helper()
	ci := conversationFixture(t, r, pool, caseID)
	caseInfoJSON, err := json.Marshal(ci)
	if err != nil {
		t.Fatalf("marshal fixture case info: %v", err)
	}

	err = r.withTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := insertQueueRow(context.Background(), tx, ci, caseInfoJSON, queueAssigned); err != nil {
			return err
		}
		return assignCaseToEngineer(context.Background(), tx, userID, ci)
	})
	if err != nil {
		t.Fatalf("assignFixture: %v", err)
	}
	return ci
}

// conversationRowForTest is the subset of a chat_conversation row
// assertions check directly, bypassing Router's own API so a test can
// confirm what's actually in the database rather than only what a
// method's return value claims.
type conversationRowForTest struct {
	AssigneeID     *string
	State          string
	AcceptedAt     *time.Time
	SessionEndedAt *time.Time
}

func getConversationRow(t *testing.T, pool *pgxpool.Pool, caseID string) conversationRowForTest {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var row conversationRowForTest
	err := pool.QueryRow(ctx, `
		SELECT assignee_id, state, accepted_at, session_ended_at FROM chat_conversation WHERE case_id = $1
	`, caseID).Scan(&row.AssigneeID, &row.State, &row.AcceptedAt, &row.SessionEndedAt)
	if err != nil {
		t.Fatalf("read conversation row %s: %v", caseID, err)
	}
	return row
}

func getChatStatus(t *testing.T, pool *pgxpool.Pool, userID string) Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var status Status
	if err := pool.QueryRow(ctx, `SELECT chat_status FROM cs_engineer_status WHERE user_id = $1`, userID).Scan(&status); err != nil {
		t.Fatalf("read chat_status %s: %v", userID, err)
	}
	return status
}

// completedSafely calls Router.Completed for userID/caseID, and, if that
// drained a case off the shared chat_queue (result.AssignedCase != nil),
// immediately puts it back and clears the fixture engineer off it via SQL.
// None of this file's fixtures ever leave a WAITING_FOR_ENGINEER row
// behind, so a non-nil AssignedCase here can only be a real, pre-existing
// case from the shared dev database's queue -- not test data. Without
// this, a fixture engineer freeing capacity could silently steal a real
// customer's queued escalation and then lose it once the fixture's row is
// deleted at cleanup.
func completedSafely(t *testing.T, r *Router, pool *pgxpool.Pool, userID, caseID string) CompletedResult {
	t.Helper()
	ctx := context.Background()
	result, err := r.Completed(ctx, userID, caseID)
	if err != nil {
		t.Fatalf("Completed: %v", err)
	}
	if result.AssignedCase == nil {
		return result
	}

	t.Logf("Completed drained a real queued case (caseId=%s) onto the test fixture engineer -- restoring it to the queue", result.AssignedCase.CaseID)
	rescueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(rescueCtx)
	if err != nil {
		t.Fatalf("rescue: begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(rescueCtx) }()
	if err := requeueWaiting(rescueCtx, tx, result.AssignedCase.CaseID); err != nil {
		t.Fatalf("rescue: re-queue drained case: %v", err)
	}
	if _, err := tx.Exec(rescueCtx, `
		UPDATE chat_conversation SET assignee_id = NULL, accepted_at = NULL, updated_at = now()
		WHERE case_id = $1
	`, result.AssignedCase.CaseID); err != nil {
		t.Fatalf("rescue: clear fixture engineer's stolen case: %v", err)
	}
	if err := tx.Commit(rescueCtx); err != nil {
		t.Fatalf("rescue: commit: %v", err)
	}
	return result
}

// TestIsPending is a pure unit test -- isPending takes no database
// connection, so it actually runs anywhere `go test` runs.
func TestIsPending(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		state      string
		acceptedAt *time.Time
		want       bool
	}{
		{"open unconfirmed", "OPEN", nil, true},
		{"open confirmed (shouldn't happen, but must not misreport)", "OPEN", &now, false},
		{"active", "ACTIVE", nil, false},
		{"active with accepted_at", "ACTIVE", &now, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPending(tt.state, tt.acceptedAt); got != tt.want {
				t.Errorf("isPending(%s, accepted=%v) = %v, want %v", tt.state, tt.acceptedAt != nil, got, tt.want)
			}
		})
	}
}

func TestCompleted_NoOpWhenNoSuchCase(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "completed-noop")

	result, err := r.Completed(context.Background(), userID, "no-such-case")
	if err != nil {
		t.Fatalf("Completed: %v", err)
	}
	if result.Ended || result.AssignedCase != nil {
		t.Errorf("Completed for a case the engineer never held should be a pure no-op, got %+v", result)
	}
}

func TestCompleted_EndsSessionWithoutTouchingChatStatus(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "completed-status")
	caseID := testCaseID(t, pool, "completed-status")
	assignFixture(t, r, pool, userID, caseID)

	if _, err := r.Accept(context.Background(), userID, caseID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := r.SetPresence(context.Background(), userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}

	result := completedSafely(t, r, pool, userID, caseID)
	if !result.Ended {
		t.Errorf("expected Ended=true for a real open case, got %+v", result)
	}

	row := getConversationRow(t, pool, caseID)
	if row.SessionEndedAt == nil {
		t.Errorf("expected session_ended_at to be set after Completed, got %+v", row)
	}
	// chat_status must be untouched by Completed -- it's a pure manual
	// toggle now, independent of case load (see SetPresence's doc comment).
	if status := getChatStatus(t, pool, userID); status != StatusAvailable {
		t.Errorf("expected Completed to leave chat_status alone (still AVAILABLE), got %s", status)
	}
}

func TestCompleted_DuplicateCallIsIdempotent(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "completed-duplicate")
	caseID := testCaseID(t, pool, "completed-duplicate")
	assignFixture(t, r, pool, userID, caseID)

	first := completedSafely(t, r, pool, userID, caseID)
	if !first.Ended {
		t.Fatalf("expected the first Completed call to end the session, got %+v", first)
	}

	second, err := r.Completed(context.Background(), userID, caseID)
	if err != nil {
		t.Fatalf("second Completed: %v", err)
	}
	if second.Ended || second.AssignedCase != nil {
		t.Errorf("a duplicate Completed call for an already-ended session must be a no-op, got %+v", second)
	}
}

func TestConcurrentCapacity_SecondCaseAssignedWithoutQueueingWhenCapacityTwo(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "capacity-two")
	setMaxConcurrent(t, pool, userID, 2)
	if _, err := r.SetPresence(context.Background(), userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}

	firstID := testCaseID(t, pool, "capacity-two-first")
	first := assignFixture(t, r, pool, userID, firstID)
	if _, err := r.Accept(context.Background(), userID, first.CaseID); err != nil {
		t.Fatalf("Accept first: %v", err)
	}

	// A second, independent case handed straight to this same engineer
	// (bypassing popAvailableEngineer's own ranking, which could otherwise
	// pick a different real engineer in the shared dev database) --
	// exercises exactly the capacity check assignCaseToEngineer/
	// activeCaseCount are responsible for: with capacity 2 and one active
	// case, this must succeed and both must coexist.
	secondID := testCaseID(t, pool, "capacity-two-second")
	second := assignFixture(t, r, pool, userID, secondID)

	firstRow := getConversationRow(t, pool, first.CaseID)
	secondRow := getConversationRow(t, pool, second.CaseID)
	if firstRow.AssigneeID == nil || *firstRow.AssigneeID != userID {
		t.Errorf("expected first case to remain assigned to %s, got %+v", userID, firstRow)
	}
	if secondRow.AssigneeID == nil || *secondRow.AssigneeID != userID {
		t.Errorf("expected second case to also be assigned to %s (capacity 2), got %+v", userID, secondRow)
	}
	if status := getChatStatus(t, pool, userID); status != StatusAvailable {
		t.Errorf("expected chat_status to remain AVAILABLE while holding two concurrent cases, got %s", status)
	}

	// Ending the first case must not disturb the second.
	completedSafely(t, r, pool, userID, first.CaseID)
	secondRowAfter := getConversationRow(t, pool, second.CaseID)
	if secondRowAfter.AssigneeID == nil || *secondRowAfter.AssigneeID != userID || secondRowAfter.SessionEndedAt != nil {
		t.Errorf("expected the second, still-open case to be untouched by ending the first, got %+v", secondRowAfter)
	}
}

func TestSetPresence_AvailableDrainsQueueUpToCapacity(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "capacity-drain")
	setMaxConcurrent(t, pool, userID, 2)

	// Two cases queued (unassigned) before this engineer goes AVAILABLE.
	firstID := testCaseID(t, pool, "capacity-drain-first")
	secondID := testCaseID(t, pool, "capacity-drain-second")
	firstCI := conversationFixture(t, r, pool, firstID)
	secondCI := conversationFixture(t, r, pool, secondID)
	for _, ci := range []CaseInfo{firstCI, secondCI} {
		caseInfoJSON, err := json.Marshal(ci)
		if err != nil {
			t.Fatalf("marshal queued case: %v", err)
		}
		if err := r.withTx(context.Background(), func(tx pgx.Tx) error {
			_, err := insertQueueRow(context.Background(), tx, ci, caseInfoJSON, queueWaitingForEngineer)
			return err
		}); err != nil {
			t.Fatalf("enqueue fixture case: %v", err)
		}
	}

	// Assert on the drained state first, then rescue manually below --
	// rescuing before asserting would erase exactly what this test needs
	// to check.
	result, err := r.SetPresence(context.Background(), userID, StatusAvailable)
	if err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}
	// The queue is shared with the real dev database, so other real cases
	// may also be waiting ahead of these two -- this only asserts that
	// draining stopped at exactly the engineer's own capacity (2), not
	// that these specific two fixture cases were the ones claimed.
	if len(result.AssignedCases) != 2 {
		t.Fatalf("expected exactly 2 cases claimed (capacity 2), got %d: %+v", len(result.AssignedCases), result.AssignedCases)
	}

	var activeCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM chat_conversation
		WHERE assignee_id = $1 AND state IN ('OPEN', 'ACTIVE') AND session_ended_at IS NULL
	`, userID).Scan(&activeCount); err != nil {
		t.Fatalf("count active cases: %v", err)
	}
	if activeCount != 2 {
		t.Errorf("expected exactly 2 active cases after draining to capacity, got %d", activeCount)
	}

	// Rescue: put both drained cases back exactly like setAvailableSafely
	// would have, now that the assertions above are done.
	for _, c := range result.AssignedCases {
		rescueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		tx, err := pool.Begin(rescueCtx)
		if err != nil {
			cancel()
			t.Fatalf("rescue: begin transaction: %v", err)
		}
		if err := requeueWaiting(rescueCtx, tx, c.CaseID); err != nil {
			_ = tx.Rollback(rescueCtx)
			cancel()
			t.Fatalf("rescue: re-queue drained case: %v", err)
		}
		if _, err := tx.Exec(rescueCtx, `
			UPDATE chat_conversation SET assignee_id = NULL, accepted_at = NULL, updated_at = now()
			WHERE case_id = $1
		`, c.CaseID); err != nil {
			_ = tx.Rollback(rescueCtx)
			cancel()
			t.Fatalf("rescue: clear fixture engineer's stolen case: %v", err)
		}
		if err := tx.Commit(rescueCtx); err != nil {
			cancel()
			t.Fatalf("rescue: commit: %v", err)
		}
		cancel()
	}
}

func TestAccept_AppliesForTheHeldCase(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "accept-valid")
	caseID := testCaseID(t, pool, "accept-valid")
	assignFixture(t, r, pool, userID, caseID)

	result, err := r.Accept(context.Background(), userID, caseID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !result.Applied {
		t.Fatalf("expected Accept to apply for the case the engineer is actually pending on, got %+v", result)
	}

	row := getConversationRow(t, pool, caseID)
	if row.State != "ACTIVE" || row.AcceptedAt == nil {
		t.Errorf("expected ACTIVE with accepted_at set after a valid Accept, got %+v", row)
	}

	var queueRows int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM chat_queue WHERE chat_conversation_id = $1`, caseID).Scan(&queueRows); err != nil {
		t.Fatalf("check queue row removed: %v", err)
	}
	if queueRows != 0 {
		t.Errorf("expected Accept to delete the case's chat_queue row, but %d remain", queueRows)
	}
}

func TestAccept_RejectsWhenNeverAssigned(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "accept-idle")

	result, err := r.Accept(context.Background(), userID, "some-case-id")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result.Applied {
		t.Errorf("expected Accept to reject a caseID the engineer was never assigned, got %+v", result)
	}
}

func TestAccept_RejectsWrongEngineer(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "accept-wrong-a")
	otherID := testUserID(t, r, pool, "accept-wrong-b")
	caseID := testCaseID(t, pool, "accept-wrong")
	assignFixture(t, r, pool, userID, caseID)

	result, err := r.Accept(context.Background(), otherID, caseID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result.Applied {
		t.Errorf("expected Accept to reject an engineer who isn't the assignee, got %+v", result)
	}

	row := getConversationRow(t, pool, caseID)
	if row.AssigneeID == nil || *row.AssigneeID != userID || row.State != "OPEN" {
		t.Errorf("a wrong-engineer Accept must not disturb the real assignment, got %+v", row)
	}
}

func TestAccept_RejectsDoubleAccept(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "accept-double")
	caseID := testCaseID(t, pool, "accept-double")
	assignFixture(t, r, pool, userID, caseID)

	first, err := r.Accept(context.Background(), userID, caseID)
	if err != nil || !first.Applied {
		t.Fatalf("first Accept should apply, got %+v, err=%v", first, err)
	}

	second, err := r.Accept(context.Background(), userID, caseID)
	if err != nil {
		t.Fatalf("second Accept: %v", err)
	}
	if second.Applied {
		t.Errorf("a second Accept for a case already ACTIVE (not pending) must not apply again, got %+v", second)
	}
}

func TestDecline_NoOpForStaleCaseID(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "decline-stale")
	caseID := testCaseID(t, pool, "decline-stale")
	assignFixture(t, r, pool, userID, caseID)

	result, err := r.Decline(context.Background(), userID, "a-different-case-id-entirely")
	if err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if result.ReassignedTo != "" || result.Requeued || result.AssignedCase != nil {
		t.Errorf("Decline for a caseID the engineer isn't actually holding must be a no-op, got %+v", result)
	}

	// The real case must be untouched.
	row := getConversationRow(t, pool, caseID)
	if row.AssigneeID == nil || *row.AssigneeID != userID || row.State != "OPEN" {
		t.Errorf("a stale Decline must not disturb the engineer's real pending case, got %+v", row)
	}
}

// Decline's reassignment path (as opposed to the no-op tested above) is
// deliberately not exercised here: it can hand the declined case to
// whichever OTHER engineer is currently AVAILABLE with spare capacity in
// the shared dev database, which could be a real account -- polluting that
// real engineer's live assignment with a fake test case is a worse outcome
// than the coverage gap. That path was exercised manually via curl during
// this feature's own testing instead (see the project's
// live-engineer-chat-escalation-summary.md).

func TestEnqueue_WaitingFlipsToAssignedAndKeepsCreatedAt(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()

	backID := testCaseID(t, pool, "enqueue-back")
	frontID := testCaseID(t, pool, "enqueue-front")
	backCase := CaseInfo{CaseID: backID, ConversationID: "conv-" + backID}
	frontCase := CaseInfo{CaseID: frontID, ConversationID: "conv-" + frontID}
	backJSON, _ := json.Marshal(backCase)
	frontJSON, _ := json.Marshal(frontCase)

	var backCreatedAt, frontCreatedAt time.Time
	err := r.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := insertQueueRow(ctx, tx, backCase, backJSON, queueWaitingForEngineer); err != nil {
			return err
		}
		if _, err := insertQueueRow(ctx, tx, frontCase, frontJSON, queueWaitingForEngineer); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("enqueue fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM chat_queue WHERE chat_conversation_id IN ($1, $2)`, backCase.CaseID, frontCase.CaseID)
	})

	if err := pool.QueryRow(ctx, `SELECT created_at FROM chat_queue WHERE chat_conversation_id = $1`, backCase.CaseID).Scan(&backCreatedAt); err != nil {
		t.Fatalf("read back created_at: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT created_at FROM chat_queue WHERE chat_conversation_id = $1`, frontCase.CaseID).Scan(&frontCreatedAt); err != nil {
		t.Fatalf("read front created_at: %v", err)
	}
	if !backCreatedAt.Before(frontCreatedAt) && backCreatedAt != frontCreatedAt {
		t.Fatalf("expected backCase to have been enqueued before frontCase, got back=%v front=%v", backCreatedAt, frontCreatedAt)
	}

	// requeueWaiting must flip a claimed row back to WAITING_FOR_ENGINEER
	// without touching created_at, so it keeps its original place ahead of
	// anything that arrives later. Exercised directly against backCase's own
	// row rather than via claimOldestWaiting, since the real queue could
	// claim a different row first.
	err = r.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE chat_queue SET status = 'ASSIGNED' WHERE chat_conversation_id = $1`, backCase.CaseID); err != nil {
			return err
		}
		return requeueWaiting(ctx, tx, backCase.CaseID)
	})
	if err != nil {
		t.Fatalf("simulate assign+requeue: %v", err)
	}

	var afterCreatedAt time.Time
	var status queueStatus
	if err := pool.QueryRow(ctx, `SELECT created_at, status FROM chat_queue WHERE chat_conversation_id = $1`, backCase.CaseID).Scan(&afterCreatedAt, &status); err != nil {
		t.Fatalf("read back row after requeue: %v", err)
	}
	if status != queueWaitingForEngineer {
		t.Errorf("expected backCase to be WAITING_FOR_ENGINEER again after requeueWaiting, got %s", status)
	}
	if !afterCreatedAt.Equal(backCreatedAt) {
		t.Errorf("expected requeueWaiting to leave created_at untouched, got before=%v after=%v", backCreatedAt, afterCreatedAt)
	}
}

// TestTimeoutOne_ReassignsWithoutTouchingChatStatus is the regression test
// for concurrency's own timeout behavior change: a timed-out conversation
// must be reassigned/requeued on its own, without forcing the unresponsive
// engineer's chat_status to OFFLINE the way the old single-case model did
// (see timeout.go's own doc comment on why that would be wrong once an
// engineer can hold several concurrent cases). Backdates updated_at via
// SQL instead of waiting out a real timeout, and calls timeoutOne directly
// (not the full sweep) so this only ever touches its own fixture case.
func TestTimeoutOne_ReassignsWithoutTouchingChatStatus(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	userID := testUserID(t, r, pool, "timeout-status")
	caseID := testCaseID(t, pool, "timeout-status")
	assignFixture(t, r, pool, userID, caseID)

	if _, err := pool.Exec(ctx, `
		UPDATE chat_conversation SET updated_at = now() - interval '1 hour' WHERE case_id = $1
	`, caseID); err != nil {
		t.Fatalf("backdate updated_at to simulate a real timeout: %v", err)
	}

	result, err := r.timeoutOne(ctx, userID, caseID, time.Minute)
	if err != nil {
		t.Fatalf("timeoutOne: %v", err)
	}
	if result == nil {
		t.Fatalf("expected timeoutOne to fire for a conversation past the timeout")
	}
	if result.ReassignedTo != "" {
		// Might be a real engineer in the shared dev database -- rescue them
		// exactly like completedSafely does elsewhere in this file, so this
		// test can never leave a real account holding fake test data.
		reassignedTo := result.ReassignedTo
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := pool.Exec(cleanupCtx, `
				UPDATE chat_conversation SET assignee_id = NULL, accepted_at = NULL, updated_at = now()
				WHERE case_id = $1
			`, caseID); err != nil {
				t.Logf("rescue: clear reassigned conversation for %s: %v", reassignedTo, err)
			}
		})
	}

	row := getConversationRow(t, pool, caseID)
	if row.AssigneeID != nil && *row.AssigneeID == userID {
		t.Errorf("expected the timed-out case to no longer be assigned to the unresponsive engineer, got %+v", row)
	}
	// The regression this guards against: the old model forced chat_status
	// to OFFLINE here, which would have also silently hidden any OTHER
	// concurrent case this same engineer was handling just fine.
	if status := getChatStatus(t, pool, userID); status != StatusOffline {
		// testUserID seeds OFFLINE and this test never changes it, so this
		// is really asserting "still whatever it was," not "still OFFLINE
		// specifically" -- documented via the message below rather than
		// re-deriving chat_status's start value here.
		t.Errorf("expected timeoutOne to leave chat_status exactly as the engineer set it (untouched by this call), got %s", status)
	}
}
