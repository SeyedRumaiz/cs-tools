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

package cloudstatus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// capturedRequest is what the mock dashboard saw.
type capturedRequest struct {
	method    string
	path      string
	signature string
	body      map[string]any
}

// mockDashboard stands in for a WSO2 status dashboard. It records exactly what
// arrived, so these tests assert the WIRE CONTRACT rather than the Go types --
// the receiving service parses JSON off a socket and knows nothing about our
// structs.
func mockDashboard(t *testing.T, status int, seen *[]capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		*seen = append(*seen, capturedRequest{
			method:    r.Method,
			path:      r.URL.Path,
			signature: r.Header.Get("X-Webhook-Signature"),
			body:      body,
		})
		w.WriteHeader(status)
	}))
}

// TestWebhookPost_WireContract pins every byte the dashboard depends on.
//
// This is the test that matters most in the whole port. Everything else can be
// fixed after the fact by re-running a sweep; a malformed webhook is accepted
// with a 200 by a receiver that then ignores it, and nothing anywhere reports
// a problem.
func TestWebhookPost_WireContract(t *testing.T) {
	var seen []capturedRequest
	srv := mockDashboard(t, http.StatusOK, &seen)
	defer srv.Close()

	hook, err := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"choreo": srv.URL},
		Secrets:  map[string]string{defaultSecretKey: "s3cr3t"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := hook.Post(context.Background(), "choreo", "outage_begin", "2026-09-28T10:00:00Z"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one request, got %d", len(seen))
	}
	got := seen[0]

	if got.method != http.MethodPost {
		t.Errorf("method: got %s, want POST", got.method)
	}
	if got.path != "/api/v1/webhook" {
		t.Errorf("path: got %q, want %q", got.path, "/api/v1/webhook")
	}
	// The secret travels VERBATIM, not as an HMAC -- see Post's own comment.
	if got.signature != "s3cr3t" {
		t.Errorf("X-Webhook-Signature: got %q, want the raw secret", got.signature)
	}
	// Exactly three fields. An extra one is a contract change with the
	// dashboard, so this asserts the count, not just the contents.
	if len(got.body) != 3 {
		t.Errorf("body has %d fields, want exactly 3: %v", len(got.body), got.body)
	}
	if got.body["event"] != "outage_begin" {
		t.Errorf("event: got %v", got.body["event"])
	}
	if got.body["cloud"] != "choreo" {
		t.Errorf("cloud: got %v", got.body["cloud"])
	}
	if got.body["timestamp"] != "2026-09-28T10:00:00Z" {
		t.Errorf("timestamp: got %v", got.body["timestamp"])
	}
}

// TestWebhookPost_TimestampKeepsMillisecondPrecision pins the one part of the
// payload that is a format rather than a value.
//
// ServiceNow's body script passes `new Date()` through JSON.stringify, which
// emits ISO-8601 UTC with exactly three decimal places. The port matched it to
// the second at first, which no local test would ever have caught -- a
// receiver with a strict parser rejects the shorter form, and a webhook
// rejected for its format fails exactly as silently as one with a wrong name.
func TestWebhookPost_TimestampKeepsMillisecondPrecision(t *testing.T) {
	var seen []capturedRequest
	srv := mockDashboard(t, http.StatusOK, &seen)
	defer srv.Close()

	hook, _ := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"devant": srv.URL},
		Secrets:  map[string]string{defaultSecretKey: "s"},
	})

	const withMillis = "2026-09-28T07:49:34.725Z"
	if err := hook.Post(context.Background(), "devant", "outage_begin", withMillis); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := seen[0].body["timestamp"].(string)
	if got != withMillis {
		t.Errorf("timestamp = %q, want it passed through untouched as %q", got, withMillis)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`).MatchString(got) {
		t.Errorf("timestamp %q does not match ServiceNow's ISO-8601-with-milliseconds shape", got)
	}
}

// TestWebhookPost_PerCloudSecretOverridesDefault covers the split that
// Defect 2 will need if the dashboards turn out not to share a secret.
func TestWebhookPost_PerCloudSecretOverridesDefault(t *testing.T) {
	var seen []capturedRequest
	srv := mockDashboard(t, http.StatusOK, &seen)
	defer srv.Close()

	hook, _ := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"asgardeo": srv.URL, "choreo": srv.URL},
		Secrets:  map[string]string{defaultSecretKey: "shared", "choreo": "choreo-own"},
	})

	_ = hook.Post(context.Background(), "asgardeo", "outage_begin", "t")
	_ = hook.Post(context.Background(), "choreo", "outage_begin", "t")

	if len(seen) != 2 {
		t.Fatalf("expected two requests, got %d", len(seen))
	}
	if seen[0].signature != "shared" {
		t.Errorf("asgardeo should fall back to the shared secret, got %q", seen[0].signature)
	}
	if seen[1].signature != "choreo-own" {
		t.Errorf("choreo should use its own secret, got %q", seen[1].signature)
	}
}

// TestWebhookPost_NonSuccessIsAnError, and the dashboard's response body is
// deliberately not echoed into the error.
func TestWebhookPost_NonSuccessIsAnError(t *testing.T) {
	var seen []capturedRequest
	srv := mockDashboard(t, http.StatusInternalServerError, &seen)
	defer srv.Close()

	hook, _ := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"choreo": srv.URL},
		Secrets:  map[string]string{defaultSecretKey: "s"},
	})

	err := hook.Post(context.Background(), "choreo", "outage_begin", "t")
	if err == nil {
		t.Fatal("a 500 must be an error")
	}
}

// TestWebhookPost_UnconfiguredCloudDoesNotPost is Defect 1's guard at the
// last possible moment: ServiceNow posted to a URL of "undefined" here.
func TestWebhookPost_UnconfiguredCloudDoesNotPost(t *testing.T) {
	var seen []capturedRequest
	srv := mockDashboard(t, http.StatusOK, &seen)
	defer srv.Close()

	hook, _ := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"choreo": srv.URL},
		Secrets:  map[string]string{defaultSecretKey: "s"},
	})

	if err := hook.Post(context.Background(), "agent-manager", "outage_begin", "t"); err == nil {
		t.Fatal("an unconfigured cloud must error, never post")
	}
	if len(seen) != 0 {
		t.Errorf("nothing must reach any dashboard, got %d requests", len(seen))
	}
}

// TestNewWebhook_RejectsPlainHTTPHost is why URLs are validated at
// construction: a typo should stop startup, not surface mid-incident.
func TestNewWebhook_RejectsPlainHTTPHost(t *testing.T) {
	_, err := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"choreo": "http://status.example.com"},
	})
	if err == nil {
		t.Fatal("a non-loopback plain-http dashboard URL must be refused")
	}
}
