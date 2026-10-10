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

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

func newGraphQLTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
}

func newTestSalesClient(t *testing.T, tokenSrv, apiSrv *httptest.Server) *SalesEntityClient {
	t.Helper()
	return NewSalesEntityClient(SalesEntityConfig{
		BaseURL:      apiSrv.URL,
		TokenURL:     tokenSrv.URL,
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})
}

func TestGetContactByEmail_ReturnsContact(t *testing.T) {
	var capturedAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"contacts": []map[string]any{
					{
						"id": "contact-1",
						"account": map[string]any{
							"id": "acct-1", "name": "Acme", "classification": "Enterprise",
						},
						"memberships": []map[string]any{
							{"subscriptionId": "sub-1", "state": "Registered", "type": "OWN CONTACT"},
						},
					},
				},
			},
		})
	}))
	defer apiSrv.Close()
	tokenSrv := newGraphQLTokenServer(t)
	defer tokenSrv.Close()

	c := newTestSalesClient(t, tokenSrv, apiSrv)

	contact, err := c.GetContactByEmail(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("GetContactByEmail returned error: %v", err)
	}
	if contact == nil {
		t.Fatal("expected a contact, got nil")
	}
	if contact.ID != "contact-1" {
		t.Errorf("ID = %q, want %q", contact.ID, "contact-1")
	}
	if len(contact.Memberships) != 1 || contact.Memberships[0].SubscriptionID != "sub-1" {
		t.Errorf("Memberships = %+v, want one membership for sub-1", contact.Memberships)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want %q", capturedAuth, "Bearer test-token")
	}
}

func TestGetContactByEmail_NoMatchReturnsNilNil(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"contacts": []map[string]any{}},
		})
	}))
	defer apiSrv.Close()
	tokenSrv := newGraphQLTokenServer(t)
	defer tokenSrv.Close()

	c := newTestSalesClient(t, tokenSrv, apiSrv)

	contact, err := c.GetContactByEmail(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if contact != nil {
		t.Errorf("expected nil contact, got %+v", contact)
	}
}

func TestGetSubscriptionByKey_MapsGraphQLErrorsToBadGateway(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":   map[string]any{"subscriptions": []map[string]any{}},
			"errors": []map[string]any{{"message": "internal resolver error"}},
		})
	}))
	defer apiSrv.Close()
	tokenSrv := newGraphQLTokenServer(t)
	defer tokenSrv.Close()

	c := newTestSalesClient(t, tokenSrv, apiSrv)

	_, err := c.GetSubscriptionByKey(context.Background(), "key-1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadGateway)
	}
}

func TestGetSubscriptionByKey_MapsHTTPStatusError(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer apiSrv.Close()
	tokenSrv := newGraphQLTokenServer(t)
	defer tokenSrv.Close()

	c := newTestSalesClient(t, tokenSrv, apiSrv)

	_, err := c.GetSubscriptionByKey(context.Background(), "key-1")
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusServiceUnavailable)
	}
}
