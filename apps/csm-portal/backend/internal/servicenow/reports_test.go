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

func TestGetSLAReport_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/wso2/case_sla/report" {
			t.Errorf("path = %q, want /api/wso2/case_sla/report", r.URL.Path)
		}
		if got := r.URL.Query().Get("project_id"); got != "proj-1" {
			t.Errorf("project_id = %q, want proj-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"project_name":"Acme","project_key":"ACME","percentileDataList":[{"key":"p50","case_type":"incident","priority":"P1","response_time":"1h","workaround_time":"2h","resolution_time":"3h"}],"caseDataList":[{"case_sys_id":"sid1","case_id":"cid1","case_number":"CS001","case_type":"incident"}]}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.GetSLAReport(context.Background(), "proj-1", "2024-01-01", "2024-01-31")
	if err != nil {
		t.Fatalf("GetSLAReport returned error: %v", err)
	}
	if result.ProjectName != "Acme" || result.ProjectKey != "ACME" {
		t.Errorf("project = %+v, want Acme/ACME", result)
	}
	if len(result.PercentileDataList) != 1 || result.PercentileDataList[0].Key != "p50" {
		t.Errorf("PercentileDataList = %+v", result.PercentileDataList)
	}
	if len(result.CaseDataList) != 1 || result.CaseDataList[0].CaseNumber != "CS001" {
		t.Errorf("CaseDataList = %+v", result.CaseDataList)
	}
}

func TestGetTimeLogBreakdown_AggregatesTimeCards(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/now/table/customer_project":
			_, _ = w.Write([]byte(`{"result":[{"number":"PROJ1","sys_id":"psid1","short_description":"My Project","u_project_key":"MP","u_remaining_query_hours":"10","u_wso2_closure_state":"Open","u_project_type.u_name":"Standard","u_total_query_hour":"20"}]}`))
		case r.URL.Path == "/api/now/table/sn_customerservice_case":
			_, _ = w.Write([]byte(`{"result":[{"number":"CS001","sys_id":"csid1","u_wso2_case_id":"WSO2-1","short_description":"desc","u_case_type.u_name":"Incident","priority":"10","state":"1"}]}`))
		case r.URL.Path == "/api/now/table/time_card":
			_, _ = w.Write([]byte(`{"result":[{"total":"90","u_is_billable":"true","sys_created_on":"t1","sys_created_by":"a","sys_updated_on":"t2","sys_updated_by":"b","state":"Approved"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.GetTimeLogBreakdown(context.Background(), "PROJ1")
	if err != nil {
		t.Fatalf("GetTimeLogBreakdown returned error: %v", err)
	}
	if result.ProjectName != "My Project" || result.ProjectKey != "MP" {
		t.Errorf("project = %+v, want My Project/MP", result)
	}
	if len(result.Cases) != 1 {
		t.Fatalf("Cases = %+v, want 1 entry", result.Cases)
	}
	c1 := result.Cases[0]
	if c1.CaseNumber != "CS001" || c1.Priority != "Critical (P1)" || c1.State != "Open" {
		t.Errorf("case = %+v", c1)
	}
	if c1.TotalHours != "1h 30m" || c1.ConsumedQueryHours != "1h 30m" {
		t.Errorf("TotalHours/ConsumedQueryHours = %q/%q, want 1h 30m/1h 30m", c1.TotalHours, c1.ConsumedQueryHours)
	}
}

func TestGetTimeLogBreakdown_ProjectNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.GetTimeLogBreakdown(context.Background(), "does-not-exist")
	if err != ErrProjectNotFound {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

func TestFormatTime(t *testing.T) {
	tests := []struct {
		minutes int
		want    string
	}{
		{0, "0m"},
		{5, "5m"},
		{60, "1h 0m"},
		{90, "1h 30m"},
		{125, "2h 5m"},
	}
	for _, tt := range tests {
		if got := formatTime(tt.minutes); got != tt.want {
			t.Errorf("formatTime(%d) = %q, want %q", tt.minutes, got, tt.want)
		}
	}
}

func TestGetProjectReportDetails_ParsesNestedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/wso2/cs_report/project-insights" {
			t.Errorf("path = %q, want /api/wso2/cs_report/project-insights", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{
			"subscriptionDetails":{"projectName":"Acme","projectKey":"ACME","projectType":"Standard","accountName":"Acme Corp","startDate":"2024-01-01","endDate":"2024-12-31","supportTier":"Gold","subscription":"sub-1","totalQueryHours":"100","consumedQueryHours":"20"},
			"casesRecords":[{"caseSysId":"sid1","caseNumber":"CS001","engagementType":"query","caseType":"incident","casePriority":"P1","caseState":"Open","opened":"2024-01-02","description":"desc","updated":"2024-01-03","deployment":"prod","productName":"API-M"}],
			"slaDetails":{"slaRecords":[{"task":"t1","slaDefinition":"resolution","businessElapsedPercentage":"50"}],"slaPerformanceStats":{"Workaround":{"fraction":"1/2","percentage":"50"},"Resolution":{"fraction":2,"percentage":"80"},"Response":{"fraction":"3/3","percentage":"100"}}},
			"projectDeployments":[{"name":"prod","products":[{"name":"API-M","version":"4.2","supportStatus":"active","eolDate":"2030-01-01","cores":4,"tps":"100","updateLevelInfo":2,"earliestPossibleSupportEOLDate":"2029-01-01"}]}],
			"monthlyCounts":[{"yearAndMonth":"2024-01","counts":{"incidentCount":3,"queryCount":5}}]
		}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.GetProjectReportDetails(context.Background(), "proj-1", "2024-01-01", "2024-12-31")
	if err != nil {
		t.Fatalf("GetProjectReportDetails returned error: %v", err)
	}
	if result.SubscriptionDetails.ProjectName != "Acme" {
		t.Errorf("SubscriptionDetails = %+v", result.SubscriptionDetails)
	}
	if len(result.CasesRecords) != 1 || result.CasesRecords[0].CaseNumber != "CS001" {
		t.Errorf("CasesRecords = %+v", result.CasesRecords)
	}
	if len(result.ProjectDeployments) != 1 || len(result.ProjectDeployments[0].Products) != 1 {
		t.Fatalf("ProjectDeployments = %+v", result.ProjectDeployments)
	}
	product := result.ProjectDeployments[0].Products[0]
	if product.Cores != "4" {
		t.Errorf("product.Cores = %q, want \"4\" (numeric JSON value converted to string)", product.Cores)
	}
	if len(result.MonthlyCounts) != 1 || result.MonthlyCounts[0].Counts.IncidentCount != 3 {
		t.Errorf("MonthlyCounts = %+v", result.MonthlyCounts)
	}
}
