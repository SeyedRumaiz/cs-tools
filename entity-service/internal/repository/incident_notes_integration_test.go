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

// Integration tests for the incident create and note writes that must commit as one transaction: an
// incident is never left without the work note it was created with, and a two-note update never
// saves one half. A note containing a NUL byte is rejected by Postgres itself, which is how these
// force the second write to fail. Same DSN and skip-when-unset pattern as the other repository
// integration tests (reuses caseStatsPool):
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run IncidentNotesIntegration

package repository_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	notesTestActor     = "incident-notes-test@example.com"
	notesTestUserID    = "49222222-0000-0000-0000-000000000001"
	notesTestServiceID = "49333333-0000-0000-0000-000000000001"
)

// seedNotesFixture creates the caller and service an incident must reference, and removes everything
// the test creates (incidents are found by their created_by) when it ends.
func seedNotesFixture(t *testing.T, pool *pgxpool.Pool) context.Context {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM comment WHERE created_by = $1`, notesTestActor)
		_, _ = scoped.Exec(ctx, `DELETE FROM incident WHERE id IN (SELECT id FROM work_item WHERE created_by = $1)`, notesTestActor)
		_, _ = scoped.Exec(ctx, `DELETE FROM work_item WHERE created_by = $1`, notesTestActor)
		_, _ = pool.Exec(ctx, `DELETE FROM service WHERE id = $1`, notesTestServiceID)
		_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, notesTestUserID)
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user" (id, created_on, updated_on, user_name, email, is_active) VALUES ($1, now(), now(), $2, $2, true)`,
			[]any{notesTestUserID, notesTestActor}},
		{`INSERT INTO service (id, created_on, updated_on, created_by, updated_by, name, number) VALUES ($1, now(), now(), $2, $2, 'notes test service', 'SVC-NOTES-TEST')`,
			[]any{notesTestServiceID, notesTestActor}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed (%.50s): %v", q.sql, err)
		}
	}
	return ctx
}

func notesTestRequest(workNotes string) domain.CreateIncidentRequest {
	return domain.CreateIncidentRequest{
		CallerID: notesTestUserID, Category: "SERVICE_INTERRUPTION", ServiceID: notesTestServiceID,
		Impact: "HIGH", Urgency: "HIGH", Subject: "notes test alarm", WorkNotes: &workNotes,
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := repository.NewScoped(pool).QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("count (%.50s): %v", sql, err)
	}
	return n
}

func TestIncidentNotesIntegration_CreateSavesTheIncidentWithItsWorkNote(t *testing.T) {
	pool := caseStatsPool(t)
	ctx := seedNotesFixture(t, pool)
	repo := repository.NewIncidentRepository(repository.NewScoped(pool))

	resp, err := repo.CreateIncident(ctx, notesTestRequest("AWS alarm: CPU > 90%"), "CRITICAL", nil, notesTestActor)
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM comment WHERE work_item_id = $1 AND type = 'WORK_NOTE'`, resp.Incident.ID); n != 1 {
		t.Errorf("%d work notes saved with the incident, want 1", n)
	}
}

// A work note that cannot be saved leaves no incident behind, so the create is never reported as a
// success (and incident.created is never published) for an incident missing its alert.
func TestIncidentNotesIntegration_CreateRollsBackWhenItsNoteFails(t *testing.T) {
	pool := caseStatsPool(t)
	ctx := seedNotesFixture(t, pool)
	repo := repository.NewIncidentRepository(repository.NewScoped(pool))

	if _, err := repo.CreateIncident(ctx, notesTestRequest("bad note \x00"), "CRITICAL", nil, notesTestActor); err == nil {
		t.Fatal("CreateIncident succeeded with a note Postgres rejects")
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM work_item WHERE created_by = $1`, notesTestActor); n != 0 {
		t.Errorf("%d incident(s) left behind by the failed create, want 0", n)
	}
}

// When the second of two notes fails, neither is saved, so a retry cannot save the first one twice.
func TestIncidentNotesIntegration_UpdateSavesBothNotesOrNeither(t *testing.T) {
	pool := caseStatsPool(t)
	ctx := seedNotesFixture(t, pool)
	repo := repository.NewIncidentRepository(repository.NewScoped(pool))
	resp, err := repo.CreateIncident(ctx, notesTestRequest(""), "CRITICAL", nil, notesTestActor)
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	id := resp.Incident.ID

	work, bad := "duplicate alert", "comment \x00"
	if err := repo.CreateIncidentNotes(ctx, id, &work, &bad, notesTestActor); err == nil {
		t.Fatal("CreateIncidentNotes succeeded with a comment Postgres rejects")
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM comment WHERE work_item_id = $1`, id); n != 0 {
		t.Fatalf("%d note(s) saved by the failed update, want 0", n)
	}

	good := "triaging"
	if err := repo.CreateIncidentNotes(ctx, id, &work, &good, notesTestActor); err != nil {
		t.Fatalf("CreateIncidentNotes: %v", err)
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM comment WHERE work_item_id = $1`, id); n != 2 {
		t.Errorf("%d note(s) saved, want both", n)
	}
}
