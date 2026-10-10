// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"alert-core-service/internal/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIncidentSubject_TagsDelayedCSMCreation(t *testing.T) {
	inc := model.Incident{Service: "checkout-svc", MetricName: "HighCPU", Environment: "Production"}

	if got, want := incidentSubject(inc), "HighCPU"; got != want {
		t.Fatalf("incidentSubject() = %q, want %q", got, want)
	}

	inc.Fallback = true
	if got, want := incidentSubject(inc), "[DELAYED-CSM] HighCPU"; got != want {
		t.Fatalf("incidentSubject() with Fallback = %q, want %q", got, want)
	}
}

func TestIncidentSubject_FallsBackToServiceWhenNoMetricName(t *testing.T) {
	inc := model.Incident{Service: "checkout-svc"}

	if got, want := incidentSubject(inc), "checkout-svc"; got != want {
		t.Fatalf("incidentSubject() = %q, want %q", got, want)
	}
}

func TestFallbackGoogleChatCard_ThreadKeyFollowsThreadedFlag(t *testing.T) {
	inc := model.Incident{Fingerprint: "fp-abc123", IncidentNumber: "PENDING-fp-abc1", Service: "svc", Severity: 1}

	threaded := fallbackGoogleChatCard(inc, true)
	thread, ok := threaded["thread"].(map[string]any)
	if !ok {
		t.Fatalf("expected a thread field when threaded=true, got %#v", threaded)
	}
	if got, want := thread["threadKey"], chatThreadKey(inc); got != want {
		t.Fatalf("threadKey = %v, want %v", got, want)
	}

	unthreaded := fallbackGoogleChatCard(inc, false)
	if _, ok := unthreaded["thread"]; ok {
		t.Fatalf("expected no thread field when threaded=false, got %#v", unthreaded)
	}
}

func TestNotifyChat_ThreadReplyOptionFollowsThreadingFlag(t *testing.T) {
	tests := []struct {
		name          string
		threading     bool
		wantReplyOpt  string
		wantKeyIntact bool
	}{
		{name: "enabled", threading: true, wantReplyOpt: "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD", wantKeyIntact: true},
		{name: "disabled", threading: false, wantReplyOpt: "", wantKeyIntact: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotURL = r.URL.String()
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{}"))
			}))
			defer srv.Close()

			n := &Notifier{
				logger:                  testLogger(),
				client:                  &http.Client{},
				fallbackChatWebhookURLs: []string{srv.URL + "/spaces/AAA/messages?key=k&token=t"},
				maxAttempts:             1,
				retryBaseDelay:          time.Millisecond,
				chatThreadingEnabled:    tt.threading,
			}
			inc := model.Incident{Fingerprint: "fp-xyz", IncidentNumber: "PENDING-fp-xyz", Service: "svc"}
			if ok := n.NotifyChat(context.Background(), inc); !ok {
				t.Fatalf("NotifyChat() = false, want true")
			}

			u, err := url.Parse(gotURL)
			if err != nil {
				t.Fatalf("parse captured url %q: %v", gotURL, err)
			}
			if got := u.Query().Get("messageReplyOption"); got != tt.wantReplyOpt {
				t.Fatalf("messageReplyOption = %q, want %q", got, tt.wantReplyOpt)
			}
			if tt.wantKeyIntact && u.Query().Get("key") != "k" {
				t.Fatalf("expected the webhook's existing key= query param to survive, got %q", gotURL)
			}
		})
	}
}

func TestAnnotationGoogleChatCard_RendersBoldedNoteWithoutHeader(t *testing.T) {
	inc := model.Incident{Fingerprint: "fp-abc123", IncidentNumber: "PENDING-fp-abc1", Service: "svc", Severity: 1}
	note := model.BuildChatDigest(1, 0, "cpu", "vendor")

	card := annotationGoogleChatCard(inc, note, true)
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	body := string(raw)

	if strings.Contains(body, "header") {
		t.Fatalf("expected no header on the Duplicate/OK annotation card, got %s", body)
	}
	if strings.Contains(body, "Priority Incident Reported") {
		t.Fatalf("expected a Duplicate annotation to render distinctly from the original fallback card, got %s", body)
	}
	if !strings.Contains(note, "<b>Duplicate alert received.</b>") {
		t.Fatalf("expected the rendered note to bold the \"Duplicate alert received.\" line, got %s", note)
	}
	if strings.Contains(body, "Alert:") {
		t.Fatalf("expected no Alert: line in the chat annotation body, got %s", body)
	}
	thread, ok := card["thread"].(map[string]any)
	if !ok || thread["threadKey"] != chatThreadKey(inc) {
		t.Fatalf("expected thread.threadKey = %q when threaded=true, got %#v", chatThreadKey(inc), card["thread"])
	}

	okNote := model.BuildChatDigest(0, 1, "cpu", "vendor")
	okCard := annotationGoogleChatCard(inc, okNote, false)
	if !strings.Contains(okNote, "<b>OK alert received.</b>") {
		t.Fatalf("expected the card to visibly label the OK/resolved kind in bold, got %s", okNote)
	}
	if _, ok := okCard["thread"]; ok {
		t.Fatalf("expected no thread field when threaded=false, got %#v", okCard)
	}
}

func TestNotifyChatAnnotation_PostsThreadedCard(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	n := &Notifier{
		logger:                  testLogger(),
		client:                  &http.Client{},
		fallbackChatWebhookURLs: []string{srv.URL + "/spaces/AAA/messages?key=k&token=t"},
		maxAttempts:             1,
		retryBaseDelay:          time.Millisecond,
		chatThreadingEnabled:    true,
	}
	inc := model.Incident{Fingerprint: "fp-xyz", IncidentNumber: "PENDING-fp-xyz", Service: "svc"}
	note := model.BuildWorkNote("OK", "ALT2", "cpu", "vendor")
	if ok := n.NotifyChatAnnotation(context.Background(), inc, note); !ok {
		t.Fatalf("NotifyChatAnnotation() = false, want true")
	}

	var posted map[string]any
	if err := json.Unmarshal(gotBody, &posted); err != nil {
		t.Fatalf("unmarshal posted body: %v", err)
	}
	thread, ok := posted["thread"].(map[string]any)
	if !ok || thread["threadKey"] != chatThreadKey(inc) {
		t.Fatalf("expected posted card's thread.threadKey = %q, got %#v", chatThreadKey(inc), posted["thread"])
	}
}

func TestChatThreadKey_NewIncidentGetsNewThread(t *testing.T) {
	first := model.Incident{Fingerprint: "abc", FirstSeen: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	sameIncident := first
	sameIncident.FirstSeen = first.FirstSeen.Add(time.Microsecond)
	next := first
	next.FirstSeen = first.FirstSeen.Add(5 * time.Minute)

	if chatThreadKey(first) != chatThreadKey(sameIncident) {
		t.Fatalf("thread key must survive Postgres microsecond rounding of first_seen")
	}
	if chatThreadKey(first) == chatThreadKey(next) {
		t.Fatalf("a new incident after the dedup window must get its own thread, both got %q", chatThreadKey(first))
	}
}
