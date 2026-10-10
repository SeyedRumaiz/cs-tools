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

package escalation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTokenServer mirrors internal/notifications' own test helper of the same
// name -- a fake OAuth2 token endpoint every oauthhttp-backed client's tests
// point TokenURL at.
func newTokenServer(t *testing.T) *httptest.Server {
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

func TestDetectEscalation_PostsAndDecodesResult(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"isFrustrated":true,"frustratedLevel":0.91,"reason":"Repeated unanswered follow-ups","isEmailTrigger":true}`))
	}))
	defer srv.Close()
	tokenSrv := newTokenServer(t)
	defer tokenSrv.Close()

	c := New(Config{
		BaseURL:      srv.URL,
		TokenURL:     tokenSrv.URL,
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Scopes:       []string{"escalation"},
	})
	result, err := c.DetectEscalation(t.Context(), "CASE-1", "CS0001", "WSO2 API Manager", "This has been open for weeks with no update.")
	if err != nil {
		t.Fatalf("DetectEscalation() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/escalations" {
		t.Errorf("path = %q, want /escalations", gotPath)
	}
	if gotBody["caseId"] != "CASE-1" || gotBody["caseNumber"] != "CS0001" || gotBody["productName"] != "WSO2 API Manager" {
		t.Errorf("request body = %+v, missing expected fields", gotBody)
	}
	if gotBody["comment"] != "This has been open for weeks with no update." {
		t.Errorf("request body comment = %v", gotBody["comment"])
	}

	if !result.IsFrustrated || !result.ShouldAlert || result.FrustratedLevel != 0.91 || result.Reason == "" {
		t.Errorf("result = %+v, unexpected", result)
	}
}

func TestDetectEscalation_NoBaseURL_ReturnsError(t *testing.T) {
	c := New(Config{})
	if _, err := c.DetectEscalation(t.Context(), "CASE-1", "CS0001", "", "a comment"); err == nil {
		t.Fatal("expected an error with no base URL configured")
	}
}

func TestDetectEscalation_EmptyComment_ReturnsError(t *testing.T) {
	c := New(Config{BaseURL: "https://example.test"})
	if _, err := c.DetectEscalation(t.Context(), "CASE-1", "CS0001", "", ""); err == nil {
		t.Fatal("expected an error for an empty comment")
	}
}

func TestDetectEscalation_UpstreamError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()
	tokenSrv := newTokenServer(t)
	defer tokenSrv.Close()

	c := New(Config{BaseURL: srv.URL, TokenURL: tokenSrv.URL, ClientID: "test-client-id", ClientSecret: "test-client-secret"})
	if _, err := c.DetectEscalation(t.Context(), "CASE-1", "CS0001", "", "a comment"); err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
}
