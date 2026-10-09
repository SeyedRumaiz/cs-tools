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

package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeNovera serves a client-credentials token endpoint and Novera's /ws,
// replying to the first frame with the given events.
func fakeNovera(t *testing.T, events []string, got *map[string]string, gotSession, gotAuth *string) *Novera {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"bearer","expires_in":3600}`))
		case "/ws":
			*gotSession = r.URL.Query().Get("sessionId")
			*gotAuth = r.Header.Get("Authorization")
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = json.Unmarshal(frame, got)
			for _, e := range events {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(e))
			}
			_, _, _ = conn.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return NewNovera(NoveraConfig{WSBaseURL: srv.URL, TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s"})
}

func collect(t *testing.T, p Provider, turn Turn) []Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []Event
	p.Answer(ctx, turn, func(e Event) { events = append(events, e) })
	return events
}

func TestNovera_StreamsAnAnswer(t *testing.T) {
	var frame map[string]string
	var session, auth string
	n := fakeNovera(t, []string{
		`{"type":"thinking_start"}`,
		`{"type":"thinking_step","step":"reasoning","label":"raw model thoughts"}`,
		`{"type":"thinking_step","step":"tool_activity","label":"Searching the knowledge base"}`,
		`{"type":"token","content":"Hel"}`,
		`{"type":"token","content":"lo."}`,
		`{"type":"final","payload":{"message":"Hello.","sessionId":"s"}}`,
	}, &frame, &session, &auth)

	events := collect(t, n, Turn{AccountID: "devant.abc", ConversationID: "conv-1", Message: "hi"})

	want := []Event{
		{Type: EventStatus, Text: "Searching the knowledge base"},
		{Type: EventToken, Text: "Hel"},
		{Type: EventToken, Text: "lo."},
		{Type: EventDone, Text: "Hello."},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %+v, want %+v", events, want)
		}
	}
	if session != "devant.abc:conv-1" || auth != "Bearer tok" {
		t.Errorf("session=%q auth=%q", session, auth)
	}
	if frame["type"] != "user_message" || frame["accountId"] != "devant.abc" ||
		frame["conversationId"] != "conv-1" || frame["message"] != "hi" {
		t.Errorf("frame = %v", frame)
	}
}

func TestNovera_PassesOnItsErrorMessage(t *testing.T) {
	var frame map[string]string
	var session, auth string
	n := fakeNovera(t, []string{`{"type":"error","message":"Too many messages. Please wait a minute."}`}, &frame, &session, &auth)

	events := collect(t, n, Turn{AccountID: "a", ConversationID: "c", Message: "hi"})
	if len(events) != 1 || events[0] != (Event{Type: EventError, Text: "Too many messages. Please wait a minute."}) {
		t.Fatalf("events = %+v", events)
	}
}

func TestNovera_UnreachableEndsWithAnError(t *testing.T) {
	n := NewNovera(NoveraConfig{WSBaseURL: "http://127.0.0.1:1", TokenURL: "http://127.0.0.1:1/token", ClientID: "c", ClientSecret: "s"})
	events := collect(t, n, Turn{AccountID: "a", ConversationID: "c", Message: "hi"})
	if len(events) != 1 || events[0].Type != EventError || !strings.Contains(events[0].Text, "not available") {
		t.Fatalf("events = %+v", events)
	}
}

func TestMock_EndsWithTheFullReply(t *testing.T) {
	events := collect(t, Mock{}, Turn{Message: "help"})
	last := events[len(events)-1]
	if last.Type != EventDone || !strings.Contains(last.Text, `"help"`) {
		t.Fatalf("last event = %+v", last)
	}
	var streamed strings.Builder
	for _, e := range events {
		if e.Type == EventToken {
			streamed.WriteString(e.Text)
		}
	}
	if streamed.String() != last.Text {
		t.Errorf("streamed %q, done %q", streamed.String(), last.Text)
	}
}

func TestAccountID_IsStablePerUserAndHidesTheSubject(t *testing.T) {
	a := AccountID("devant", "user-1")
	if a != AccountID("devant", "user-1") || a == AccountID("devant", "user-2") || a == AccountID("other", "user-1") {
		t.Fatalf("AccountID not stable and per user/tenant: %s", a)
	}
	if !strings.HasPrefix(a, "devant.") || strings.Contains(a, "user-1") {
		t.Errorf("AccountID = %q", a)
	}
}
