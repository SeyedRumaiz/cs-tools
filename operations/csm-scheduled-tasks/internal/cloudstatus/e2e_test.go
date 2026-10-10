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
	"net/http/httptest"
	"sync"
	"testing"
)

// stubEntityService stands in for entity-service: it serves the sweep and
// pending reads and accepts delivery reports, recording what it was told.
type stubEntityService struct {
	mu       sync.Mutex
	pending  []PendingWebhook
	reports  map[string]deliveryReport
	sweepHit int
}

func newStubEntityService(pending []PendingWebhook) *stubEntityService {
	return &stubEntityService{pending: pending, reports: map[string]deliveryReport{}}
}

func (s *stubEntityService) handler() http.Handler {
	mux := http.NewServeMux()

	// The OAuth2 token endpoint the client-credentials grant calls first.
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	})

	mux.HandleFunc("POST /internal/cloud-status/sweep", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.sweepHit++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SweepResult{Scanned: 3, Recorded: 1})
	})

	mux.HandleFunc("GET /internal/cloud-status/pending", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Only hand out what has not been reported delivered -- the real
		// service's behaviour, and what makes the second tick quiet.
		var out []PendingWebhook
		for _, p := range s.pending {
			if rep, ok := s.reports[p.ID]; ok && rep.Delivered {
				continue
			}
			out = append(out, p)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pendingResponse{Count: len(out), Webhooks: out})
	})

	mux.HandleFunc("POST /internal/cloud-status/{id}/delivery", func(w http.ResponseWriter, r *http.Request) {
		var rep deliveryReport
		_ = json.NewDecoder(r.Body).Decode(&rep)
		s.mu.Lock()
		s.reports[r.PathValue("id")] = rep
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

// TestEndToEnd_SweepPostDeliverReport runs the whole delivery path over real
// HTTP: the OAuth2 grant, the entity-service reads, the outbound webhook, and
// the delivery report -- with only the two endpoints stubbed.
//
// This is the test that stands in for a live run until the real dashboard
// credentials are available. Everything between the task handler and the
// socket is the production code path; only what is on the far end is fake.
func TestEndToEnd_SweepPostDeliverReport(t *testing.T) {
	entity := newStubEntityService([]PendingWebhook{
		{ID: "11111111-1111-1111-1111-111111111111", Number: "OUT0001",
			Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin", Timestamp: "2026-09-28T10:00:00Z"},
		{ID: "22222222-2222-2222-2222-222222222222", Number: "OUT0002",
			Cloud: "asgardeo", Event: "OUTAGE_END", WireEvent: "outage_end", Timestamp: "2026-09-28T11:30:00Z"},
	})
	entitySrv := httptest.NewServer(entity.handler())
	defer entitySrv.Close()

	var seen []capturedRequest
	dash := mockDashboard(t, http.StatusOK, &seen)
	defer dash.Close()

	client, err := NewClient(Config{
		BaseURL:      entitySrv.URL,
		TokenURL:     entitySrv.URL + "/token",
		ClientID:     "id",
		ClientSecret: "secret",
	})
	if err != nil {
		t.Fatalf("construct client: %v", err)
	}
	hook, err := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"choreo": dash.URL, "asgardeo": dash.URL},
		Secrets:  map[string]string{defaultSecretKey: "s3cr3t"},
	})
	if err != nil {
		t.Fatalf("construct webhook: %v", err)
	}

	// ── tick one ────────────────────────────────────────────────────────
	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}

	if entity.sweepHit != 1 {
		t.Errorf("sweep should run once per tick, got %d", entity.sweepHit)
	}
	if len(seen) != 2 {
		t.Fatalf("both webhooks should be posted, got %d", len(seen))
	}
	for _, req := range seen {
		if req.path != "/api/v1/webhook" {
			t.Errorf("path: %q", req.path)
		}
		if req.signature != "s3cr3t" {
			t.Errorf("signature: %q", req.signature)
		}
		if len(req.body) != 3 {
			t.Errorf("body should carry exactly 3 fields, got %v", req.body)
		}
	}
	if seen[0].body["cloud"] != "choreo" || seen[0].body["event"] != "outage_begin" {
		t.Errorf("first webhook: %v", seen[0].body)
	}
	if seen[1].body["cloud"] != "asgardeo" || seen[1].body["event"] != "outage_end" {
		t.Errorf("second webhook: %v", seen[1].body)
	}

	entity.mu.Lock()
	reports := len(entity.reports)
	allDelivered := true
	for _, r := range entity.reports {
		if !r.Delivered {
			allDelivered = false
		}
	}
	entity.mu.Unlock()
	if reports != 2 || !allDelivered {
		t.Errorf("both outcomes should be reported delivered, got %d reports", reports)
	}

	// ── tick two: the steady state ──────────────────────────────────────
	// Nothing is owed any more, so nothing must be posted. This is the
	// property ServiceNow did not have -- there, every update to the outage
	// posted the same event again.
	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("a quiet tick must post nothing; total posts went to %d", len(seen))
	}
}

// TestEndToEnd_DashboardDownIsRecordedAndRetried proves the retry path: a
// failing dashboard is reported as a failure, stays pending, and is posted
// again on the next tick once it recovers.
func TestEndToEnd_DashboardDownIsRecordedAndRetried(t *testing.T) {
	entity := newStubEntityService([]PendingWebhook{
		{ID: "33333333-3333-3333-3333-333333333333", Number: "OUT0003",
			Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin", Timestamp: "2026-09-28T10:00:00Z"},
	})
	entitySrv := httptest.NewServer(entity.handler())
	defer entitySrv.Close()

	// A dashboard whose status we can flip between ticks.
	var mu sync.Mutex
	code := http.StatusServiceUnavailable
	var seen []capturedRequest
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, capturedRequest{path: r.URL.Path})
		w.WriteHeader(code)
	}))
	defer dash.Close()

	client, _ := NewClient(Config{BaseURL: entitySrv.URL, TokenURL: entitySrv.URL + "/token"})
	hook, _ := NewWebhook(WebhookConfig{
		BaseURLs: map[string]string{"choreo": dash.URL},
		Secrets:  map[string]string{defaultSecretKey: "s"},
	})

	// ── tick one: the dashboard is down ─────────────────────────────────
	if err := DeliverDue(client, hook)(context.Background()); err == nil {
		t.Fatal("a failing dashboard must surface as an error")
	}
	entity.mu.Lock()
	rep, ok := entity.reports["33333333-3333-3333-3333-333333333333"]
	entity.mu.Unlock()
	if !ok || rep.Delivered {
		t.Fatalf("the failure must be reported as undelivered, got %+v", rep)
	}
	if rep.Error == "" {
		t.Error("the recorded failure must carry a reason")
	}

	// ── tick two: it recovers ───────────────────────────────────────────
	mu.Lock()
	code = http.StatusOK
	mu.Unlock()

	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	mu.Lock()
	posts := len(seen)
	mu.Unlock()
	if posts != 2 {
		t.Errorf("the webhook must be retried, total posts %d want 2", posts)
	}
	entity.mu.Lock()
	rep = entity.reports["33333333-3333-3333-3333-333333333333"]
	entity.mu.Unlock()
	if !rep.Delivered {
		t.Error("the retry must be reported as delivered")
	}
}
