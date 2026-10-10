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
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetProductList_DedupesTrimsAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/now/table/cmdb_software_product_model" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[{"name":"WSO2 API Manager"},{"name":" WSO2 Identity Server "},{"name":"WSO2 API Manager"},{"name":""}]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	products, err := c.GetProductList(context.Background())
	if err != nil {
		t.Fatalf("GetProductList returned error: %v", err)
	}
	want := []string{"WSO2 API Manager", "WSO2 Identity Server"}
	if len(products) != len(want) {
		t.Fatalf("products = %v, want %v", products, want)
	}
	for i := range want {
		if products[i] != want[i] {
			t.Errorf("products[%d] = %q, want %q", i, products[i], want[i])
		}
	}
}

func TestGetABTTeamList_TrimsAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/now/table/sys_user_group" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[{"name":"Zeta Team"},{"name":"Alpha Team"}]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	teams, err := c.GetABTTeamList(context.Background())
	if err != nil {
		t.Fatalf("GetABTTeamList returned error: %v", err)
	}
	want := []string{"Alpha Team", "Zeta Team"}
	if len(teams) != len(want) || teams[0] != want[0] || teams[1] != want[1] {
		t.Errorf("teams = %v, want %v", teams, want)
	}
}

func TestGetABTTeamMembers_MergesMemberAndRole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/now/table/sys_user_grmember":
			_, _ = w.Write([]byte(`{"result":[{"user.name":"Jane Doe","user.email":"jane@example.com"}]}`))
		case "/api/now/table/u_team_member_role":
			_, _ = w.Write([]byte(`{"result":[{"u_role":"Lead"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			_, _ = w.Write([]byte(`{"result":[]}`))
		}
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	roster, err := c.GetABTTeamMembers(context.Background(), "team-1")
	if err != nil {
		t.Fatalf("GetABTTeamMembers returned error: %v", err)
	}
	if len(roster) != 1 {
		t.Fatalf("roster = %v, want 1 member", roster)
	}
	got := roster[0]
	if got.Name != "Jane Doe" || got.Email != "jane@example.com" || got.Role != "Lead" {
		t.Errorf("roster[0] = %+v, unexpected", got)
	}
}

func TestGetABTTeamMembers_RoleLookupFailureLeavesRoleBlank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/now/table/sys_user_grmember":
			_, _ = w.Write([]byte(`{"result":[{"user.name":"Jane Doe","user.email":"jane@example.com"}]}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	roster, err := c.GetABTTeamMembers(context.Background(), "team-1")
	if err != nil {
		t.Fatalf("GetABTTeamMembers returned error: %v", err)
	}
	if len(roster) != 1 {
		t.Fatalf("roster = %v, want 1 member", roster)
	}
	if roster[0].Role != "" {
		t.Errorf("Role = %q, want blank on a role-lookup failure", roster[0].Role)
	}
}
