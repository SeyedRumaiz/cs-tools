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
	"net/http"
	"os"
	"testing"
)

// The full chain, with only the dashboard faked.
//
// Everything the other tests cover in halves, joined: a real outage row in the
// dev database, read through entity-service's real handler over real HTTP by
// this package's real client, posted by this package's real webhook poster,
// and reported back. The single stub is the status dashboard itself, which is
// the one thing that must not be hit until its credentials and contract are
// confirmed.
//
// Point it at the entity-service harness in the other module:
//
//	CLOUD_STATUS_LIVE_ENTITY_URL=http://127.0.0.1:9120 \
//	go test ./internal/cloudstatus/ -run TestLiveDelivery -v
//
// No OAuth2: the harness serves the handlers without the auth middleware, so
// the token endpoint is not involved. The grant itself is covered by
// TestEndToEnd_SweepPostDeliverReport.
func TestLiveDelivery(t *testing.T) {
	entityURL := os.Getenv("CLOUD_STATUS_LIVE_ENTITY_URL")
	if entityURL == "" {
		t.Skip("CLOUD_STATUS_LIVE_ENTITY_URL not set; skipping the live delivery run")
	}

	// Against the REAL dashboards when they are configured, a mock otherwise.
	//
	// The real run is opt-in and deliberately so: it posts a genuine event to
	// a status backend outside this estate, and a status page is the one
	// audience that notices a test.
	var seen []capturedRequest
	var cfg WebhookConfig
	realURLs := os.Getenv("CLOUD_STATUS_WEBHOOK_URLS")

	if realURLs != "" {
		if err := json.Unmarshal([]byte(realURLs), &cfg.BaseURLs); err != nil {
			t.Fatalf("CLOUD_STATUS_WEBHOOK_URLS is not valid JSON: %v", err)
		}
		if err := json.Unmarshal([]byte(os.Getenv("CLOUD_STATUS_WEBHOOK_SECRETS")), &cfg.Secrets); err != nil {
			t.Fatalf("CLOUD_STATUS_WEBHOOK_SECRETS is not valid JSON: %v", err)
		}
		t.Logf("REAL RUN: posting to %d configured dashboards", len(cfg.BaseURLs))
		for cloud, u := range cfg.BaseURLs {
			t.Logf("  %-14s -> %s", cloud, u)
		}
	} else {
		dash := mockDashboard(t, http.StatusOK, &seen)
		defer dash.Close()
		cfg = WebhookConfig{
			BaseURLs: map[string]string{
				"devant": dash.URL, "choreo": dash.URL, "asgardeo": dash.URL,
				"bijira": dash.URL, "moesif": dash.URL,
				"choreo-eu": dash.URL, "agent-manager": dash.URL,
			},
			Secrets: map[string]string{defaultSecretKey: "live-run-secret"},
		}
		t.Log("MOCK RUN: set CLOUD_STATUS_WEBHOOK_URLS to post for real")
	}

	client, err := newUnauthenticatedClient(entityURL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	hook, err := NewWebhook(cfg)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}

	// ── tick one: whatever the database currently owes ──────────────────
	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("live tick: %v", err)
	}
	if realURLs != "" {
		// A real dashboard records nothing here; the outcome that matters is
		// whether DeliverDue returned an error, which is checked above.
		t.Log("real post completed without error — check the dashboard and the events table")
		return
	}
	if len(seen) == 0 {
		t.Fatal("nothing was posted; the fixture should have left one webhook pending")
	}
	for i, req := range seen {
		t.Logf("post %d: %s %s  sig=%q  body=%v", i+1, req.method, req.path, req.signature, req.body)
		if req.path != "/api/v1/webhook" {
			t.Errorf("path = %q", req.path)
		}
		if req.signature != "live-run-secret" {
			t.Errorf("signature = %q", req.signature)
		}
		if len(req.body) != 3 {
			t.Errorf("body should have exactly 3 fields, got %v", req.body)
		}
		if req.body["cloud"] == "" || req.body["event"] == "" || req.body["timestamp"] == "" {
			t.Errorf("a field came through empty: %v", req.body)
		}
	}

	// ── tick two: the steady state ──────────────────────────────────────
	before := len(seen)
	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("second live tick: %v", err)
	}
	if len(seen) != before {
		t.Errorf("a delivered webhook was re-sent: %d posts became %d", before, len(seen))
	}
	t.Logf("second tick posted nothing — delivery was recorded in the database")
}

// newUnauthenticatedClient builds a Client with a plain HTTP client, for
// talking to the unauthenticated harness. Test-only: the production path
// always goes through NewClient's client-credentials grant.
func newUnauthenticatedClient(baseURL string) (*Client, error) {
	return &Client{http: &http.Client{}, baseURL: baseURL}, nil
}
