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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

// newOnboardingTestClient wires a CustomerEntityClient to a single httptest
// server that answers both the token endpoint and the API path under test.
func newOnboardingTestClient(t *testing.T, handle http.HandlerFunc) (*CustomerEntityClient, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/onboarding-steps/search", handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := NewCustomerEntityClient(CustomerEntityConfig{
		BaseURL:      srv.URL,
		TokenURL:     srv.URL + "/token",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
	})
	return client, srv
}

func TestSearchOnboardingStepsSendsFiltersAndDecodesPage(t *testing.T) {
	t.Parallel()

	const projectID = "11111111-1111-1111-1111-111111111111"

	var gotMethod string
	var gotBody map[string]any
	client, _ := newOnboardingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"steps": [{
				"id": "22222222-2222-2222-2222-222222222222",
				"membershipSfId": "a0X000000000001AAA",
				"contactSfId": "003000000000001AAA",
				"email": "jane.doe@example.com",
				"projectId": "` + projectID + `",
				"projectContactId": null,
				"step": "EMAIL",
				"status": "FAILED",
				"attemptCount": 3,
				"lastError": "smtp: 550 mailbox unavailable",
				"eventType": "CREATED",
				"eventModifiedOn": "2026-09-01T10:00:00Z",
				"createdOn": "2026-09-01T10:00:01Z",
				"updatedOn": "2026-09-02T10:00:01Z"
			}],
			"total": 1,
			"limit": 50,
			"offset": 0
		}`))
	})

	pid := projectID
	resp, err := client.SearchOnboardingSteps(context.Background(), OnboardingStepSearchRequest{
		Filters:    OnboardingStepFilters{ProjectID: &pid},
		Pagination: OnboardingStepPagination{Limit: OnboardingStepSearchMaxLimit, Offset: 0},
	})
	if err != nil {
		t.Fatalf("SearchOnboardingSteps: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	filters, _ := gotBody["filters"].(map[string]any)
	if filters["projectId"] != projectID {
		t.Errorf("filters.projectId = %v, want %q", filters["projectId"], projectID)
	}
	if _, present := filters["membershipSfIds"]; present {
		t.Errorf("filters.membershipSfIds should be omitted when unset, got %v", filters["membershipSfIds"])
	}
	pagination, _ := gotBody["pagination"].(map[string]any)
	if pagination["limit"] != float64(50) || pagination["offset"] != float64(0) {
		t.Errorf("pagination = %v, want limit 50 offset 0", pagination)
	}

	if resp.Total != 1 || len(resp.Steps) != 1 {
		t.Fatalf("resp = %+v, want one step with total 1", resp)
	}
	step := resp.Steps[0]
	if step.MembershipSfID != "a0X000000000001AAA" || step.Step != "EMAIL" || step.Status != "FAILED" || step.AttemptCount != 3 {
		t.Errorf("step = %+v, want the EMAIL/FAILED row", step)
	}
	if step.LastError == nil || *step.LastError != "smtp: 550 mailbox unavailable" {
		t.Errorf("lastError = %v, want the upstream text", step.LastError)
	}
	if step.ProjectContactID != nil {
		t.Errorf("projectContactId = %v, want nil for a JSON null", *step.ProjectContactID)
	}
	if step.EventModifiedOn.IsZero() || step.UpdatedOn.IsZero() {
		t.Errorf("timestamps were not decoded: %+v", step)
	}
}

func TestSearchOnboardingStepsSurfacesUpstreamStatus(t *testing.T) {
	t.Parallel()

	client, _ := newOnboardingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"onboarding ledger unavailable"}`))
	})

	_, err := client.SearchOnboardingSteps(context.Background(), OnboardingStepSearchRequest{
		Pagination: OnboardingStepPagination{Limit: OnboardingStepSearchMaxLimit},
	})
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *apierror.Error", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestSearchOnboardingStepsRejectsMalformedBody(t *testing.T) {
	t.Parallel()

	client, _ := newOnboardingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"steps": "not-an-array"}`))
	})

	_, err := client.SearchOnboardingSteps(context.Background(), OnboardingStepSearchRequest{
		Pagination: OnboardingStepPagination{Limit: OnboardingStepSearchMaxLimit},
	})
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		t.Errorf("a decode failure must not be reported as an upstream status error: %v", err)
	}
}
