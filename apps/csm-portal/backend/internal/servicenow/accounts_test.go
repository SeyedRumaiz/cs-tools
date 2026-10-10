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

package servicenow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p", TeamScheduleURL: "https://schedule.example.com/", EscalationTemplateID: "tmpl-1"})
}

func TestGetAccounts_BuildsExpectedQuery(t *testing.T) {
	var capturedQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("sysparm_query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	})

	email := "user@example.com"
	if _, err := c.GetAccounts(context.Background(), &email, nil, nil, 0, 10, false); err != nil {
		t.Fatalf("GetAccounts returned error: %v", err)
	}
	want := "u_owner.email=user@example.com^ORu_renewal_account_manager.email=user@example.com^ORu_technical_owner.email=user@example.com"
	if capturedQuery != want {
		t.Errorf("sysparm_query = %q, want %q", capturedQuery, want)
	}
}

func TestGetAccounts_RejectsUnsafeEmail(t *testing.T) {
	called := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	email := "user@example.com^OR active=true"
	_, err := c.GetAccounts(context.Background(), &email, nil, nil, 0, 10, false)
	if err == nil {
		t.Fatal("expected an error for an unsafe email value, got nil")
	}
	var unsafe *ErrUnsafeQueryValue
	if !errors.As(err, &unsafe) {
		t.Fatalf("expected *ErrUnsafeQueryValue, got %T: %v", err, err)
	}
	if called {
		t.Error("upstream should not have been called for an unsafe query value")
	}
}

func TestGetAccounts_MapsResponseToPortalShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{
					"number": "ACC001", "name": "Acme", "u_region": "APAC", "u_arr_today": "1000",
					"u_owner.name": "Alice", "u_technical_owner.name": "Bob",
					"u_integration_cs_team.sys_id": "cs-team-1",
				},
			},
		})
	})

	result, err := c.GetAccounts(context.Background(), nil, nil, nil, 0, 10, false)
	if err != nil {
		t.Fatalf("GetAccounts returned error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	got := result[0]
	if got.Number != "ACC001" || got.Name != "Acme" || got.AccountManager != "Alice" || got.TechnicalOwner != "Bob" {
		t.Errorf("unexpected account details: %+v", got)
	}
	if got.IntegrationCSTeamScheduleURL != "https://schedule.example.com/cs-team-1" {
		t.Errorf("IntegrationCSTeamScheduleURL = %q, want %q", got.IntegrationCSTeamScheduleURL, "https://schedule.example.com/cs-team-1")
	}
}

func TestGetAccountByID_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	})

	_, err := c.GetAccountByID(context.Background(), "ACC404")
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
}

func TestEscalateCase_LinksExistingActiveEscalation(t *testing.T) {
	var patchCalled bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/customer_account":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"acct-sys-1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/sn_customerservice_case":
			if strings.Contains(r.URL.Query().Get("sysparm_query"), "active_account_escalation") {
				// isCaseEscalated: report no match, so EscalateCase proceeds to link it.
				_, _ = w.Write([]byte(`{"result":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"case-sys-1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/sn_customerservice_escalation":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"esc-sys-1"}]}`))
		case r.Method == http.MethodPatch:
			patchCalled = true
			_, _ = w.Write([]byte(`{"result":{"number":"CS0001","sys_id":"case-sys-1"}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	req := EscalationRequest{Justification: "urgent", RequestSource: "Customer", Reason: "Inactivity", Severity: "High Severity"}
	result, err := c.EscalateCase(context.Background(), "ACC1", "CS0001", req, "agent@example.com")
	if err != nil {
		t.Fatalf("EscalateCase returned error: %v", err)
	}
	if !patchCalled {
		t.Error("expected the case to be PATCHed to link it to the escalation")
	}
	if result.LinkedCaseNumber != "CS0001" || result.SysID != "case-sys-1" {
		t.Errorf("unexpected result: %+v", result)
	}
}

// TestEscalateCase_SerializesConcurrentCallsForSameAccount guards the fix
// for the race where two concurrent EscalateCase calls for the same account
// (a double-click, or two agents escalating together) could both read "no
// active escalation" and each create their own. It runs two concurrent
// EscalateCase calls for the same account against a server that fails the
// test if it ever sees the active-escalations read overlap with another
// in-flight read/create, and asserts only one escalation is ever created.
func TestEscalateCase_SerializesConcurrentCallsForSameAccount(t *testing.T) {
	var mu sync.Mutex
	var inCriticalSection, maxObservedConcurrency, createCalls int
	var escalationExists bool

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/customer_account":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"acct-sys-1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/sn_customerservice_case":
			if strings.Contains(r.URL.Query().Get("sysparm_query"), "active_account_escalation") {
				_, _ = w.Write([]byte(`{"result":[]}`)) // isCaseEscalated: not yet linked
				return
			}
			caseID := r.URL.Query().Get("sysparm_query")
			// Two different cases, one per goroutine -- see caseNumbers below.
			sysID := "case-sys-1"
			if strings.Contains(caseID, "CS0002") {
				sysID = "case-sys-2"
			}
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"` + sysID + `"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/sn_customerservice_escalation":
			mu.Lock()
			inCriticalSection++
			if inCriticalSection > maxObservedConcurrency {
				maxObservedConcurrency = inCriticalSection
			}
			exists := escalationExists
			mu.Unlock()

			time.Sleep(10 * time.Millisecond) // widen the window a race would need to land in

			mu.Lock()
			inCriticalSection--
			mu.Unlock()

			if exists {
				_, _ = w.Write([]byte(`{"result":[{"sys_id":"esc-sys-1"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"result":[]}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/sn_customerservice_escalation_severity":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"sev-sys-1"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/now/table/sn_customerservice_escalation":
			mu.Lock()
			createCalls++
			escalationExists = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{"result":{"sys_id":"esc-sys-1"}}`))
		case r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{"result":{"number":"CS0001","sys_id":"case-sys-1"}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	req := EscalationRequest{Justification: "urgent", RequestSource: "Customer", Reason: "Inactivity", Severity: "High Severity"}
	caseNumbers := []string{"CS0001", "CS0002"}
	var wg sync.WaitGroup
	errs := make([]error, len(caseNumbers))
	for i, caseNumber := range caseNumbers {
		wg.Add(1)
		go func(i int, caseNumber string) {
			defer wg.Done()
			_, errs[i] = c.EscalateCase(context.Background(), "ACC1", caseNumber, req, "agent@example.com")
		}(i, caseNumber)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("EscalateCase(%q) returned error: %v", caseNumbers[i], err)
		}
	}
	if maxObservedConcurrency > 1 {
		t.Errorf("observed %d concurrent calls inside the escalation read -- the per-account lock did not serialize them", maxObservedConcurrency)
	}
	if createCalls != 1 {
		t.Errorf("createNewEscalation called %d times, want exactly 1 (the second call should have linked to the first's escalation)", createCalls)
	}
}

func TestEscalateCase_ConflictWhenAlreadyEscalated(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/now/table/customer_account":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"acct-sys-1"}]}`))
		case r.URL.Path == "/api/now/table/sn_customerservice_escalation":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"esc-sys-1"}]}`))
		case r.URL.Path == "/api/now/table/sn_customerservice_case":
			// Both the case sys_id lookup and the isCaseEscalated check hit
			// this same table; either way, report a match.
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"case-sys-1"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	req := EscalationRequest{Justification: "urgent", RequestSource: "Customer", Reason: "Inactivity", Severity: "High Severity"}
	_, err := c.EscalateCase(context.Background(), "ACC1", "CS0001", req, "agent@example.com")
	if !errors.Is(err, ErrEscalationConflict) {
		t.Fatalf("expected ErrEscalationConflict, got %v", err)
	}
}

// TestEscalateCase_RejectsEmptySysIDFromEscalationCreate guards the fix for
// silently linking a case to no escalation at all when ServiceNow's create
// response is shaped unexpectedly (e.g. {"result":{}}), which decodes to an
// empty sys_id rather than an error.
func TestEscalateCase_RejectsEmptySysIDFromEscalationCreate(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/now/table/customer_account":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"acct-sys-1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/now/table/sn_customerservice_case":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"case-sys-1"}]}`))
		case r.URL.Path == "/api/now/table/sn_customerservice_escalation" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"result":[]}`)) // no active escalation
		case r.URL.Path == "/api/now/table/sn_customerservice_escalation_severity":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"sev-sys-1"}]}`))
		case r.URL.Path == "/api/now/table/sn_customerservice_escalation" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"result":{}}`)) // malformed: no sys_id
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	req := EscalationRequest{Justification: "urgent", RequestSource: "Customer", Reason: "Inactivity", Severity: "High Severity"}
	_, err := c.EscalateCase(context.Background(), "ACC1", "CS0001", req, "agent@example.com")
	if err == nil {
		t.Fatal("expected an error for a missing sys_id, got nil")
	}
}
