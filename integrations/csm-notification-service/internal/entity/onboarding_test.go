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

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/apierror"
)

// TestRecordOnboardingStep_SendsExpectedRequest pins the wire shape
// entity-service's PUT /onboarding-steps/{membershipSfId}/{step} expects:
// membership and step in the path, everything else in the body, with
// eventModifiedOn serialised as RFC 3339 UTC.
func TestRecordOnboardingStep_SendsExpectedRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "row-1", "attemptCount": 1})
	}))
	defer apiSrv.Close()
	tokenSrv := newCustomerTokenServer(t)
	defer tokenSrv.Close()

	at := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	err := newTestCustomerClient(t, tokenSrv, apiSrv).RecordOnboardingStep(context.Background(), OnboardingStepRequest{
		MembershipSfID:  "a0e000000000001AAA",
		Step:            OnboardingStepIdentity,
		Status:          OnboardingStepFailed,
		LastError:       "upstream returned 500",
		EventType:       "project_contact.invited",
		EventModifiedOn: at,
		Email:           "jane@acme.com",
		ContactSfID:     "003000000000001AAA",
	})
	if err != nil {
		t.Fatalf("RecordOnboardingStep() error = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/onboarding-steps/a0e000000000001AAA/IDENTITY" {
		t.Errorf("request = %s %s, want PUT /onboarding-steps/a0e000000000001AAA/IDENTITY", gotMethod, gotPath)
	}
	want := map[string]any{
		"status":          "FAILED",
		"lastError":       "upstream returned 500",
		"eventType":       "project_contact.invited",
		"eventModifiedOn": "2026-09-22T10:30:00Z",
		"email":           "jane@acme.com",
		"contactSfId":     "003000000000001AAA",
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, gotBody[k], v)
		}
	}
	for _, absent := range []string{"membershipSfId", "step"} {
		if _, present := gotBody[absent]; present {
			t.Errorf("body carries %q, which belongs in the path", absent)
		}
	}
}

// TestRecordOnboardingStep_OmitsEmptyOptionalFields: a SUCCEEDED/SKIPPED
// write has no lastError, and an integration user may have no contactSfId
// — neither should be sent as an empty string.
func TestRecordOnboardingStep_OmitsEmptyOptionalFields(t *testing.T) {
	var gotBody map[string]any
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()
	tokenSrv := newCustomerTokenServer(t)
	defer tokenSrv.Close()

	err := newTestCustomerClient(t, tokenSrv, apiSrv).RecordOnboardingStep(context.Background(), OnboardingStepRequest{
		MembershipSfID:  "a0e000000000001AAA",
		Step:            OnboardingStepEmail,
		Status:          OnboardingStepSkipped,
		EventType:       "project_contact.invited",
		EventModifiedOn: time.Now(),
		Email:           "svc@acme.com",
	})
	if err != nil {
		t.Fatalf("RecordOnboardingStep() error = %v", err)
	}
	for _, absent := range []string{"lastError", "contactSfId"} {
		if _, present := gotBody[absent]; present {
			t.Errorf("body carries empty %q; want it omitted", absent)
		}
	}
}

// TestRecordOnboardingStep_UpstreamError: a non-2xx is surfaced as an
// *apierror.Error (body omitted, like every other call on this client —
// see do()'s doc comment), so dispatch can log the status without PII.
func TestRecordOnboardingStep_UpstreamError(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer apiSrv.Close()
	tokenSrv := newCustomerTokenServer(t)
	defer tokenSrv.Close()

	err := newTestCustomerClient(t, tokenSrv, apiSrv).RecordOnboardingStep(context.Background(), OnboardingStepRequest{
		MembershipSfID: "a0e000000000001AAA", Step: OnboardingStepIdentity, Status: OnboardingStepSucceeded,
		EventType: "project_contact.invited", EventModifiedOn: time.Now(), Email: "jane@acme.com",
	})
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("RecordOnboardingStep() error = %v, want *apierror.Error with status 403", err)
	}
}

// TestRecordOnboardingStep_RequiresPathValues: nothing is sent when the
// membership id or step is missing — there is no row to address.
func TestRecordOnboardingStep_RequiresPathValues(t *testing.T) {
	called := false
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()
	tokenSrv := newCustomerTokenServer(t)
	defer tokenSrv.Close()

	c := newTestCustomerClient(t, tokenSrv, apiSrv)
	if err := c.RecordOnboardingStep(context.Background(), OnboardingStepRequest{Step: OnboardingStepIdentity}); err == nil {
		t.Error("expected an error for a missing membershipSfId")
	}
	if err := c.RecordOnboardingStep(context.Background(), OnboardingStepRequest{MembershipSfID: "a0e1"}); err == nil {
		t.Error("expected an error for a missing step")
	}
	if called {
		t.Error("upstream should not have been called")
	}
}

// TestSucceededEmailStep pins the read side of the ledger: the path it
// calls, that only a SUCCEEDED EMAIL row counts as "already invited" (a
// FAILED or SKIPPED email, or a succeeded step of another kind, must not
// suppress an invitation), and that the row's eventModifiedOn comes back
// verbatim for dispatch to compare -- including when it is missing.
func TestSucceededEmailStep(t *testing.T) {
	cases := map[string]struct {
		steps  []map[string]any
		wantOK bool
		wantOn string
	}{
		"no steps at all": {steps: []map[string]any{}},
		"email succeeded": {
			steps:  []map[string]any{{"step": "IDENTITY", "status": "SUCCEEDED", "eventModifiedOn": "2026-09-01T08:00:00Z"}, {"step": "EMAIL", "status": "SUCCEEDED", "eventModifiedOn": "2026-09-01T08:00:00.123Z"}},
			wantOK: true, wantOn: "2026-09-01T08:00:00.123Z",
		},
		"email succeeded without a timestamp": {steps: []map[string]any{{"step": "EMAIL", "status": "SUCCEEDED"}}, wantOK: true},
		"email failed":                        {steps: []map[string]any{{"step": "EMAIL", "status": "FAILED", "eventModifiedOn": "2026-09-01T08:00:00Z"}}},
		"email skipped":                       {steps: []map[string]any{{"step": "EMAIL", "status": "SKIPPED"}}},
		"another step succeeds":               {steps: []map[string]any{{"step": "IDENTITY", "status": "SUCCEEDED"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var gotMethod, gotPath string
			apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"steps": tc.steps})
			}))
			defer apiSrv.Close()
			tokenSrv := newCustomerTokenServer(t)
			defer tokenSrv.Close()

			got, err := newTestCustomerClient(t, tokenSrv, apiSrv).SucceededEmailStep(context.Background(), "a0e000000000001AAA")
			if err != nil {
				t.Fatalf("SucceededEmailStep() error = %v", err)
			}
			if (got != nil) != tc.wantOK {
				t.Fatalf("SucceededEmailStep() = %+v, want found=%v", got, tc.wantOK)
			}
			if got != nil && (got.Step != OnboardingStepEmail || got.Status != OnboardingStepSucceeded || got.EventModifiedOn != tc.wantOn) {
				t.Errorf("SucceededEmailStep() = %+v, want EMAIL/SUCCEEDED at %q", got, tc.wantOn)
			}
			if gotMethod != http.MethodGet || gotPath != "/onboarding-steps/a0e000000000001AAA" {
				t.Errorf("request = %s %s, want GET /onboarding-steps/a0e000000000001AAA", gotMethod, gotPath)
			}
		})
	}
}

// TestSucceededEmailStep_UpstreamErrorIsReturned: a ledger we cannot read
// is an error, never a quiet "no invitation has been sent".
func TestSucceededEmailStep_UpstreamErrorIsReturned(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer apiSrv.Close()
	tokenSrv := newCustomerTokenServer(t)
	defer tokenSrv.Close()

	got, err := newTestCustomerClient(t, tokenSrv, apiSrv).SucceededEmailStep(context.Background(), "a0e000000000001AAA")
	if err == nil {
		t.Fatal("SucceededEmailStep() error = nil, want the upstream failure surfaced")
	}
	if got != nil {
		t.Error("SucceededEmailStep() returned a step on an error; a failed read must never look like a sent invitation")
	}
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Errorf("error = %v, want *apierror.Error", err)
	}
}

// TestSucceededStep_WelcomeEmail: the Welcome lookup matches only its own step.
func TestSucceededStep_WelcomeEmail(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"steps": []map[string]any{
			{"step": "EMAIL", "status": "SUCCEEDED"}, {"step": "WELCOME_EMAIL", "status": "SUCCEEDED", "eventModifiedOn": "2026-09-01T08:00:00Z"},
		}})
	}))
	defer apiSrv.Close()
	tokenSrv := newCustomerTokenServer(t)
	defer tokenSrv.Close()

	got, err := newTestCustomerClient(t, tokenSrv, apiSrv).SucceededStep(context.Background(), "a0e000000000001AAA", OnboardingStepWelcomeEmail)
	if err != nil || got == nil || got.Step != OnboardingStepWelcomeEmail {
		t.Fatalf("SucceededStep() = %+v, %v, want the WELCOME_EMAIL row", got, err)
	}
}
