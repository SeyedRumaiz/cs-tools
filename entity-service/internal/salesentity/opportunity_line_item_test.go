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

func TestGetOpportunityLineItem_PostsIDSearchAndDecodes(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/opportunity-line-items/search", func(w http.ResponseWriter, r *http.Request) {
			var req idSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if r.Method != http.MethodPost || req.ID != "00kE200000EB5HF" || req.Limit != 1 {
				t.Errorf("method=%s request=%+v", r.Method, req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"00kE200000EB5HFIA1","opportunityId":"006E200000aIb6ZIAS","name":"Line","quantity":3,"totalPrice":1200.5,
				"serviceStartDate":"2026-04-01","serviceEndDate":"2027-03-31","lastModifiedDate":"2026-09-18T06:37:07.000+0000",
				"product":{"id":"01txx","name":"Development Support - 40 hours","productCode":"DS-40"}}]`))
		})
	})
	li, err := client.GetOpportunityLineItem(context.Background(), "00kE200000EB5HF")
	if err != nil {
		t.Fatalf("GetOpportunityLineItem: %v", err)
	}
	if deref(li.ID) != "00kE200000EB5HFIA1" || deref(li.OpportunityID) != "006E200000aIb6ZIAS" || li.Quantity == nil || *li.Quantity != 3 ||
		deref(li.LastModifiedDate) == "" || li.Product == nil || deref(li.Product.ProductCode) != "DS-40" {
		t.Errorf("line item = %+v", li)
	}
}

func TestGetOpportunityLineItem_EmptyIsRetryable(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/opportunity-line-items/search", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[]`))
		})
	})
	_, err := client.GetOpportunityLineItem(context.Background(), "00kE200000EB5HFIA1")
	var su *apierror.ServiceUnavailableError
	if !errors.As(err, &su) {
		t.Fatalf("err = %v, want ServiceUnavailableError", err)
	}
}
