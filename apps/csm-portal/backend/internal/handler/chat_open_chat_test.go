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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
)

func findOpenChat(h *ChatHandler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.HandleFindOpenChat(w, httptest.NewRequest(http.MethodGet, "/internal/chat/open-chat"+query, nil))
	return w
}

func decodeOpenChat(t *testing.T, w *httptest.ResponseRecorder) openChatResponse {
	t.Helper()
	var resp openChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body.String())
	}
	return resp
}

func TestHandleFindOpenChat(t *testing.T) {
	t.Run("requires customerEmail and projectId", func(t *testing.T) {
		h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{})
		assertStatus(t, findOpenChat(h, "?customerEmail=a@example.com"), http.StatusBadRequest)
		assertStatus(t, findOpenChat(h, "?projectId=p"), http.StatusBadRequest)
	})

	t.Run("no open chat returns null", func(t *testing.T) {
		h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{})
		w := findOpenChat(h, "?customerEmail=a@example.com&projectId=p")
		assertStatus(t, w, http.StatusOK)
		if resp := decodeOpenChat(t, w); resp.OpenChat != nil {
			t.Errorf("expected no open chat, got %+v", resp.OpenChat)
		}
	})

	t.Run("returns the chat with the engineer's email when known", func(t *testing.T) {
		var gotEmail, gotProject string
		routing := &mockRoutingService{findOpenChatFn: func(_ context.Context, email, project string) (*routingclient.OpenChat, error) {
			gotEmail, gotProject = email, project
			return &routingclient.OpenChat{CaseID: "c-1", ConversationID: "conv-1", Accepted: true, AssigneeID: "u-1"}, nil
		}}
		h := newTestChatHandler(routing, &mockChatEventPusher{})
		h.engineers.remember("u-1", "eng@example.com")

		w := findOpenChat(h, "?customerEmail=a%40example.com&projectId=p-1")
		assertStatus(t, w, http.StatusOK)
		if gotEmail != "a@example.com" || gotProject != "p-1" {
			t.Errorf("routing called with %q/%q", gotEmail, gotProject)
		}
		want := openChat{CaseID: "c-1", ConversationID: "conv-1", Accepted: true, EngineerEmail: "eng@example.com"}
		if resp := decodeOpenChat(t, w); resp.OpenChat == nil || *resp.OpenChat != want {
			t.Errorf("got %+v, want %+v", resp.OpenChat, want)
		}
	})

	t.Run("routing failure returns 502", func(t *testing.T) {
		routing := &mockRoutingService{findOpenChatFn: func(context.Context, string, string) (*routingclient.OpenChat, error) {
			return nil, errors.New("down")
		}}
		h := newTestChatHandler(routing, &mockChatEventPusher{})
		assertStatus(t, findOpenChat(h, "?customerEmail=a@example.com&projectId=p"), http.StatusBadGateway)
	})
}
