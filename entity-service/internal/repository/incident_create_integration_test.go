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

// Exercises IncidentRepository.CreateIncident (the plain-Postgres create)
// against a real Postgres with migrations through 0185 applied: priority,
// subcategory, configuration item, assigned engineer, watch list and the
// comment/work-note journal rows all land in one transaction, and a bad
// watcher or a subcategory from another category rolls the whole create
// back. Skipped without INCIDENT_CREATE_TEST_DSN.
//
//	INCIDENT_CREATE_TEST_DSN=postgres://... go test ./internal/repository/ -run IncidentCreate

package repository_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	icCallerID   = "d1000000-0000-0000-0000-000000000001"
	icEngineerID = "d1000000-0000-0000-0000-000000000002"
	icWatcherID  = "d1000000-0000-0000-0000-000000000003"
	icServiceID  = "d2000000-0000-0000-0000-000000000001"
	icCIID       = "d3000000-0000-0000-0000-000000000001"
	icSubject    = "incident-create integration test"
)

func incidentCreatePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("INCIDENT_CREATE_TEST_DSN")
	if dsn == "" {
		t.Skip("INCIDENT_CREATE_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedIncidentCreateFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM work_item WHERE subject = $1`, icSubject)
		_, _ = pool.Exec(ctx, `DELETE FROM service WHERE id = $1`, icServiceID)
		_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id = ANY($1::uuid[])`, []string{icCallerID, icEngineerID, icWatcherID})
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC()
	for _, u := range []struct{ id, name, email string }{
		{icCallerID, "ic-caller", "ic-caller@test.local"},
		{icEngineerID, "ic-engineer", "ic-engineer@test.local"},
		{icWatcherID, "ic-watcher", "IC-Watcher@test.local"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO "user" (id, created_on, updated_on, user_name, email) VALUES ($1, $2, $2, $3, $4)`,
			u.id, now, u.name, u.email); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO service (id, created_on, updated_on, created_by, updated_by, name, number)
		VALUES ($1, $2, $2, 'test', 'test', 'IC Service', 'IC-SVC-1')`, icServiceID, now); err != nil {
		t.Fatalf("seed service: %v", err)
	}
}

func icRequest() domain.CreateIncidentRequest {
	sub := domain.IncidentSubcategoryPartialOutage
	ci := icCIID
	engineer := icEngineerID
	comments := "customer-visible description"
	notes := "internal work note"
	return domain.CreateIncidentRequest{
		Subject:             icSubject,
		CallerID:            icCallerID,
		Category:            domain.IncidentCategoryServiceInterruption,
		Subcategory:         &sub,
		ServiceID:           icServiceID,
		ConfigurationItemID: &ci,
		Impact:              domain.IncidentImpactMedium,
		Urgency:             domain.IncidentUrgencyHigh,
		AssignedEngineerID:  &engineer,
		WatchList:           []string{icWatcherID, "ic-watcher@TEST.local"},
		AdditionalComments:  &comments,
		WorkNotes:           &notes,
	}
}

func TestIncidentCreate_PersistsEveryField(t *testing.T) {
	pool := incidentCreatePool(t)
	seedIncidentCreateFixture(t, pool)
	scoped := repository.NewScoped(pool)
	repo := repository.NewIncidentRepository(scoped)
	ctx := repository.WithSystemIdentity(context.Background())

	sub := "Partial Outage"
	resp, err := repo.CreateIncident(ctx, icRequest(), "HIGH", &sub, "jane.doe@test.local")
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	if resp.Incident.ID == "" || resp.Incident.Number == "" || resp.Incident.CreatedBy != "jane.doe@test.local" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	var state, priority, subLabel, ci, assignedTo string
	if err := scoped.QueryRow(ctx, `
		SELECT inc.state::text, inc.priority::text, sc.label, inc.cmdb_ci_id::text, wi.assigned_to_id::text
		FROM incident inc
		JOIN work_item wi ON wi.id = inc.id
		JOIN incident_subcategory sc ON sc.id = inc.subcategory_id
		WHERE inc.id = $1`, resp.Incident.ID).Scan(&state, &priority, &subLabel, &ci, &assignedTo); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "NEW" || priority != "HIGH" || subLabel != "Partial Outage" || ci != icCIID || assignedTo != icEngineerID {
		t.Errorf("got state=%s priority=%s subcategory=%s ci=%s assignedTo=%s", state, priority, subLabel, ci, assignedTo)
	}

	var watchers int
	if err := scoped.QueryRow(ctx, `SELECT count(*) FROM work_item_watcher WHERE work_item_id = $1 AND user_id = $2`,
		resp.Incident.ID, icWatcherID).Scan(&watchers); err != nil {
		t.Fatalf("read watchers: %v", err)
	}
	if watchers != 1 {
		t.Errorf("watchers = %d, want 1 (an id and an email for the same user collapse)", watchers)
	}

	rows, err := scoped.Query(ctx, `SELECT type::text, content, created_by FROM comment WHERE work_item_id = $1 ORDER BY type`, resp.Incident.ID)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var typ, content, by string
		if err := rows.Scan(&typ, &content, &by); err != nil {
			t.Fatalf("scan comment: %v", err)
		}
		if by != "jane.doe@test.local" {
			t.Errorf("comment %s created_by = %s", typ, by)
		}
		got[typ] = content
	}
	if got["COMMENT"] != "customer-visible description" || got["WORK_NOTE"] != "internal work note" || len(got) != 2 {
		t.Errorf("journal rows = %v", got)
	}
}

// TestIncidentCreate_FromServiceNowStoresJournal: the dual-write create stores its work note and comment as the plain create does.
func TestIncidentCreate_FromServiceNowStoresJournal(t *testing.T) {
	pool := incidentCreatePool(t)
	seedIncidentCreateFixture(t, pool)
	scoped := repository.NewScoped(pool)
	repo := repository.NewIncidentRepository(scoped)
	ctx := repository.WithSystemIdentity(context.Background())

	const snID = "d4000000-0000-0000-0000-000000000001"
	req := icRequest()
	req.Subcategory, req.ConfigurationItemID, req.AssignedEngineerID, req.WatchList = nil, nil, nil, nil
	resp, err := repo.CreateIncidentFromServiceNow(ctx, req, snID, "INC-IC-SN-1", "sn.user@test.local")
	if err != nil {
		t.Fatalf("CreateIncidentFromServiceNow: %v", err)
	}
	if resp.Incident.ID != snID {
		t.Fatalf("id = %s, want %s", resp.Incident.ID, snID)
	}

	rows, err := scoped.Query(ctx, `SELECT type::text, content, created_by FROM comment WHERE work_item_id = $1`, snID)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var typ, content, by string
		if err := rows.Scan(&typ, &content, &by); err != nil {
			t.Fatalf("scan comment: %v", err)
		}
		if by != "sn.user@test.local" {
			t.Errorf("comment %s created_by = %s", typ, by)
		}
		got[typ] = content
	}
	if got["COMMENT"] != "customer-visible description" || got["WORK_NOTE"] != "internal work note" || len(got) != 2 {
		t.Errorf("journal rows = %v", got)
	}
}

// TestIncidentCreate_WithoutSubcategory: subcategory is optional on create
// (the webapp form no longer requires it), so a request with none must
// insert with incident.subcategory_id NULL and the category still set --
// the nullable column plus incident_subcategory_requires_category (which
// only constrains a NON-null subcategory_id) must both accept that.
func TestIncidentCreate_WithoutSubcategory(t *testing.T) {
	pool := incidentCreatePool(t)
	seedIncidentCreateFixture(t, pool)
	scoped := repository.NewScoped(pool)
	repo := repository.NewIncidentRepository(scoped)
	ctx := repository.WithSystemIdentity(context.Background())

	req := icRequest()
	req.Subcategory = nil
	resp, err := repo.CreateIncident(ctx, req, "HIGH", nil, "jane.doe@test.local")
	if err != nil {
		t.Fatalf("CreateIncident without subcategory: %v", err)
	}
	if resp.Incident.ID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	var category string
	var subcategoryIsNull bool
	if err := scoped.QueryRow(ctx,
		`SELECT category::text, subcategory_id IS NULL FROM incident WHERE id = $1`,
		resp.Incident.ID).Scan(&category, &subcategoryIsNull); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if category != string(domain.IncidentCategoryServiceInterruption) || !subcategoryIsNull {
		t.Errorf("got category=%s subcategory_id IS NULL=%v, want SERVICE_INTERRUPTION and true", category, subcategoryIsNull)
	}

	view, err := repo.GetIncidentByID(ctx, resp.Incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if view.Subcategory != nil {
		t.Errorf("view.Subcategory = %v, want nil", *view.Subcategory)
	}
}

// TestIncidentCreate_LifecycleWithoutSubcategory takes an incident created
// with no subcategory through New -> In Progress -> Resolved -> Closed and
// re-reads it through GetIncidentByID after each stage: state must follow,
// and subcategory_id must stay NULL with the category intact.
//
// The state changes are written directly in SQL. Nothing in entity-service
// transitions a Postgres incident's state (incidentService.UpdateIncident
// rejects `state` on both Postgres data sources; transitions go through
// ServiceNow -- see handler/incident_lifecycle_test.go), so this covers the
// database side only: that no constraint or trigger on incident (the
// incident_subcategory_requires_category CHECK, the category/subcategory
// FK, the incident_outbox AFTER UPDATE trigger) rejects a NULL subcategory
// at any stage, and that the read path maps every stage correctly.
func TestIncidentCreate_LifecycleWithoutSubcategory(t *testing.T) {
	pool := incidentCreatePool(t)
	seedIncidentCreateFixture(t, pool)
	scoped := repository.NewScoped(pool)
	repo := repository.NewIncidentRepository(scoped)
	ctx := repository.WithSystemIdentity(context.Background())

	req := icRequest()
	req.Subcategory = nil
	resp, err := repo.CreateIncident(ctx, req, "HIGH", nil, "jane.doe@test.local")
	if err != nil {
		t.Fatalf("CreateIncident without subcategory: %v", err)
	}
	id := resp.Incident.ID

	assertStage := func(t *testing.T, stage, wantState string) {
		t.Helper()
		view, err := repo.GetIncidentByID(ctx, id)
		if err != nil {
			t.Fatalf("%s: GetIncidentByID: %v", stage, err)
		}
		if view.State == nil || *view.State != wantState {
			t.Errorf("%s: state = %v, want %s", stage, view.State, wantState)
		}
		if view.Subcategory != nil {
			t.Errorf("%s: subcategory = %s, want none", stage, *view.Subcategory)
		}
		if view.Category == nil || *view.Category != string(domain.IncidentCategoryServiceInterruption) {
			t.Errorf("%s: category = %v, want SERVICE_INTERRUPTION", stage, view.Category)
		}
		var subcategoryIsNull bool
		if err := scoped.QueryRow(ctx, `SELECT subcategory_id IS NULL FROM incident WHERE id = $1`, id).Scan(&subcategoryIsNull); err != nil {
			t.Fatalf("%s: read subcategory_id: %v", stage, err)
		}
		if !subcategoryIsNull {
			t.Errorf("%s: incident.subcategory_id is set, want NULL", stage)
		}
	}

	walkIncidentLifecycle(t, scoped, id, assertStage)
}

// walkIncidentLifecycle calls assertStage after create (NEW), then writes
// each later stage -- In Progress, Resolved (with the resolution fields),
// Closed -- and calls it again. The writes are plain SQL: see
// TestIncidentCreate_LifecycleWithoutSubcategory for why.
func walkIncidentLifecycle(t *testing.T, scoped *repository.Scoped, id string, assertStage func(t *testing.T, stage, wantState string)) {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	assertStage(t, "after create", "NEW")

	stages := []struct {
		name, state, sql string
	}{
		{"start work", "IN_PROGRESS", `UPDATE incident SET state = 'IN_PROGRESS' WHERE id = $1`},
		{"resolve", "RESOLVED", `UPDATE incident SET state = 'RESOLVED', resolution_code = 'SOLVED_PERMANENTLY',
			close_notes = 'Rolled back the bad gateway config.', resolved_by_id = '` + icEngineerID + `', resolved_on = now() WHERE id = $1`},
		{"close", "CLOSED", `UPDATE incident SET state = 'CLOSED' WHERE id = $1`},
	}
	for _, s := range stages {
		tag, err := scoped.Exec(ctx, s.sql, id)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("%s: updated %d rows, want 1", s.name, tag.RowsAffected())
		}
		assertStage(t, "after "+s.name, s.state)
	}
}

// TestIncidentCreate_ChannelRoundTripsThroughLifecycle: the UI's "Channel"
// is the incident's contactType (column contact_type). Whatever value is
// created must read back as that same value -- what the webapp's option list
// and the API accept -- at every stage from New to Closed. SITE_247 is the
// one value whose enum label differs ('SITE_24_7'); it used to read back as
// "SITE_24_7", matching no option.
func TestIncidentCreate_ChannelRoundTripsThroughLifecycle(t *testing.T) {
	for _, tc := range []struct {
		channel   domain.IncidentContactType
		wantLabel string // incident_contact_type_enum label stored in the column
	}{
		{domain.IncidentContactTypePhone, "PHONE"},
		{domain.IncidentContactTypeSite247, "SITE_24_7"},
	} {
		t.Run(string(tc.channel), func(t *testing.T) {
			pool := incidentCreatePool(t)
			seedIncidentCreateFixture(t, pool)
			scoped := repository.NewScoped(pool)
			repo := repository.NewIncidentRepository(scoped)
			ctx := repository.WithSystemIdentity(context.Background())

			req := icRequest()
			req.Subcategory = nil
			channel := tc.channel
			req.ContactType = &channel
			resp, err := repo.CreateIncident(ctx, req, "HIGH", nil, "jane.doe@test.local")
			if err != nil {
				t.Fatalf("CreateIncident with channel %s: %v", tc.channel, err)
			}
			id := resp.Incident.ID

			walkIncidentLifecycle(t, scoped, id, func(t *testing.T, stage, wantState string) {
				t.Helper()
				view, err := repo.GetIncidentByID(ctx, id)
				if err != nil {
					t.Fatalf("%s: GetIncidentByID: %v", stage, err)
				}
				if view.State == nil || *view.State != wantState {
					t.Errorf("%s: state = %v, want %s", stage, view.State, wantState)
				}
				if view.ContactType == nil || *view.ContactType != string(tc.channel) {
					got := "<nil>"
					if view.ContactType != nil {
						got = *view.ContactType
					}
					t.Errorf("%s: channel (contactType) = %s, want %s", stage, got, tc.channel)
				}
				var stored string
				if err := scoped.QueryRow(ctx, `SELECT contact_type::text FROM incident WHERE id = $1`, id).Scan(&stored); err != nil {
					t.Fatalf("%s: read contact_type: %v", stage, err)
				}
				if stored != tc.wantLabel {
					t.Errorf("%s: incident.contact_type = %s, want %s", stage, stored, tc.wantLabel)
				}
			})
		})
	}
}

func TestIncidentCreate_RollsBackOnBadInput(t *testing.T) {
	pool := incidentCreatePool(t)
	seedIncidentCreateFixture(t, pool)
	scoped := repository.NewScoped(pool)
	repo := repository.NewIncidentRepository(scoped)
	ctx := repository.WithSystemIdentity(context.Background())

	t.Run("unknown watcher", func(t *testing.T) {
		req := icRequest()
		req.WatchList = []string{"nobody@test.local"}
		sub := "Partial Outage"
		_, err := repo.CreateIncident(ctx, req, "HIGH", &sub, "jane.doe@test.local")
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
	})
	t.Run("subcategory from another category", func(t *testing.T) {
		sub := "dns" // NETWORK, not SERVICE_INTERRUPTION
		_, err := repo.CreateIncident(ctx, icRequest(), "HIGH", &sub, "jane.doe@test.local")
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
	})

	var n int
	if err := scoped.QueryRow(ctx, `SELECT count(*) FROM work_item WHERE subject = $1`, icSubject).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d work_item rows left behind by rejected creates, want 0", n)
	}
}
