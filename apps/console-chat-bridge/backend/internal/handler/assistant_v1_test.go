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

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/assistant"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/stream"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tokenvalidator"
)

// only gives every tenant the same provider.
func only(p assistant.Provider) func(string) assistant.Provider {
	return func(string) assistant.Provider { return p }
}

// blockingProvider answers only when released.
type blockingProvider struct{ release chan struct{} }

func (b blockingProvider) Answer(ctx context.Context, _ assistant.Turn, emit func(assistant.Event)) {
	<-b.release
	emit(assistant.Event{Type: assistant.EventDone, Text: "ok"})
}

func assistantRequest(t *testing.T, h http.HandlerFunc, method, path, body, subject string, pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	tn := &tenant.Tenant{Validator: fakeValidator{identity: tokenvalidator.Identity{Subject: subject}}}
	tn.Slug = "devant"
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer user-token")
	for k, v := range pathValues {
		r.SetPathValue(k, v)
	}
	r = r.WithContext(tenant.WithTenant(r.Context(), tn))
	w := httptest.NewRecorder()
	middleware.TenantAuth()(h).ServeHTTP(w, r)
	return w
}

func TestAssistant_AnswersOnTheUsersOwnStream(t *testing.T) {
	hub := stream.NewHub()
	h := NewAssistantHandler(only(assistant.Mock{}), hub)

	mine, unsubMine := hub.SubscribeSize(assistantStreamKey("devant", "sub-1", "conv-1"), assistantStreamBuffer)
	defer unsubMine()
	theirs, unsubTheirs := hub.Subscribe(assistantStreamKey("devant", "sub-2", "conv-1"))
	defer unsubTheirs()

	w := assistantRequest(t, h.HandleMessageV1, http.MethodPost, "/v1/devant/assistant/conv-1/messages",
		`{"message":"How do I deploy?"}`, "sub-1", map[string]string{"conversationId": "conv-1"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var last assistant.Event
	deadline := time.After(5 * time.Second)
	for last.Type != assistant.EventDone {
		select {
		case payload := <-mine:
			if err := json.Unmarshal([]byte(payload), &last); err != nil {
				t.Fatalf("decode %q: %v", payload, err)
			}
		case <-deadline:
			t.Fatal("no done event")
		}
	}
	if !strings.Contains(last.Text, "How do I deploy?") {
		t.Errorf("done = %+v", last)
	}
	select {
	case payload := <-theirs:
		t.Fatalf("another user's stream got %q", payload)
	default:
	}
}

func TestAssistant_OneQuestionAtATime(t *testing.T) {
	release := make(chan struct{})
	h := NewAssistantHandler(only(blockingProvider{release: release}), stream.NewHub())
	ask := func() int {
		return assistantRequest(t, h.HandleMessageV1, http.MethodPost, "/v1/devant/assistant/conv-1/messages",
			`{"message":"hi"}`, "sub-1", map[string]string{"conversationId": "conv-1"}).Code
	}
	if code := ask(); code != http.StatusAccepted {
		t.Fatalf("first ask: %d", code)
	}
	if code := ask(); code != http.StatusConflict {
		t.Fatalf("second ask while busy: %d, want 409", code)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for ask() != http.StatusAccepted {
		if time.Now().After(deadline) {
			t.Fatal("still busy after the answer finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAssistant_RejectsBadInput(t *testing.T) {
	h := NewAssistantHandler(only(assistant.Mock{}), stream.NewHub())
	cases := []struct {
		name, conv, body string
	}{
		{"bad conversation id", "../x", `{"message":"hi"}`},
		{"empty message", "conv-1", `{"message":"   "}`},
		{"too long", "conv-1", `{"message":"` + strings.Repeat("a", 4001) + `"}`},
		{"not json", "conv-1", `hi`},
	}
	for _, c := range cases {
		w := assistantRequest(t, h.HandleMessageV1, http.MethodPost, "/v1/devant/assistant/x/messages",
			c.body, "sub-1", map[string]string{"conversationId": c.conv})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", c.name, w.Code)
		}
	}
}

func TestAssistant_OffOrOptedOutIsNotFound(t *testing.T) {
	for name, h := range map[string]*AssistantHandler{
		"no provider": NewAssistantHandler(only(nil), stream.NewHub()),
		"this tenant has none": NewAssistantHandler(func(slug string) assistant.Provider {
			if slug == "devant" {
				return nil
			}
			return assistant.Mock{}
		}, stream.NewHub()),
	} {
		w := assistantRequest(t, h.HandleStatusV1, http.MethodGet, "/v1/devant/assistant", "", "sub-1", nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, w.Code)
		}
	}
	w := assistantRequest(t, NewAssistantHandler(only(assistant.Mock{}), stream.NewHub()).HandleStatusV1,
		http.MethodGet, "/v1/devant/assistant", "", "sub-1", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Errorf("enabled: status %d body %s", w.Code, w.Body.String())
	}
}

func TestAssistant_WaitsForTheStreamBeforeAnswering(t *testing.T) {
	hub := stream.NewHub()
	h := NewAssistantHandler(only(assistant.Mock{}), hub)
	w := assistantRequest(t, h.HandleMessageV1, http.MethodPost, "/v1/devant/assistant/conv-1/messages",
		`{"message":"hi"}`, "sub-1", map[string]string{"conversationId": "conv-1"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}

	// The browser's stream connects a moment after the question.
	time.Sleep(200 * time.Millisecond)
	ch, unsubscribe := hub.SubscribeSize(assistantStreamKey("devant", "sub-1", "conv-1"), assistantStreamBuffer)
	defer unsubscribe()

	var first assistant.Event
	select {
	case payload := <-ch:
		_ = json.Unmarshal([]byte(payload), &first)
	case <-time.After(5 * time.Second):
		t.Fatal("no events")
	}
	if first.Type != assistant.EventStatus {
		t.Fatalf("first event = %+v, want the opening status, not a partial answer", first)
	}
}
