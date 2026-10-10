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

func partnersTestClient(t *testing.T, body string, check func(customerPartnersRequest)) *Client {
	t.Helper()
	return newMembershipTestClient(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/customer-search", func(w http.ResponseWriter, r *http.Request) {
			var req customerPartnersRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if check != nil {
				check(req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
	})
}

// includePartners must travel with isRealTime: Sales Entity rejects it otherwise.
func TestGetCustomerPartners_PostsRealtimeIncludePartners(t *testing.T) {
	client := partnersTestClient(t, `[{"id":"0012S00002WlTCqQAN","name":"Beta App",
		"partners":[{"id":"001E000001Oa5xEIAR","name":"Smart Software"},{"id":"001E000000AAAAAIAA","name":null}]}]`,
		func(req customerPartnersRequest) {
			if len(req.IDs) != 1 || req.IDs[0] != "0012S00002WlTCq" || !req.IsRealTime || !req.IncludePartners || req.Limit != 1 {
				t.Errorf("request = %+v", req)
			}
		})
	id, partners, err := client.GetCustomerPartners(context.Background(), "0012S00002WlTCq")
	if err != nil {
		t.Fatalf("GetCustomerPartners: %v", err)
	}
	if id != "0012S00002WlTCqQAN" || len(partners) != 2 || partners[0].ID != "001E000001Oa5xEIAR" || deref(partners[0].Name) != "Smart Software" {
		t.Errorf("id = %q partners = %+v", id, partners)
	}
}

func TestGetCustomerPartners_EmptyListIsNoPartners(t *testing.T) {
	client := partnersTestClient(t, `[{"id":"001E2000025AYH2IAO","partners":[]}]`, nil)
	_, partners, err := client.GetCustomerPartners(context.Background(), "001E2000025AYH2IAO")
	if err != nil || partners == nil || len(partners) != 0 {
		t.Fatalf("partners = %#v err = %v, want an empty non-nil list", partners, err)
	}
}

// A build without includePartners omits the key (or sends null); that must
// never read as "no partners", which would delete every stored link.
func TestGetCustomerPartners_MissingPartnersKeyIsRetryable(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `[{"id":"001E2000025AYH2IAO"}]`,
		"null":   `[{"id":"001E2000025AYH2IAO","partners":null}]`,
		"empty":  `[]`,
		"other":  `[{"id":"001E000000ZZZZZIAA","partners":[]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			client := partnersTestClient(t, body, nil)
			_, _, err := client.GetCustomerPartners(context.Background(), "001E2000025AYH2IAO")
			var su *apierror.ServiceUnavailableError
			if !errors.As(err, &su) {
				t.Fatalf("err = %v, want ServiceUnavailableError", err)
			}
		})
	}
}
