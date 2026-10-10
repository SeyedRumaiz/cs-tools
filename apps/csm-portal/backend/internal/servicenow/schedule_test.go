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

func TestGetABTTeamSchedule_ParsesResponseAndEchoesConfiguredURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/wso2/wso2_team_schedule/schedule" {
			t.Errorf("path = %q, want /api/wso2/wso2_team_schedule/schedule", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("sysparm_team") != "team-1" || q.Get("sysparm_from") != "2024-01-01" {
			t.Errorf("query = %v, want sysparm_team=team-1 sysparm_from=2024-01-01", q)
		}
		w.Header().Set("Content-Type", "application/json")
		// metadata is populated here deliberately — a prior version of
		// GetABTTeamSchedule silently dropped this field (wrongly believed
		// unpopulated by the Ballerina source; it isn't — see
		// ABTTeamScheduleMetadata's doc comment), which broke the SPL
		// frontend's team/event-type filter dropdowns. This fixture exists
		// to keep that regression caught.
		_, _ = w.Write([]byte(`{"result":{"list":[{"label":"Team A","members":[{"name":"Alice","roles":[{"name":"lead","label":"Lead"}],"schedule":{"2024-01-01":[{"name":"oncall","label":"On Call"}]}}]}],"metadata":[{"teams":[{"label":"Team A","id":"team-1","link":"/teams/team-1"}],"eventTypes":[{"name":"oncall","label":"On Call"}]}]}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.GetABTTeamSchedule(context.Background(), "2024-01-01", "7d", "team-1", "oncall", "https://sn.example.com/schedule")
	if err != nil {
		t.Fatalf("GetABTTeamSchedule returned error: %v", err)
	}
	if result.SnURL != "https://sn.example.com/schedule" {
		t.Errorf("SnURL = %q, want the configured URL echoed back", result.SnURL)
	}
	if len(result.List) != 1 || result.List[0].Label != "Team A" {
		t.Fatalf("List = %+v", result.List)
	}
	members := result.List[0].Members
	if len(members) != 1 || members[0].Name != "Alice" {
		t.Fatalf("Members = %+v", members)
	}
	if len(members[0].Roles) != 1 || members[0].Roles[0].Name != "lead" {
		t.Errorf("Roles = %+v", members[0].Roles)
	}
	sched, ok := members[0].Schedule["2024-01-01"]
	if !ok || len(sched) != 1 || sched[0].Name != "oncall" {
		t.Errorf("Schedule = %+v", members[0].Schedule)
	}
	if len(result.Metadata) != 1 {
		t.Fatalf("Metadata = %+v, want 1 entry", result.Metadata)
	}
	meta := result.Metadata[0]
	if len(meta.Teams) != 1 || meta.Teams[0].Label != "Team A" || meta.Teams[0].ID != "team-1" || meta.Teams[0].Link != "/teams/team-1" {
		t.Errorf("Metadata[0].Teams = %+v, want [{Team A team-1 /teams/team-1}]", meta.Teams)
	}
	if len(meta.EventTypes) != 1 || meta.EventTypes[0].Name != "oncall" || meta.EventTypes[0].Label != "On Call" {
		t.Errorf("Metadata[0].EventTypes = %+v, want [{oncall On Call}]", meta.EventTypes)
	}
}

func TestGetABTTeamSchedule_MetadataIsEmptySliceNotNilWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"list":[]}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.GetABTTeamSchedule(context.Background(), "", "", "", "", "https://sn.example.com/schedule")
	if err != nil {
		t.Fatalf("GetABTTeamSchedule returned error: %v", err)
	}
	if result.Metadata == nil {
		t.Error("Metadata should be an empty slice, not nil, matching Ballerina's [] default")
	}
	if len(result.Metadata) != 0 {
		t.Errorf("Metadata = %+v, want empty", result.Metadata)
	}
}
