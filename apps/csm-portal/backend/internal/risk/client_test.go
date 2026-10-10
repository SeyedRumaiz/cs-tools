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

package risk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
)

func TestSysIDToUUIDRoundTrip(t *testing.T) {
	sysID := "aabbccdd11223344aabbccdd11223344"
	uuid := sysIDToUUID(sysID)
	if uuid != "aabbccdd-1122-3344-aabb-ccdd11223344" {
		t.Fatalf("sysIDToUUID(%q) = %q", sysID, uuid)
	}
	if back := uuidToSysID(uuid); back != sysID {
		t.Fatalf("uuidToSysID(%q) = %q, want %q", uuid, back, sysID)
	}
}

func TestSysIDToUUIDLeavesNonSysIDUnchanged(t *testing.T) {
	// A caller-supplied garbage value (or a value already in UUID shape) must
	// not be silently mangled -- entity-service's own UUID validation should
	// be the one to reject it, with a clear error.
	for _, v := range []string{"not-a-sys-id", "", "11111111-1111-1111-1111-111111111111"} {
		if got := sysIDToUUID(v); got != v {
			t.Errorf("sysIDToUUID(%q) = %q, want unchanged", v, got)
		}
	}
}

func TestEnumCaseConversion(t *testing.T) {
	if got := lowerEnum("AT_RISK"); got != "at_risk" {
		t.Errorf("lowerEnum(AT_RISK) = %q", got)
	}
	if got := upperEnum("at_risk"); got != "AT_RISK" {
		t.Errorf("upperEnum(at_risk) = %q", got)
	}
	v := "resolved"
	if got := upperEnumPtr(&v); got == nil || *got != "RESOLVED" {
		t.Errorf("upperEnumPtr(resolved) = %v", got)
	}
	if got := upperEnumPtr(nil); got != nil {
		t.Errorf("upperEnumPtr(nil) = %v, want nil", got)
	}
}

// newRiskTestClient wires a risk.Client to a single httptest server that
// answers both the OAuth2 token endpoint and whatever customer-health path
// the test registers, mirroring internal/entity's own newOnboardingTestClient
// helper.
func newRiskTestClient(t *testing.T, pattern string, handle http.HandlerFunc) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc(pattern, handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	entityClient := entity.NewCustomerEntityClient(entity.CustomerEntityConfig{
		BaseURL:      srv.URL,
		TokenURL:     srv.URL + "/token",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
	})
	return NewClient(entityClient)
}

func TestOpenProjectRisk_ConvertsSysIDAndEnumCase(t *testing.T) {
	const projectSysID = "aabbccdd11223344aabbccdd11223344"
	var gotPath string
	var gotBody map[string]any
	client := newRiskTestClient(t, "/projects/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "11111111-1111-1111-1111-111111111111",
			"projectId": "aabbccdd-1122-3344-aabb-ccdd11223344",
			"accountId": "99999999-9999-9999-9999-999999999999",
			"status": "OPEN",
			"openedComment": "customer is unresponsive",
			"openedByEmail": "jane@example.com",
			"openedOn": "2026-01-15T10:30:00Z",
			"closedComment": null,
			"closedByEmail": null,
			"closedOn": null,
			"actionItems": []
		}`))
	})

	result, err := client.OpenProjectRisk(context.Background(), projectSysID, "customer is unresponsive")
	if err != nil {
		t.Fatalf("OpenProjectRisk: %v", err)
	}

	wantPath := "/projects/" + sysIDToUUID(projectSysID) + "/risk"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q (sys_id converted to UUID)", gotPath, wantPath)
	}
	if gotBody["comment"] != "customer is unresponsive" {
		t.Errorf("request body comment = %v", gotBody["comment"])
	}
	if result.ProjectSysID != projectSysID {
		t.Errorf("ProjectSysID = %q, want %q (UUID converted back to sys_id)", result.ProjectSysID, projectSysID)
	}
	if result.AccountSysID != "99999999999999999999999999999999" {
		t.Errorf("AccountSysID = %q", result.AccountSysID)
	}
	if result.Status != "open" {
		t.Errorf("Status = %q, want lowercase %q", result.Status, "open")
	}
}

func TestCloseProjectRisk_SurfacesUpstreamConflict(t *testing.T) {
	client := newRiskTestClient(t, "/risks/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"cannot close risk: 1 action item(s) are still open"}`))
	})

	_, err := client.CloseProjectRisk(context.Background(), "11111111-1111-1111-1111-111111111111", "done")
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *apierror.Error", err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
}

func TestCreateActionItem_UppercasesPriorityOnTheWayIn(t *testing.T) {
	var gotBody map[string]any
	client := newRiskTestClient(t, "/risks/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "33333333-3333-3333-3333-333333333333",
			"riskId": "11111111-1111-1111-1111-111111111111",
			"projectId": "99999999-9999-9999-9999-999999999999",
			"accountId": "88888888-8888-8888-8888-888888888888",
			"title": "Follow up",
			"description": null,
			"priority": "HIGH",
			"status": "OPEN",
			"assignedToEmail": null,
			"dueDate": "2026-02-01",
			"resolutionComment": null,
			"resolvedByEmail": null,
			"resolvedOn": null,
			"createdByEmail": "jane@example.com",
			"createdOn": "2026-01-15T10:30:00Z",
			"updatedOn": "2026-01-15T10:30:00Z",
			"commentCount": 0
		}`))
	})

	result, err := client.CreateActionItem(context.Background(), "11111111-1111-1111-1111-111111111111", CreateActionItemRequest{
		Title: "Follow up", Priority: "high", DueDate: "2026-02-01",
	})
	if err != nil {
		t.Fatalf("CreateActionItem: %v", err)
	}
	if gotBody["priority"] != "HIGH" {
		t.Errorf("request body priority = %v, want HIGH (uppercased)", gotBody["priority"])
	}
	if result.Priority != "high" {
		t.Errorf("result Priority = %q, want lowercase %q", result.Priority, "high")
	}
	if result.Status != "open" {
		t.Errorf("result Status = %q, want lowercase %q", result.Status, "open")
	}
}

// Regression: GetAccountsByHealthStatus's own doc comment promises an empty
// list for any health status other than "at_risk"/"healthy" -- but
// entity-service 400s on a value outside its own domain (anything but
// AT_RISK/HEALTHY/TO_BE_REVIEWED), so without filtering before the call, an
// arbitrary/malformed status broke that contract by surfacing an upstream
// validation error instead of the promised empty list.
func TestGetAccountsByHealthStatus_UnsupportedStatusReturnsEmptyWithoutCallingUpstream(t *testing.T) {
	called := false
	client := newRiskTestClient(t, "/accounts/", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid status: BOGUS"}`))
	})

	ids, err := client.GetAccountsByHealthStatus(context.Background(), "bogus")
	if err != nil {
		t.Fatalf("GetAccountsByHealthStatus: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want empty", ids)
	}
	if called {
		t.Error("entity-service was called for an unsupported status; want no round trip")
	}
}
