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

package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Migration 0191: a WORK_NOTE comment is internal. A project member may read
// and write the other comment types of its own project, never a work note,
// and an internal caller sees everything.

const (
	wnProjectID = "74000000-0000-0000-0000-000000000001"
	wnAccountID = "74111111-0000-0000-0000-000000000001"
	wnContactID = "74222222-0000-0000-0000-000000000001"
	wnWorkItem  = "74333333-0000-0000-0000-000000000001"

	wnPlainComment   = "74444444-0000-0000-0000-000000000001"
	wnWorkNote       = "74444444-0000-0000-0000-000000000002"
	wnApprovalRecord = "74444444-0000-0000-0000-000000000003"
	wnNullType       = "74444444-0000-0000-0000-000000000004"

	wnMember   = "wn-member@test.local"
	wnStranger = "wn-stranger@test.local"
)

func wnContexts() (internal, member, stranger context.Context) {
	base := context.Background()
	internal = repository.WithSystemIdentity(base)
	member = repository.WithCallerIdentity(base, repository.SearchScope{ProjectIDs: []string{wnProjectID}, ViewerEmail: wnMember})
	stranger = repository.WithCallerIdentity(base, repository.SearchScope{ViewerEmail: wnStranger})
	return
}

func seedWorkNoteFixture(t *testing.T, pool *pgxpool.Pool) *repository.Scoped {
	t.Helper()
	internal, _, _ := wnContexts()
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(internal, `DELETE FROM comment_edit_history WHERE comment_id IN ($1, $2, $3, $4)`, wnPlainComment, wnWorkNote, wnApprovalRecord, wnNullType)
		_, _ = scoped.Exec(internal, `DELETE FROM comment WHERE work_item_id = $1`, wnWorkItem)
		_, _ = scoped.Exec(internal, `DELETE FROM work_item WHERE project_id = $1`, wnProjectID)
		_, _ = pool.Exec(internal, `DELETE FROM project_contact WHERE project_id = $1`, wnProjectID)
		_, _ = pool.Exec(internal, `DELETE FROM project WHERE id = $1`, wnProjectID)
		_, _ = pool.Exec(internal, `DELETE FROM account_contact WHERE id = $1`, wnContactID)
		_, _ = pool.Exec(internal, `DELETE FROM account WHERE id = $1`, wnAccountID)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC()
	mustPool := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(internal, sql, args...); err != nil {
			t.Fatalf("seed (%.80s): %v", sql, err)
		}
	}
	mustScoped := func(sql string, args ...any) {
		t.Helper()
		if _, err := scoped.Exec(internal, sql, args...); err != nil {
			t.Fatalf("seed scoped (%.80s): %v", sql, err)
		}
	}

	mustPool(`INSERT INTO account (id, created_on, updated_on, created_by, updated_by, name, number, sf_id)
		VALUES ($1, $2, $2, 'test', 'test', 'WN Test Account', 'WN-ACC-1', 'sf-wn-acc-1')`, wnAccountID, now)
	mustPool(`INSERT INTO account_contact (id, created_on, updated_on, created_by, updated_by, user_name, account_id)
		VALUES ($1, $2, $2, 'test', 'test', 'WN Test Contact', $3)`, wnContactID, now, wnAccountID)
	mustPool(`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, sf_id, account_id)
		VALUES ($1, $2, $2, 'test', 'test', 'WNTEST', 'sf-wntest', $3)`, wnProjectID, now, wnAccountID)
	mustPool(`INSERT INTO project_contact (id, created_on, updated_on, created_by, updated_by, email, account_contact_id, project_id, state)
		VALUES (gen_random_uuid(), $1, $1, 'test', 'test', $2, $3, $4, 'REGISTERED')`, now, wnMember, wnContactID, wnProjectID)

	mustScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type, project_id)
		VALUES ($1, $2, $2, 'test', 'test', 'WN-CASE-1', 'WN-WSO2-1', 'wn case', 'CASE', $3)`, wnWorkItem, now, wnProjectID)

	for _, c := range []struct{ id, typ, body string }{
		{wnPlainComment, "COMMENT", "visible to the customer"},
		{wnWorkNote, "WORK_NOTE", "internal note"},
		{wnApprovalRecord, "APPROVAL_HISTORY", "approval trail"},
	} {
		mustScoped(`INSERT INTO comment (id, created_on, created_by, type, work_item_id, content)
			VALUES ($1, $2, 'test', $3::comment_type_enum, $4, $5)`, c.id, now, c.typ, wnWorkItem, c.body)
	}
	mustScoped(`INSERT INTO comment (id, created_on, created_by, work_item_id, content)
		VALUES ($1, $2, 'test', $3, 'comment with no type')`, wnNullType, now, wnWorkItem)
	for _, id := range []string{wnPlainComment, wnWorkNote} {
		mustScoped(`INSERT INTO comment_edit_history (comment_id, body, edited_by, edited_at) VALUES ($1, 'older body', 'test', $2)`, id, now)
	}
	return scoped
}

func commentTypesVisible(t *testing.T, ctx context.Context, scoped *repository.Scoped) map[string]int {
	t.Helper()
	rows, err := scoped.Query(ctx, `SELECT coalesce(type::text, '(none)'), count(*) FROM comment WHERE work_item_id = $1 GROUP BY 1`, wnWorkItem)
	if err != nil {
		t.Fatalf("query comments: %v", err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[typ] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return got
}

func isRLSViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func TestCommentWorkNoteRLS_MemberDoesNotSeeWorkNotes(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	internal, member, stranger := wnContexts()

	if got := commentTypesVisible(t, internal, scoped); got["COMMENT"] != 1 || got["WORK_NOTE"] != 1 || got["APPROVAL_HISTORY"] != 1 {
		t.Fatalf("internal must see every comment type, got %v", got)
	}

	got := commentTypesVisible(t, member, scoped)
	if got["WORK_NOTE"] != 0 {
		t.Fatalf("a project member must not see a work note, got %v", got)
	}
	if got["COMMENT"] != 1 || got["APPROVAL_HISTORY"] != 1 {
		t.Fatalf("a project member must still see COMMENT and APPROVAL_HISTORY, got %v", got)
	}

	// by id, not just in a list
	var content string
	err := scoped.QueryRow(member, `SELECT content FROM comment WHERE id = $1`, wnWorkNote).Scan(&content)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a work note fetched by id must be invisible to a member, got content=%q err=%v", content, err)
	}

	if got := commentTypesVisible(t, stranger, scoped); len(got) != 0 {
		t.Fatalf("a caller outside the project must see nothing, got %v", got)
	}
}

func TestCommentWorkNoteRLS_MemberCannotWriteOrPromoteToWorkNote(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	_, member, _ := wnContexts()

	// a member can still add a plain comment and edit it (the portal's own flow)
	var newID string
	if err := scoped.QueryRow(member, `
		INSERT INTO comment (id, created_on, created_by, type, work_item_id, content)
		VALUES (gen_random_uuid(), now(), $1, 'COMMENT'::comment_type_enum, $2, 'customer reply') RETURNING id`,
		wnMember, wnWorkItem).Scan(&newID); err != nil {
		t.Fatalf("a member must still be able to add a COMMENT: %v", err)
	}
	if tag, err := scoped.Exec(member, `UPDATE comment SET content = 'edited reply' WHERE id = $1`, newID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("a member must still be able to edit a COMMENT: rows=%d err=%v", tag.RowsAffected(), err)
	}

	// ...but not write a work note,
	_, err := scoped.Exec(member, `
		INSERT INTO comment (id, created_on, created_by, type, work_item_id, content)
		VALUES (gen_random_uuid(), now(), $1, 'WORK_NOTE'::comment_type_enum, $2, 'forged internal note')`,
		wnMember, wnWorkItem)
	if !isRLSViolation(err) {
		t.Fatalf("a member inserting a WORK_NOTE must be refused by RLS (42501), got %v", err)
	}

	// turn an existing comment into one,
	_, err = scoped.Exec(member, `UPDATE comment SET type = 'WORK_NOTE'::comment_type_enum WHERE id = $1`, newID)
	if !isRLSViolation(err) {
		t.Fatalf("a member re-typing a COMMENT to WORK_NOTE must be refused by RLS (42501), got %v", err)
	}

	// or touch the support team's own note: it is invisible, so it matches no row.
	tag, err := scoped.Exec(member, `UPDATE comment SET content = 'tampered' WHERE id = $1`, wnWorkNote)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("a member must not be able to edit a work note, rows=%d err=%v", tag.RowsAffected(), err)
	}
}

func TestCommentWorkNoteRLS_EditHistoryFollowsTheComment(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	_, member, _ := wnContexts()

	count := func(commentID string) int {
		var n int
		if err := scoped.QueryRow(member, `SELECT count(*) FROM comment_edit_history WHERE comment_id = $1`, commentID).Scan(&n); err != nil {
			t.Fatalf("count history: %v", err)
		}
		return n
	}
	if n := count(wnPlainComment); n != 1 {
		t.Fatalf("a member must see the edit history of a plain comment, got %d", n)
	}
	if n := count(wnWorkNote); n != 0 {
		t.Fatalf("a member must not see the edit history of a work note, got %d", n)
	}
}

// The escalation work note is written as the system identity (see
// CreateCaseEscalation): that one write must keep working.
func TestCommentWorkNoteRLS_SystemIdentityCanStillWriteWorkNotes(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	internal, member, _ := wnContexts()

	if _, err := scoped.Exec(internal, `
		INSERT INTO comment (id, created_on, created_by, type, work_item_id, content)
		VALUES (gen_random_uuid(), now(), 'system', 'WORK_NOTE'::comment_type_enum, $1, 'Case escalated from EL0 to EL1.')`,
		wnWorkItem); err != nil {
		t.Fatalf("the system identity must be able to write a work note: %v", err)
	}
	if got := commentTypesVisible(t, member, scoped); got["WORK_NOTE"] != 0 {
		t.Fatalf("the new work note must stay hidden from the member, got %v", got)
	}
}

// A comment with no type is not a work note: it stays visible, as it is today.
func TestCommentWorkNoteRLS_CommentWithNoTypeStaysVisible(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	_, member, _ := wnContexts()

	var content string
	if err := scoped.QueryRow(member, `SELECT content FROM comment WHERE id = $1`, wnNullType).Scan(&content); err != nil {
		t.Fatalf("a member must still see a comment with no type: %v", err)
	}
}

// CreateCaseComment writes through INSERT ... SELECT FROM work_item ... RETURNING,
// not a plain INSERT: run that exact shape as a member.
func TestCommentWorkNoteRLS_ServiceInsertShapeFollowsTheSameRule(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	_, member, _ := wnContexts()

	const query = `
		INSERT INTO comment (id, created_on, created_by, type, work_item_id, content)
		SELECT gen_random_uuid(), COALESCE($5, NOW()), $1, $2::comment_type_enum, w.id, $4
		FROM work_item w
		WHERE w.id = $3
		RETURNING id, work_item_id, type, content, created_by, created_on`
	var id, workItemID, typ, content, createdBy string
	var createdOn time.Time

	err := scoped.QueryRow(member, query, wnMember, "COMMENT", wnWorkItem, "customer reply", nil).
		Scan(&id, &workItemID, &typ, &content, &createdBy, &createdOn)
	if err != nil {
		t.Fatalf("a member must still be able to add a COMMENT the way CreateCaseComment does: %v", err)
	}

	err = scoped.QueryRow(member, query, wnMember, "WORK_NOTE", wnWorkItem, "forged note", nil).
		Scan(&id, &workItemID, &typ, &content, &createdBy, &createdOn)
	if !isRLSViolation(err) {
		t.Fatalf("a member adding a WORK_NOTE the way CreateCaseComment does must be refused (42501), got %v", err)
	}
}

// The edit history of a hidden work note cannot be written to either.
func TestCommentWorkNoteRLS_MemberCannotWriteEditHistoryOfAWorkNote(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	_, member, _ := wnContexts()

	const insert = `INSERT INTO comment_edit_history (comment_id, body, edited_by, edited_at) VALUES ($1, 'earlier body', $2, now())`
	if _, err := scoped.Exec(member, insert, wnPlainComment, wnMember); err != nil {
		t.Fatalf("a member must still be able to record the edit history of a plain comment: %v", err)
	}
	_, err := scoped.Exec(member, insert, wnWorkNote, wnMember)
	if !isRLSViolation(err) {
		t.Fatalf("recording edit history against a work note must be refused (42501), got %v", err)
	}
}

// The two real repository operations, called as the case service calls them,
// with a project member's identity: the ordinary create refuses a WORK_NOTE
// (and still accepts a COMMENT), while CreateCaseCommentAsSystem, which is
// what the escalation note and the ServiceNow mirror use, writes it and
// keeps a ServiceNow timestamp it is given. The member still cannot see the
// note afterwards.
func TestCommentWorkNoteRLS_CaseRepoSystemOperationWritesWhatMemberCannot(t *testing.T) {
	pool := caseStatsPool(t)
	scoped := seedWorkNoteFixture(t, pool)
	_, member, _ := wnContexts()
	repo := repository.NewCaseRepository(scoped)

	note := domain.CreateCaseCommentRequest{CaseID: wnWorkItem, Type: domain.CommentTypeWorkNote, Content: "internal note", CreatedBy: wnMember}

	// the caller-identity create is refused for a work note ...
	if _, err := repo.CreateCaseComment(member, note, nil); !isRLSViolation(err) {
		t.Fatalf("a member must not be able to write a work note through CreateCaseComment, got %v", err)
	}
	// ... and still works for an ordinary comment
	if _, err := repo.CreateCaseComment(member, domain.CreateCaseCommentRequest{CaseID: wnWorkItem, Type: domain.CommentTypeComment, Content: "hello", CreatedBy: wnMember}, nil); err != nil {
		t.Fatalf("a member must still be able to write an ordinary comment: %v", err)
	}

	// the system operation writes the note, from the same member context
	when := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	got, err := repo.CreateCaseCommentAsSystem(member, note, &when)
	if err != nil {
		t.Fatalf("CreateCaseCommentAsSystem must write a work note even for a member's context: %v", err)
	}
	if got.Type != domain.CommentTypeWorkNote || got.CaseID != wnWorkItem || got.Content != "internal note" {
		t.Fatalf("unexpected comment back: %+v", got)
	}
	if !got.CreatedOn.Equal(when) {
		t.Fatalf("ServiceNow's CreatedOn must be kept, got %v want %v", got.CreatedOn, when)
	}

	// the member still does not see any work note (the seeded one nor the new one)
	if v := commentTypesVisible(t, member, scoped); v["WORK_NOTE"] != 0 {
		t.Fatalf("the member must not see work notes, got %v", v)
	}
	internal, _, _ := wnContexts()
	if v := commentTypesVisible(t, internal, scoped); v["WORK_NOTE"] != 2 {
		t.Fatalf("expected the seeded and the new work note for an internal caller, got %v", v)
	}
}
