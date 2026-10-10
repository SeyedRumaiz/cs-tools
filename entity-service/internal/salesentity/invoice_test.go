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

func TestGetInvoice_PostsIDSearchAndDecodes(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/invoices/search", func(w http.ResponseWriter, r *http.Request) {
			var req idSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if r.Method != http.MethodPost || req.ID != "a0IE200000CrbvN" || req.Limit != 1 {
				t.Errorf("method=%s request=%+v", r.Method, req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"a0IE200000CrbvNMAR","name":"2508","opportunityId":"006E200000aIb6ZIAS","amount":1500.25,
				"dueDate":"2026-10-31","paidDate":null,"invoiceDate":"2026-10-01","currencyCode":"USD","classification":"Subscription",
				"description":"CSM Sync Test","serviceStartDate":"2026-04-01","serviceEndDate":"2027-03-31",
				"originalInvoiceDueDate":"2026-10-15","lastModifiedDate":"2026-09-18T06:37:07.000+0000"}]`))
		})
	})
	got, err := client.GetInvoice(context.Background(), "a0IE200000CrbvN")
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	if deref(got.ID) != "a0IE200000CrbvNMAR" || deref(got.OpportunityID) != "006E200000aIb6ZIAS" || got.Amount == nil || *got.Amount != 1500.25 ||
		deref(got.DueDate) != "2026-10-31" || got.PaidDate != nil || deref(got.InvoiceDate) != "2026-10-01" || deref(got.CurrencyCode) != "USD" ||
		deref(got.OriginalInvoiceDueDate) != "2026-10-15" || deref(got.LastModifiedDate) == "" || deref(got.Description) != "CSM Sync Test" {
		t.Errorf("invoice = %+v", got)
	}
}

func TestGetInvoice_EmptyIsRetryable(t *testing.T) {
	client := newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/invoices/search", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[]`))
		})
	})
	_, err := client.GetInvoice(context.Background(), "a0IE200000CrbvNMAR")
	var su *apierror.ServiceUnavailableError
	if !errors.As(err, &su) {
		t.Fatalf("err = %v, want ServiceUnavailableError", err)
	}
}
