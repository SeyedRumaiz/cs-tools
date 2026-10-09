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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeService is a product AI service: it records the request and replies
// with reply(w).
func fakeService(t *testing.T, reply func(w http.ResponseWriter)) (url string, got *http.Request, body *map[string]string) {
	t.Helper()
	got = &http.Request{}
	body = &map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"cc-token","token_type":"bearer","expires_in":3600}`))
		case "/ask":
			*got = *r.Clone(r.Context())
			_ = json.NewDecoder(r.Body).Decode(body)
			reply(w)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got, body
}

func sse(events ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}
}

func TestHTTP_RelaysAStreamedAnswer(t *testing.T) {
	base, got, body := fakeService(t, sse(
		`{"type":"status","text":"Checking your project"}`,
		`{"type":"token","text":"Use "}`,
		`{"type":"token","text":"Deploy."}`,
		`{"type":"something_new"}`,
		`{"type":"done","text":""}`,
	))
	p := NewHTTP(HTTPConfig{URL: base + "/ask", Token: "fixed"})

	events := collect(t, p, Turn{TenantSlug: "devant", AccountID: "devant.abc", ConversationID: "conv-1", Message: "How?"})

	want := []Event{
		{Type: EventStatus, Text: "Checking your project"},
		{Type: EventToken, Text: "Use "},
		{Type: EventToken, Text: "Deploy."},
		{Type: EventDone, Text: "Use Deploy."},
	}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	if got.Header.Get("Authorization") != "Bearer fixed" {
		t.Errorf("Authorization = %q", got.Header.Get("Authorization"))
	}
	if (*body)["conversationId"] != "conv-1" || (*body)["accountId"] != "devant.abc" ||
		(*body)["tenant"] != "devant" || (*body)["message"] != "How?" {
		t.Errorf("body = %v", *body)
	}
}

func TestHTTP_AcceptsAPlainJSONAnswer(t *testing.T) {
	base, got, _ := fakeService(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"Open **Deploy**."}`))
	})
	p := NewHTTP(HTTPConfig{URL: base + "/ask", TokenURL: base + "/token", ClientID: "c", ClientSecret: "s"})

	events := collect(t, p, Turn{ConversationID: "c", Message: "hi"})
	if len(events) != 2 || events[1] != (Event{Type: EventDone, Text: "Open **Deploy**."}) {
		t.Fatalf("events = %+v", events)
	}
	if got.Header.Get("Authorization") != "Bearer cc-token" {
		t.Errorf("Authorization = %q, want the client credentials token", got.Header.Get("Authorization"))
	}
}

func TestHTTP_PassesOnTheServicesError(t *testing.T) {
	base, _, _ := fakeService(t, sse(`{"type":"token","text":"par"}`, `{"type":"error","text":"Quota used up."}`))
	events := collect(t, NewHTTP(HTTPConfig{URL: base + "/ask"}), Turn{Message: "hi"})
	last := events[len(events)-1]
	if last != (Event{Type: EventError, Text: "Quota used up."}) {
		t.Fatalf("last = %+v", last)
	}
}

func TestHTTP_FailuresEndWithAnError(t *testing.T) {
	cases := map[string]func(http.ResponseWriter){
		"server error": func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) },
		"empty json":   func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{}`)) },
		"empty stream": sse(`{"type":"status","text":"thinking"}`),
	}
	for name, reply := range cases {
		base, _, _ := fakeService(t, reply)
		events := collect(t, NewHTTP(HTTPConfig{URL: base + "/ask"}), Turn{Message: "hi"})
		last := events[len(events)-1]
		if last.Type != EventError || !strings.Contains(last.Text, "not available") {
			t.Errorf("%s: last = %+v", name, last)
		}
	}
}

func TestHTTP_StreamWithoutDoneStillAnswers(t *testing.T) {
	base, _, _ := fakeService(t, sse(`{"type":"token","text":"All "}`, `{"type":"token","text":"set."}`))
	events := collect(t, NewHTTP(HTTPConfig{URL: base + "/ask"}), Turn{Message: "hi"})
	if last := events[len(events)-1]; last != (Event{Type: EventDone, Text: "All set."}) {
		t.Fatalf("last = %+v", last)
	}
}
