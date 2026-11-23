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

package salesentity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
)

// orgTestClient serves the token endpoint and one handler at path.
func orgTestClient(t *testing.T, path string, h http.HandlerFunc) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "expires_in": 3600})
	})
	mux.HandleFunc(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL, ClientCredentialsConfig{TokenURL: srv.URL + "/oauth2/token", ClientID: "id", ClientSecret: "secret"})
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	return m
}

func TestFindOrganization(t *testing.T) {
	const orgID = "6b1f0c2e-8f6b-4f0a-9d5c-5b1f2a3c4d5e"
	t.Run("found, searched by the lower-case UUID", func(t *testing.T) {
		c := orgTestClient(t, "/organizations/search", func(w http.ResponseWriter, r *http.Request) {
			if body := decodeBody(t, r); body["organizationId"] != orgID {
				t.Errorf("organizationId = %v, want %s", body["organizationId"], orgID)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "a0X1", "organizationId": orgID, "handle": "acme", "customerId": "001A"}})
		})
		got, ok, err := c.FindOrganization(context.Background(), "6B1F0C2E-8F6B-4F0A-9D5C-5B1F2A3C4D5E")
		if err != nil || !ok || got.ID != "a0X1" || deref(got.CustomerID) != "001A" {
			t.Fatalf("got %+v, %v, %v", got, ok, err)
		}
	})
	t.Run("none", func(t *testing.T) {
		c := orgTestClient(t, "/organizations/search", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("[]"))
		})
		_, ok, err := c.FindOrganization(context.Background(), orgID)
		if err != nil || ok {
			t.Fatalf("got %v, %v; want not found", ok, err)
		}
	})
}

func TestFindCustomer_EmptyIsNotFoundNotARetry(t *testing.T) {
	c := orgTestClient(t, "/customer-search", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	_, ok, err := c.FindCustomer(context.Background(), "001000000000001AAA")
	if err != nil || ok {
		t.Fatalf("got %v, %v; want not found and no error", ok, err)
	}
}

func TestActiveOpportunities_AsksForActiveOnly(t *testing.T) {
	c := orgTestClient(t, "/opportunities/search", func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["customerId"] != "001A" || body["isOnlyActiveOpportunities"] != true || body["limit"] != float64(10) {
			t.Errorf("body = %v", body)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "006A", "customerId": "001A"}})
	})
	got, err := c.ActiveOpportunities(context.Background(), "001A", 10)
	if err != nil || len(got) != 1 || got[0].ID != "006A" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLinkOrganization(t *testing.T) {
	in := LinkOrganizationInput{OrganizationID: "o1", Handle: "acme", Region: "eu", CustomerID: "001A"}
	t.Run("sends the body and the correlation id", func(t *testing.T) {
		c := orgTestClient(t, "/organizations/link", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			if got := r.Header.Get("correlationId"); got != "WCR0000007" {
				t.Errorf("correlationId = %q", got)
			}
			body := decodeBody(t, r)
			if body["organizationId"] != "o1" || body["handle"] != "acme" || body["region"] != "eu" || body["customerId"] != "001A" {
				t.Errorf("body = %v", body)
			}
			if _, ok := body["allowMove"]; ok {
				t.Errorf("allowMove must never be sent")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "a0X1", "organizationId": "o1"})
		})
		got, err := c.LinkOrganization(context.Background(), in, "WCR0000007")
		if err != nil || got.ID != "a0X1" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("a refusal carries its code", func(t *testing.T) {
		c := orgTestClient(t, "/organizations/link", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"under another account","code":"ORG_OTHER_ACCOUNT"}`))
		})
		_, err := c.LinkOrganization(context.Background(), in, "")
		var refused *LinkRefusedError
		if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Code != "ORG_OTHER_ACCOUNT" {
			t.Fatalf("got %v, want a LinkRefusedError with ORG_OTHER_ACCOUNT", err)
		}
	})
	t.Run("a 5xx is unavailable", func(t *testing.T) {
		c := orgTestClient(t, "/organizations/link", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		_, err := c.LinkOrganization(context.Background(), in, "")
		var unavailable *apierror.ServiceUnavailableError
		if !errors.As(err, &unavailable) {
			t.Fatalf("got %v, want a ServiceUnavailableError", err)
		}
	})
}
