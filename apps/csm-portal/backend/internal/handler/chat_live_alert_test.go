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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type liveChatAlert struct{ product, summary, message, url string }

type fakeLiveChatAlerts struct {
	hasSpace bool
	sent     chan liveChatAlert
}

func (f *fakeLiveChatAlerts) HasSpace(string) bool { return f.hasSpace }

func (f *fakeLiveChatAlerts) SendLiveChatAlert(_ context.Context, product, summary, message, url string) error {
	f.sent <- liveChatAlert{product, summary, message, url}
	return nil
}

func postEscalation(h *ChatHandler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/internal/chat/escalate", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleEscalate(w, r)
	return w
}

const liveChatEscalateBody = `{"caseId":"c-1","conversationId":"conv-1","projectId":"p","source":"console-chat-bridge","tenantSlug":"devant","customerName":"Jane <b>","message":"help me"}`

func TestHandleEscalate_SendsGoogleChatAlertWithPortalLink(t *testing.T) {
	alerts := &fakeLiveChatAlerts{hasSpace: true, sent: make(chan liveChatAlert, 1)}
	h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{}).
		WithLiveChatAlerts(alerts, "https://portal.example/")

	assertStatus(t, postEscalation(h, liveChatEscalateBody), http.StatusAccepted)

	select {
	case a := <-alerts.sent:
		if a.product != "live-chat" {
			t.Errorf("product = %q, want live-chat", a.product)
		}
		if a.url != "https://portal.example/chat" {
			t.Errorf("url = %q, want https://portal.example/chat", a.url)
		}
		if !strings.Contains(a.summary, "Jane &lt;b&gt;") || !strings.Contains(a.summary, "devant") {
			t.Errorf("summary should name the (escaped) customer and tenant: %q", a.summary)
		}
		if a.message != "help me" {
			t.Errorf("message = %q", a.message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Google Chat alert was sent")
	}
}

func TestHandleEscalate_NoAlertWithoutConfiguredSpace(t *testing.T) {
	alerts := &fakeLiveChatAlerts{hasSpace: false, sent: make(chan liveChatAlert, 1)}
	h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{}).
		WithLiveChatAlerts(alerts, "https://portal.example")

	assertStatus(t, postEscalation(h, liveChatEscalateBody), http.StatusAccepted)

	select {
	case <-alerts.sent:
		t.Fatal("an alert was sent although no space is configured")
	case <-time.After(200 * time.Millisecond):
	}
}
