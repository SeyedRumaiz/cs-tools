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

func TestGetOpportunity_PostsIDSearchAndDecodes(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/opportunities/search", func(w http.ResponseWriter, r *http.Request) {
			var req idSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if r.Method != http.MethodPost || req.ID != "006E200000aIb6ZIAS" || req.Limit != 1 {
				t.Errorf("method=%s request=%+v", r.Method, req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{
				"id": "006E200000aIb6ZIAS", "name": "Opp", "customerId": "001xx", "stageName": "Closed Won", "isWon": true,
				"supportAccountEndDateRollUp": "2027-03-31", "lastModifiedDate": "2026-09-18T06:37:07.000+0000",
				"eulaVersion": "EULA 3.5", "type": "Renewal", "engagementCode": "ENG-1",
				"subscriptionLineItems": [{"id": "00kxx", "opportunityId": "006E200000aIb6ZIAS", "name": "Line", "quantity": 2, "totalPrice": 10.5,
					"serviceStartDate": "2026-04-01", "serviceEndDate": "2027-03-31",
					"product": {"id": "01txx", "name": "Development Support - 40 hours", "family": "Support", "productCode": "DS40", "engProductCode": "E1"}}]
			}]`))
		})
	})
	got, err := client.GetOpportunity(context.Background(), "006E200000aIb6ZIAS")
	if err != nil {
		t.Fatalf("GetOpportunity: %v", err)
	}
	if got.CustomerID != "001xx" || deref(got.StageName) != "Closed Won" || got.IsWon == nil || !*got.IsWon ||
		deref(got.SupportAccountEndDateRollUp) != "2027-03-31" || deref(got.LastModifiedDate) == "" ||
		!got.EulaVersion.Present || deref(got.EulaVersion.Value) != "EULA 3.5" || deref(got.Type) != "Renewal" || deref(got.EngagementCode) != "ENG-1" {
		t.Errorf("opportunity = %+v", got)
	}
	if len(got.SubscriptionLineItems) != 1 {
		t.Fatalf("line items = %d, want 1", len(got.SubscriptionLineItems))
	}
	li := got.SubscriptionLineItems[0]
	if deref(li.ID) != "00kxx" || li.Quantity == nil || *li.Quantity != 2 || li.TotalPrice == nil || *li.TotalPrice != 10.5 ||
		li.Product == nil || deref(li.Product.Name) != "Development Support - 40 hours" || deref(li.Product.EngProductCode) != "E1" {
		t.Errorf("line item = %+v", li)
	}
}

// TestOptionalString_PresenceIsTracked: the ingest keeps the stored EULA
// version only when the key is missing, so absent and null must differ.
func TestOptionalString_PresenceIsTracked(t *testing.T) {
	cases := map[string]struct {
		body        string
		wantPresent bool
		wantValue   *string
	}{
		"absent":  {`{"id":"x"}`, false, nil},
		"null":    {`{"id":"x","eulaVersion":null}`, true, nil},
		"empty":   {`{"id":"x","eulaVersion":""}`, true, strPtr("")},
		"present": {`{"id":"x","eulaVersion":"v3.4"}`, true, strPtr("v3.4")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var o Opportunity
			if err := json.Unmarshal([]byte(tc.body), &o); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if o.EulaVersion.Present != tc.wantPresent || deref(o.EulaVersion.Value) != deref(tc.wantValue) || (o.EulaVersion.Value == nil) != (tc.wantValue == nil) {
				t.Errorf("EulaVersion = %+v, want present=%v value=%v", o.EulaVersion, tc.wantPresent, tc.wantValue)
			}
		})
	}
}

func TestGetOpportunity_EmptyOrMismatchedIs503(t *testing.T) {
	for name, rows := range map[string]string{
		"empty":      `[]`,
		"mismatched": `[{"id":"006E200000aIb6ZXXX"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			client := newMembershipTestClient(t, func(mux *http.ServeMux) {
				mux.HandleFunc("/opportunities/search", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(rows))
				})
			})
			_, err := client.GetOpportunity(context.Background(), "006E200000aIb6ZIAS")
			var sue *apierror.ServiceUnavailableError
			if !errors.As(err, &sue) {
				t.Fatalf("err = %v, want ServiceUnavailableError", err)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
