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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package salesentity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
)

func TestGetProject_PostsIDSearchAndDecodes(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/projects/search", func(w http.ResponseWriter, r *http.Request) {
			var req idSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if r.Method != http.MethodPost || req.ID != "a0dE200000RgxAbIAJ" || req.Limit != 1 {
				t.Errorf("method=%s request=%+v", r.Method, req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{
				"id": "a0dE200000RgxAbIAJ", "name": "CSM Sync Test", "key": "CSMSYNCTEST", "description": "d",
				"type": "Subscription", "status": "OPEN", "startDate": "2026-04-01", "endDate": "2027-03-31",
				"closureStates": {"endDateBased": "OPEN", "invoiceBased": null, "queryHourBased": null},
				"complianceViolationDate": "2027-01-01", "goLiveDate": "2026-05-01", "customerId": "001E2000025AYH2IAO",
				"createdDate": "2026-09-01T00:00:00.000+0000", "lastModifiedDate": "2026-09-29T09:28:21.000+0000"
			}]`))
		})
	})
	got, err := client.GetProject(context.Background(), "a0dE200000RgxAbIAJ")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if deref(got.Key) != "CSMSYNCTEST" || deref(got.Type) != "Subscription" || deref(got.StartDate) != "2026-04-01" ||
		deref(got.EndDate) != "2027-03-31" || deref(got.ComplianceViolationDate) != "2027-01-01" || deref(got.GoLiveDate) != "2026-05-01" ||
		deref(got.CustomerID) != "001E2000025AYH2IAO" || deref(got.LastModifiedDate) == "" || deref(got.Description) != "d" {
		t.Errorf("project = %+v", got)
	}
}

func TestGetLinkedOpportunity_PostsIDSearchAndDecodes(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/linked-opportunities/search", func(w http.ResponseWriter, r *http.Request) {
			var req idSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if r.Method != http.MethodPost || req.ID != "a3UE2000008btovMAA" || req.Limit != 1 {
				t.Errorf("method=%s request=%+v", r.Method, req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id": "a3UE2000008btovMAA", "name": "LO-26-09-N-00034824", "projectId": "a0dE200000RgyJZIAZ",
				"opportunityId": "006E200000aIb6ZIAS", "lastModifiedDate": "2026-09-29T09:28:21.000+0000"}]`))
		})
	})
	got, err := client.GetLinkedOpportunity(context.Background(), "a3UE2000008btovMAA")
	if err != nil {
		t.Fatalf("GetLinkedOpportunity: %v", err)
	}
	if deref(got.Name) != "LO-26-09-N-00034824" || deref(got.ProjectID) != "a0dE200000RgyJZIAZ" || deref(got.OpportunityID) != "006E200000aIb6ZIAS" || deref(got.LastModifiedDate) == "" {
		t.Errorf("linked opportunity = %+v", got)
	}
}

// TestGetProjectAndLink_EmptyOrMismatchedIs503: an empty or foreign result
// is retryable, because the event can arrive before Salesforce commits.
func TestGetProjectAndLink_EmptyOrMismatchedIs503(t *testing.T) {
	for name, rows := range map[string]string{
		"empty":      `[]`,
		"mismatched": `[{"id":"a0dE200000RgxAbXXX"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			client := newMembershipTestClient(t, func(mux *http.ServeMux) {
				for _, path := range []string{"/projects/search", "/linked-opportunities/search"} {
					mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(rows))
					})
				}
			})
			var sue *apierror.ServiceUnavailableError
			if _, err := client.GetProject(context.Background(), "a0dE200000RgxAbIAJ"); !errors.As(err, &sue) {
				t.Errorf("GetProject err = %v, want ServiceUnavailableError", err)
			}
			if _, err := client.GetLinkedOpportunity(context.Background(), "a0dE200000RgxAbIAJ"); !errors.As(err, &sue) {
				t.Errorf("GetLinkedOpportunity err = %v, want ServiceUnavailableError", err)
			}
		})
	}
}
