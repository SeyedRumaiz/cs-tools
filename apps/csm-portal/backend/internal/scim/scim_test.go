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

package scim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

// testClient builds a Client pointed at srv, bypassing OAuth2 entirely (the
// client-credentials token fetch is irrelevant to what these tests exercise).
func testClient(srv *httptest.Server) *Client {
	return &Client{http: srv.Client(), baseURL: srv.URL}
}

func TestClient_GetRole(t *testing.T) {
	t.Run("hits the internal org's role-by-id path and extracts member emails", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "0bbeea4f-5ada-49ba-8f19-90ae6a116daa",
				"displayName": "timecard-approver-role-stg",
				"users": [
					{"display": "DEFAULT/jane.doe@wso2.com", "value": "70aa5b28-c8ca-4fc7-9da4-6a9ebfaad99e"},
					{"display": "DEFAULT/john.smith@wso2.com", "value": "81bb6c39-d9db-5gd8-ae15-7b0fcbbae0a0"}
				]
			}`))
		}))
		defer srv.Close()

		members, err := testClient(srv).GetRole(context.Background(), "0bbeea4f-5ada-49ba-8f19-90ae6a116daa")
		if err != nil {
			t.Fatalf("GetRole: %v", err)
		}

		if gotMethod != http.MethodGet {
			t.Errorf("method = %q, want GET", gotMethod)
		}
		if gotPath != "/organizations/internal/roles/0bbeea4f-5ada-49ba-8f19-90ae6a116daa" {
			t.Errorf("path = %q, want /organizations/internal/roles/0bbeea4f-5ada-49ba-8f19-90ae6a116daa", gotPath)
		}

		want := []RoleMember{
			{ID: "70aa5b28-c8ca-4fc7-9da4-6a9ebfaad99e", Email: "jane.doe@wso2.com"},
			{ID: "81bb6c39-d9db-5gd8-ae15-7b0fcbbae0a0", Email: "john.smith@wso2.com"},
		}
		if !reflect.DeepEqual(members, want) {
			t.Errorf("members = %+v, want %+v", members, want)
		}
	})

	t.Run("a display with no domain prefix falls back to the raw value", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "r-1", "displayName": "role", "users": [{"display": "jane.doe@wso2.com", "value": "u-1"}]}`))
		}))
		defer srv.Close()

		members, err := testClient(srv).GetRole(context.Background(), "r-1")
		if err != nil {
			t.Fatalf("GetRole: %v", err)
		}
		want := []RoleMember{{ID: "u-1", Email: "jane.doe@wso2.com"}}
		if !reflect.DeepEqual(members, want) {
			t.Errorf("members = %+v, want %+v", members, want)
		}
	})

	t.Run("a role with no users returns an empty, non-nil slice", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "r-1", "displayName": "role", "users": []}`))
		}))
		defer srv.Close()

		members, err := testClient(srv).GetRole(context.Background(), "r-1")
		if err != nil {
			t.Fatalf("GetRole: %v", err)
		}
		if members == nil || len(members) != 0 {
			t.Errorf("members = %+v, want an empty non-nil slice", members)
		}
	})

	t.Run("propagates a 404 as apierror.Error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"role not found"}`))
		}))
		defer srv.Close()

		_, err := testClient(srv).GetRole(context.Background(), "unknown-id")
		apiErr, ok := err.(*apierror.Error)
		if !ok {
			t.Fatalf("err = %v (%T), want *apierror.Error", err, err)
		}
		if apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
		}
	})
}

func TestClient_AddRoleMembers(t *testing.T) {
	t.Run("POSTs the internal org's role-members path with the given emails", func(t *testing.T) {
		var gotMethod, gotPath string
		var gotBody scimAddRoleMembersRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"addedUsers":["jane.doe@wso2.com"],"failedUsers":[],"addedGroups":[],"failedGroups":[]}`))
		}))
		defer srv.Close()

		err := testClient(srv).AddRoleMembers(context.Background(), "11111111-1111-1111-1111-111111111111", []string{"jane.doe@wso2.com"})
		if err != nil {
			t.Fatalf("AddRoleMembers: %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if gotPath != "/organizations/internal/roles/11111111-1111-1111-1111-111111111111/users" {
			t.Errorf("path = %q", gotPath)
		}
		if len(gotBody.Emails) != 1 || gotBody.Emails[0] != "jane.doe@wso2.com" {
			t.Errorf("request body emails = %+v", gotBody.Emails)
		}
	})

	t.Run("an email the SCIM service couldn't resolve is reported as an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"addedUsers":[],"failedUsers":["unknown@wso2.com"],"addedGroups":[],"failedGroups":[]}`))
		}))
		defer srv.Close()

		err := testClient(srv).AddRoleMembers(context.Background(), "r-1", []string{"unknown@wso2.com"})
		if err == nil {
			t.Fatal("expected an error for a failed email, got nil")
		}
		if !strings.Contains(err.Error(), "unknown@wso2.com") {
			t.Errorf("error %q does not name the failed email", err)
		}
	})

	t.Run("propagates a 404 as apierror.Error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"role not found"}`))
		}))
		defer srv.Close()

		err := testClient(srv).AddRoleMembers(context.Background(), "unknown-id", []string{"jane.doe@wso2.com"})
		apiErr, ok := err.(*apierror.Error)
		if !ok {
			t.Fatalf("err = %v (%T), want *apierror.Error", err, err)
		}
		if apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
		}
	})
}
