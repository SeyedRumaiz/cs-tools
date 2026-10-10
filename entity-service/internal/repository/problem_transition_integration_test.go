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

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// ApplyProblemTransition against a real database: a seeded problem walked
// through ServiceNow's five moves with every side effect checked, plus the
// refusals. Run with ENTITY_TEST_DATABASE_URL=postgres://... ; the rows it
// seeds are deleted afterwards.
const (
	ptProblemID = "47777777-0000-0000-0000-0000000000b1"
	ptUserID    = "47777777-0000-0000-0000-0000000000b2"
	ptGroupID   = "47777777-0000-0000-0000-0000000000b3"
)

var ptMoves = map[string]ProblemTransition{
	"assess":  {"assess", "NEW", "ASSESS"},
	"confirm": {"confirm", "ASSESS", "ROOT_CAUSE_ANALYSIS"},
	"fix":     {"fix", "ROOT_CAUSE_ANALYSIS", "FIX_IN_PROGRESS"},
	"resolve": {"resolve", "FIX_IN_PROGRESS", "RESOLVED"},
	"close":   {"close", "RESOLVED", "CLOSED"},
}

func ptSetup(t *testing.T) (context.Context, *pgxpool.Pool, ProblemRepository) {
	t.Helper()
	dsn := os.Getenv("ENTITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ENTITY_TEST_DATABASE_URL not set")
	}
	ctx := WithSystemIdentity(context.Background())
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", strings.SplitN(strings.TrimSpace(sql), "\n", 2)[0], err)
		}
	}
	cleanup := func() {
		for _, q := range []string{
			`DELETE FROM work_item WHERE id = '` + ptProblemID + `'`,
			`DELETE FROM "group" WHERE id = '` + ptGroupID + `'`,
			`DELETE FROM "user" WHERE id = '` + ptUserID + `'`,
		} {
			if _, err := pool.Exec(context.Background(), q); err != nil {
				t.Errorf("CLEANUP FAILED (%s): %v", q, err)
			}
		}
	}
	cleanup()
	t.Cleanup(func() { cleanup(); pool.Close() })
	exec(`INSERT INTO "user" (id, created_on, updated_on, user_name, email) VALUES ($1, NOW(), NOW(), 'pt.test', 'PT.Test@example.com')`, ptUserID)
	exec(`INSERT INTO "group" (id, created_on, updated_on, created_by, updated_by, name) VALUES ($1, NOW(), NOW(), 't', 't', 'PT group')`, ptGroupID)
	exec(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, subject, type)
	      VALUES ($1, NOW(), NOW(), 't', 't', 'PRB-PT-0001', 'transition test', 'PROBLEM')`, ptProblemID)
	exec(`INSERT INTO problem (id, state, problem_state, is_active, opened_on) VALUES ($1, 'NEW', 'NEW', TRUE, NOW())`, ptProblemID)
	return ctx, pool, NewProblemRepository(NewScoped(pool))
}

type ptRow struct {
	state, problemState, resolutionCode, resolvedBy, group, causeNotes string
	active, resolvedOn, closedOn                                       bool
}

func ptRead(t *testing.T, ctx context.Context, pool *pgxpool.Pool) ptRow {
	t.Helper()
	var r ptRow
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(p.state::text,''), COALESCE(p.problem_state::text,''), COALESCE(p.resolution_code::text,''),
		       COALESCE(p.resolved_by_id::text,''), COALESCE(wi.assignment_group_id::text,''), COALESCE(p.cause_notes,''),
		       COALESCE(p.is_active, FALSE), p.resolved_on IS NOT NULL, p.closed_on IS NOT NULL
		FROM problem p JOIN work_item wi ON wi.id = p.id WHERE p.id = $1`, ptProblemID).
		Scan(&r.state, &r.problemState, &r.resolutionCode, &r.resolvedBy, &r.group, &r.causeNotes, &r.active, &r.resolvedOn, &r.closedOn)
	if err != nil {
		t.Fatalf("read problem: %v", err)
	}
	return r
}

func TestProblemTransitionIntegration_WalksTheStateModel(t *testing.T) {
	ctx, pool, repo := ptSetup(t)
	req := domain.UpdateProblemRequest{ID: ptProblemID}
	const actor = "pt.test@example.com" // matched case-insensitively

	// A move from the wrong state is refused, naming the current state.
	_, err := repo.ApplyProblemTransition(ctx, req, ptMoves["fix"], true, actor)
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Msg, "Current state: NEW") {
		t.Fatalf("fix from NEW: err = %v, want a ValidationError naming NEW", err)
	}

	// assess, with the assignment group in the same save.
	group := ptGroupID
	if _, err := repo.ApplyProblemTransition(ctx, domain.UpdateProblemRequest{ID: ptProblemID, AssignmentGroupID: &group},
		ptMoves["assess"], true, actor); err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "ASSESS" || r.problemState != "ASSESS" || r.group != ptGroupID {
		t.Errorf("after assess: %+v", r)
	}

	// confirm, then fix carrying cause notes, as ServiceNow's Fix action does.
	if _, err := repo.ApplyProblemTransition(ctx, req, ptMoves["confirm"], true, actor); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	cause := "disk full"
	if _, err := repo.ApplyProblemTransition(ctx, domain.UpdateProblemRequest{ID: ptProblemID, CauseNotes: &cause},
		ptMoves["fix"], true, actor); err != nil {
		t.Fatalf("fix: %v", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "FIX_IN_PROGRESS" || r.causeNotes != "disk full" {
		t.Errorf("after fix: %+v", r)
	}

	// resolve: FIX_APPLIED, resolved on/by.
	if _, err := repo.ApplyProblemTransition(ctx, req, ptMoves["resolve"], true, actor); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "RESOLVED" || r.resolutionCode != "FIX_APPLIED" || !r.resolvedOn || r.resolvedBy != ptUserID {
		t.Errorf("after resolve: %+v", r)
	}

	// close: inactive, closed on.
	if _, err := repo.ApplyProblemTransition(ctx, req, ptMoves["close"], true, actor); err != nil {
		t.Fatalf("close: %v", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "CLOSED" || r.problemState != "CLOSED" || r.active || !r.closedOn {
		t.Errorf("after close: %+v", r)
	}
}

func TestProblemTransitionIntegration_RefusalsAndDualWrite(t *testing.T) {
	ctx, pool, repo := ptSetup(t)
	req := domain.UpdateProblemRequest{ID: ptProblemID}

	// Dual-write applies ServiceNow's accepted move whatever Postgres holds.
	if _, err := repo.ApplyProblemTransition(ctx, req, ptMoves["resolve"], false, "x@example.com"); err != nil {
		t.Fatalf("resolve without the from-state check: %v", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "RESOLVED" || r.resolvedBy != "" {
		t.Errorf("after an unenforced resolve by an unknown user: %+v (resolved_by must stay empty, not fail)", r)
	}

	// close is refused on a risk-accepted problem, as ProblemUtils refuses it.
	if _, err := pool.Exec(ctx, `UPDATE problem SET resolution_code = 'RISK_ACCEPTED' WHERE id = $1`, ptProblemID); err != nil {
		t.Fatalf("set risk accepted: %v", err)
	}
	_, err := repo.ApplyProblemTransition(ctx, req, ptMoves["close"], true, "x@example.com")
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("close on RISK_ACCEPTED: err = %v, want a ValidationError", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "RESOLVED" {
		t.Errorf("a refused close changed the state: %+v", r)
	}

	// A group that does not exist is a ValidationError, and rolls the move back.
	missing := "47777777-0000-0000-0000-0000000000ff"
	if _, err := pool.Exec(ctx, `UPDATE problem SET resolution_code = NULL WHERE id = $1`, ptProblemID); err != nil {
		t.Fatalf("clear code: %v", err)
	}
	_, err = repo.ApplyProblemTransition(ctx, domain.UpdateProblemRequest{ID: ptProblemID, AssignmentGroupID: &missing},
		ptMoves["close"], true, "x@example.com")
	if !errors.As(err, &ve) {
		t.Errorf("unknown group: err = %v, want a ValidationError", err)
	}
	if r := ptRead(t, ctx, pool); r.state != "RESOLVED" {
		t.Errorf("the move survived a failed field write: %+v", r)
	}

	// Unknown problem.
	_, err = repo.ApplyProblemTransition(ctx, domain.UpdateProblemRequest{ID: "47777777-0000-0000-0000-0000000000fe"}, ptMoves["assess"], true, "x@example.com")
	var nf *apierror.NotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("unknown problem: err = %v, want NotFound", err)
	}
}
