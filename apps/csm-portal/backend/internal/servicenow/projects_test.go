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
	"testing"
)

func TestGetProjects_MapsResponseToPortalShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"number": "PRJ001", "short_description": "Subscription A", "u_project_key": "SUBA", "u_wso2_closure_state": "Open"},
			},
		})
	})

	result, err := c.GetProjects(context.Background(), nil, 0, 10)
	if err != nil {
		t.Fatalf("GetProjects returned error: %v", err)
	}
	if len(result) != 1 || result[0].Number != "PRJ001" || result[0].Name != "Subscription A" || result[0].Key != "SUBA" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestGetProjectByID_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	})

	_, err := c.GetProjectByID(context.Background(), "PRJ404")
	if !errors.Is(err, ErrProjectByIDNotFound) {
		t.Fatalf("expected ErrProjectByIDNotFound, got %v", err)
	}
}

func TestGetProjectContacts_DefaultsMissingStateToDash(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sysparm_query"); got != "customer_project.number=PRJ001" {
			t.Errorf("sysparm_query = %q, want %q", got, "customer_project.number=PRJ001")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"customer_contact.name": "Jane Doe", "u_email": "jane@example.com"},
			},
		})
	})

	result, err := c.GetProjectContacts(context.Background(), "PRJ001", 0, 10)
	if err != nil {
		t.Fatalf("GetProjectContacts returned error: %v", err)
	}
	if len(result) != 1 || result[0].State != "-" {
		t.Errorf("unexpected result: %+v", result)
	}
}
