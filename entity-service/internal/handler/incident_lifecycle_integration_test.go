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

// Regression guard for "incidents cannot be moved to In Progress": on
// DATA_SOURCE=postgres-servicenow-dual-write, incidentService.UpdateIncident
// rejected every field but workNotes/additionalComments, so the portal's
// state PATCH ({"state":"IN_PROGRESS","assignedEngineerId":...}) came back
// 400 and no incident could leave New. These tests walk an incident through
// its real lifecycle via PATCH /incidents/{id} -- real handler, real
// dual-write service, real Postgres, with the ServiceNow mirror stubbed --
// sending exactly the bodies the portal's incident action bar sends, and
// assert the stored row after every step. Skipped without
// INCIDENT_LIFECYCLE_TEST_DSN, the same convention as the repository
// package's integration tests.
//
//	INCIDENT_LIFECYCLE_TEST_DSN=postgres://... go test ./internal/handler/ -run IncidentLifecycleIntegration

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

const (
	ilCallerID      = "e1000000-0000-0000-0000-000000000001"
	ilEngineerID    = "e1000000-0000-0000-0000-000000000002"
	ilEngineerEmail = "il-engineer@test.local"
	ilServiceID     = "e2000000-0000-0000-0000-000000000001"
	ilUnknownUserID = "e1000000-0000-0000-0000-0000000000ff"
	ilSubject       = "incident-lifecycle integration test"
)

// ilMirror stands in for the ServiceNow-backed IncidentService the
// dual-write service mirrors updates to. Only UpdateIncident is reachable.
type ilMirror struct {
	service.IncidentService
	mu    sync.Mutex
	calls []domain.UpdateIncidentRequest
}

func (m *ilMirror) UpdateIncident(_ context.Context, req domain.UpdateIncidentRequest) (domain.UpdateIncidentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	return domain.UpdateIncidentResponse{}, nil
}

// waitForMirroredState waits for the async ServiceNow mirror to receive a
// call carrying want as its state.
func (m *ilMirror) waitForMirroredState(t *testing.T, want domain.IncidentState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for _, c := range m.calls {
			if c.State != nil && *c.State == want {
				m.mu.Unlock()
				return
			}
		}
		m.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ServiceNow mirror never received state %s", want)
}

type incidentLifecycleEnv struct {
	pool   *pgxpool.Pool
	repo   repository.IncidentRepository
	mirror *ilMirror
	mux    *http.ServeMux
	token  string
}

func newIncidentLifecycleEnv(t *testing.T) *incidentLifecycleEnv {
	t.Helper()
	dsn := os.Getenv("INCIDENT_LIFECYCLE_TEST_DSN")
	if dsn == "" {
		t.Skip("INCIDENT_LIFECYCLE_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	sys := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	cleanup := func() {
		_, _ = scoped.Exec(sys, `DELETE FROM work_item WHERE id IN (
			SELECT it.id FROM incident_task it JOIN work_item wi ON wi.id = it.incident_id WHERE wi.subject = $1)`, ilSubject)
		_, _ = scoped.Exec(sys, `DELETE FROM event_outbox WHERE entity_type = 'incident' AND entity_id IN (
			SELECT id FROM work_item WHERE subject = $1)`, ilSubject)
		_, _ = scoped.Exec(sys, `DELETE FROM work_item WHERE subject = $1`, ilSubject)
		_, _ = pool.Exec(sys, `DELETE FROM service WHERE id = $1`, ilServiceID)
		_, _ = pool.Exec(sys, `DELETE FROM "user" WHERE id = ANY($1::uuid[])`, []string{ilCallerID, ilEngineerID})
	}
	cleanup()
	t.Cleanup(cleanup)

	for _, u := range []struct{ id, name, email string }{
		{ilCallerID, "il-caller", "il-caller@test.local"},
		{ilEngineerID, "il-engineer", ilEngineerEmail},
	} {
		if _, err := pool.Exec(sys, `INSERT INTO "user" (id, created_on, updated_on, user_name, email) VALUES ($1, NOW(), NOW(), $2, $3)`,
			u.id, u.name, u.email); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	if _, err := pool.Exec(sys, `INSERT INTO service (id, created_on, updated_on, created_by, updated_by, name, number)
		VALUES ($1, NOW(), NOW(), 'test', 'test', 'IL Service', 'IL-SVC-1')`, ilServiceID); err != nil {
		t.Fatalf("seed service: %v", err)
	}

	repo := repository.NewIncidentRepository(scoped)
	mirror := &ilMirror{}
	dispatcher := service.NewSNWritebackDispatcher(repository.NewSNWritebackFailureRepository(pool))
	svc := service.NewIncidentServiceWithSNMirror(repo, repository.NewUserRepository(pool), mirror, nil, dispatcher)
	h := NewIncidentHandler(svc)

	mux := http.NewServeMux()
	// System identity stands in for the identity middleware's internal-caller
	// scope (the CSM BFF is an internal caller); UserIDToken is the real
	// middleware that carries the portal user's x-user-id-token.
	mux.Handle("PATCH /incidents/{id}", middleware.UserIDToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.PatchIncident(w, r.WithContext(repository.WithSystemIdentity(r.Context())))
	})))

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"` + ilEngineerEmail + `"}`))
	return &incidentLifecycleEnv{pool: pool, repo: repo, mirror: mirror, mux: mux, token: header + "." + payload + ".sig"}
}

// createIncident creates an unassigned incident with no assignment group
// through the plain portal create, leaving it in its default state, New.
func (e *incidentLifecycleEnv) createIncident(t *testing.T) string {
	t.Helper()
	resp, err := e.repo.CreateIncident(repository.WithSystemIdentity(context.Background()), domain.CreateIncidentRequest{
		Subject:  ilSubject,
		CallerID: ilCallerID,
		Category: domain.IncidentCategoryServiceInterruption,
		// ServiceID only: no assignment group and no assignee, the shape of
		// an alert-generated incident that nobody has claimed yet.
		ServiceID: ilServiceID,
		Impact:    domain.IncidentImpactLow,
		Urgency:   domain.IncidentUrgencyLow,
	}, "LOW", nil, "il-test@test.local")
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	return resp.Incident.ID
}

// patch sends body to PATCH /incidents/{id} the way the CSM BFF forwards it
// and returns the status code and the response's message.
func (e *incidentLifecycleEnv) patch(t *testing.T, id, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/incidents/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-user-id-token", e.token)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var out struct {
		Message  string `json:"message"`
		Incident struct {
			State *string `json:"state"`
		} `json:"incident"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Message
}

// mustPatch sends body and fails the test unless it returns 200.
func (e *incidentLifecycleEnv) mustPatch(t *testing.T, id, body string) {
	t.Helper()
	if code, msg := e.patch(t, id, body); code != http.StatusOK {
		t.Fatalf("PATCH /incidents/%s %s = %d %q, want 200", id, body, code, msg)
	}
}

// incidentLifecycleRow is the stored state the lifecycle steps assert on.
type incidentLifecycleRow struct {
	State, AssignedToID, AssignmentGroupID, ResolutionCode, CloseNotes, ResolvedByID *string
	ResolvedOn                                                                       *time.Time
}

// assertIncidentLifecycleState reads the row back from Postgres and through
// GetIncidentByID (the read the portal's detail page makes), checks the state
// on both, and returns the row for step-specific checks.
func (e *incidentLifecycleEnv) assertIncidentLifecycleState(t *testing.T, id, wantDBState, wantViewState string) incidentLifecycleRow {
	t.Helper()
	sys := repository.WithSystemIdentity(context.Background())
	var row incidentLifecycleRow
	if err := e.pool.QueryRow(sys, `
		SELECT inc.state::text, wi.assigned_to_id::text, wi.assignment_group_id::text,
		       inc.resolution_code::text, inc.close_notes, inc.resolved_by_id::text, inc.resolved_on
		FROM incident inc JOIN work_item wi ON wi.id = inc.id
		WHERE inc.id = $1`, id).Scan(&row.State, &row.AssignedToID, &row.AssignmentGroupID,
		&row.ResolutionCode, &row.CloseNotes, &row.ResolvedByID, &row.ResolvedOn); err != nil {
		t.Fatalf("read back incident: %v", err)
	}
	if row.State == nil || *row.State != wantDBState {
		t.Fatalf("incident.state = %v, want %s", row.State, wantDBState)
	}
	view, err := e.repo.GetIncidentByID(sys, id)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if view.State == nil || *view.State != wantViewState {
		t.Fatalf("GetIncidentByID state = %v, want %s", view.State, wantViewState)
	}
	return row
}

// TestIncidentLifecycleIntegration_FullLifecycle walks one incident through
// New -> In Progress (claimed) -> On Hold -> In Progress -> Resolved ->
// Closed, sending what the portal sends at each step.
func TestIncidentLifecycleIntegration_FullLifecycle(t *testing.T) {
	e := newIncidentLifecycleEnv(t)
	id := e.createIncident(t)

	row := e.assertIncidentLifecycleState(t, id, "NEW", "NEW")
	if row.AssignedToID != nil || row.AssignmentGroupID != nil {
		t.Fatalf("new incident assignee=%s group=%s, want both unset", strOrNil(row.AssignedToID), strOrNil(row.AssignmentGroupID))
	}

	// New -> In Progress. The incident is unassigned, so the portal claims it
	// for the signed-in engineer in the same PATCH. This is the step that
	// used to fail. No assignment group is set, and none is needed: ServiceNow
	// has no gate on this transition either.
	e.mustPatch(t, id, `{"state":"IN_PROGRESS","assignedEngineerId":"`+ilEngineerID+`"}`)
	row = e.assertIncidentLifecycleState(t, id, "IN_PROGRESS", "IN_PROGRESS")
	if strOrNil(row.AssignedToID) != ilEngineerID {
		t.Fatalf("after In Progress assigned_to_id = %s, want %s", strOrNil(row.AssignedToID), ilEngineerID)
	}
	if row.AssignmentGroupID != nil {
		t.Fatalf("after In Progress assignment_group_id = %s, want unset", *row.AssignmentGroupID)
	}
	e.mirror.waitForMirroredState(t, domain.IncidentStateInProgress)

	// In Progress -> On Hold, then back. Already assigned, so a plain state
	// PATCH; the assignee must survive both.
	e.mustPatch(t, id, `{"state":"ON_HOLD"}`)
	row = e.assertIncidentLifecycleState(t, id, "ON_HOLD", "ON_HOLD")
	if strOrNil(row.AssignedToID) != ilEngineerID {
		t.Fatalf("after On Hold assigned_to_id = %s, want %s", strOrNil(row.AssignedToID), ilEngineerID)
	}
	e.mustPatch(t, id, `{"state":"IN_PROGRESS"}`)
	row = e.assertIncidentLifecycleState(t, id, "IN_PROGRESS", "IN_PROGRESS")
	if strOrNil(row.AssignedToID) != ilEngineerID {
		t.Fatalf("after resuming assigned_to_id = %s, want %s", strOrNil(row.AssignedToID), ilEngineerID)
	}

	// In Progress -> Resolved needs a resolution code and notes, as in
	// ServiceNow. Without them it is refused and nothing changes.
	if code, msg := e.patch(t, id, `{"state":"RESOLVED"}`); code != http.StatusBadRequest || !strings.Contains(msg, "resolutionCode and resolutionNotes are required") {
		t.Fatalf("PATCH RESOLVED without resolution = %d %q, want 400 naming resolutionCode/resolutionNotes", code, msg)
	}
	e.assertIncidentLifecycleState(t, id, "IN_PROGRESS", "IN_PROGRESS")

	e.mustPatch(t, id, `{"state":"RESOLVED","resolutionCode":"SOLVED_WORKAROUND","resolutionNotes":"Restarted the gateway."}`)
	row = e.assertIncidentLifecycleState(t, id, "RESOLVED", "RESOLVED")
	if strOrNil(row.ResolutionCode) != "SOLVED_WORK_AROUND" || strOrNil(row.CloseNotes) != "Restarted the gateway." {
		t.Fatalf("after Resolved resolution_code=%s close_notes=%s", strOrNil(row.ResolutionCode), strOrNil(row.CloseNotes))
	}
	if row.ResolvedOn == nil || strOrNil(row.ResolvedByID) != ilEngineerID {
		t.Fatalf("after Resolved resolved_on=%v resolved_by_id=%s, want set to now / %s", row.ResolvedOn, strOrNil(row.ResolvedByID), ilEngineerID)
	}
	resolvedOn := *row.ResolvedOn
	// Read back as the value the portal sent, not the enum's spelling.
	if view, err := e.repo.GetIncidentByID(repository.WithSystemIdentity(context.Background()), id); err != nil || strOrNil(view.ResolutionCode) != "SOLVED_WORKAROUND" {
		t.Fatalf("GetIncidentByID resolutionCode = %s (err %v), want SOLVED_WORKAROUND", strOrNil(view.ResolutionCode), err)
	}
	e.mirror.waitForMirroredState(t, domain.IncidentStateResolved)

	// Resolved -> Closed: the portal's resolution dialog sends the
	// resolution again. resolved_on keeps the moment it was resolved.
	e.mustPatch(t, id, `{"state":"CLOSED","resolutionCode":"SOLVED_WORKAROUND","resolutionNotes":"Restarted the gateway."}`)
	row = e.assertIncidentLifecycleState(t, id, "CLOSED", "CLOSED")
	if row.ResolvedOn == nil || !row.ResolvedOn.Equal(resolvedOn) {
		t.Fatalf("after Closed resolved_on = %v, want unchanged %v", row.ResolvedOn, resolvedOn)
	}
	e.mirror.waitForMirroredState(t, domain.IncidentStateClosed)
}

// TestIncidentLifecycleIntegration_InProgressNeedsNoAssignee moves an
// incident to In Progress with nothing but the state: no assignee, no
// assignment group. ServiceNow accepts this, so CSM must too.
func TestIncidentLifecycleIntegration_InProgressNeedsNoAssignee(t *testing.T) {
	e := newIncidentLifecycleEnv(t)
	id := e.createIncident(t)

	e.mustPatch(t, id, `{"state":"IN_PROGRESS"}`)
	row := e.assertIncidentLifecycleState(t, id, "IN_PROGRESS", "IN_PROGRESS")
	if row.AssignedToID != nil || row.AssignmentGroupID != nil {
		t.Fatalf("assignee=%s group=%s, want both still unset", strOrNil(row.AssignedToID), strOrNil(row.AssignmentGroupID))
	}
}

// TestIncidentLifecycleIntegration_Cancel covers New -> Cancelled, the other
// way out of New, stored as incident_state_enum's 'CANCELED' and read back
// as "CANCELLED", the value the API accepted. The webapp only knows
// "CANCELLED": an incident read back as 'CANCELED' crashed its detail page.
func TestIncidentLifecycleIntegration_Cancel(t *testing.T) {
	e := newIncidentLifecycleEnv(t)
	id := e.createIncident(t)

	e.mustPatch(t, id, `{"state":"CANCELLED"}`)
	e.assertIncidentLifecycleState(t, id, "CANCELED", "CANCELLED")
}

// TestIncidentLifecycleIntegration_UnknownAssigneeIsRejected claims an
// incident for a user that does not exist: a 400 naming the field, and the
// whole PATCH rolled back (still New, still unassigned).
func TestIncidentLifecycleIntegration_UnknownAssigneeIsRejected(t *testing.T) {
	e := newIncidentLifecycleEnv(t)
	id := e.createIncident(t)

	code, msg := e.patch(t, id, `{"state":"IN_PROGRESS","assignedEngineerId":"`+ilUnknownUserID+`"}`)
	if code != http.StatusBadRequest || !strings.Contains(msg, "assignedEngineerId") {
		t.Fatalf("PATCH with unknown assignee = %d %q, want 400 naming assignedEngineerId", code, msg)
	}
	row := e.assertIncidentLifecycleState(t, id, "NEW", "NEW")
	if row.AssignedToID != nil {
		t.Fatalf("assigned_to_id = %s after a rejected PATCH, want unset", *row.AssignedToID)
	}
}
