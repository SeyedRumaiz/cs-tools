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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/stream"
)

// A browser that reconnects (for example after a page reload) loads the
// messages from the case history, so only status events may be replayed
// to it; replaying the last engineer message showed it twice, and a stale
// availability notice would mislead.
func TestHandleChatEvents_ReplaysStatusButNotMessages(t *testing.T) {
	hub := stream.NewHub()
	h := NewChatsHandler(nil, hub)

	post := func(body string) {
		t.Helper()
		w := httptest.NewRecorder()
		h.HandleChatEvents(w, httptest.NewRequest(http.MethodPost, "/internal/chat-events", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s: status %d, body %s", body, w.Code, w.Body.String())
		}
	}
	assigned := `{"caseId":"case-1","type":"engineer_assigned","engineerEmail":"eng@example.com"}`
	post(assigned)
	post(`{"caseId":"case-1","type":"engineer_message","message":"Hi"}`)
	post(`{"caseId":"case-1","type":"engineer_status","status":"away"}`)

	ch, unsubscribe := hub.Subscribe("case-1")
	defer unsubscribe()

	select {
	case got := <-ch:
		if got != assigned {
			t.Fatalf("replayed %q, want the assigned event", got)
		}
	default:
		t.Fatal("expected the assigned event to be replayed")
	}
	select {
	case got := <-ch:
		t.Fatalf("messages and status notices must not be replayed, got %q", got)
	default:
	}
}
