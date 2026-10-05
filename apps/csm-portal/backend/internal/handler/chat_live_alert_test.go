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

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/notifications"
)

type liveChatAlert struct {
	product string
	alert   notifications.LiveChatAlert
}

// detail returns the value of the row labeled label, or "" if it is absent.
func (a liveChatAlert) detail(label string) string {
	for _, d := range a.alert.Details {
		if d.Label == label {
			return d.Value
		}
	}
	return ""
}

type fakeLiveChatAlerts struct {
	hasSpace bool
	sent     chan liveChatAlert
}

func (f *fakeLiveChatAlerts) HasSpace(string) bool { return f.hasSpace }

func (f *fakeLiveChatAlerts) SendLiveChatAlert(_ context.Context, product string, alert notifications.LiveChatAlert) error {
	f.sent <- liveChatAlert{product, alert}
	return nil
}

func postEscalation(h *ChatHandler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/internal/chat/escalate", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleEscalate(w, r)
	return w
}

const liveChatEscalateBody = `{"caseId":"c-1","conversationId":"conv-1","projectId":"p","source":"console-chat-bridge","tenantSlug":"devant","customerName":"Jane <b>","message":"help me"}`

func newAlertingHandler(routing *mockRoutingService, hasSpace bool) (*ChatHandler, *fakeLiveChatAlerts) {
	alerts := &fakeLiveChatAlerts{hasSpace: hasSpace, sent: make(chan liveChatAlert, 4)}
	h := newTestChatHandler(routing, &mockChatEventPusher{}).WithLiveChatAlerts(alerts, "https://portal.example/")
	return h, alerts
}

func awaitAlert(t *testing.T, alerts *fakeLiveChatAlerts) liveChatAlert {
	t.Helper()
	select {
	case a := <-alerts.sent:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("no Google Chat alert was sent")
		return liveChatAlert{}
	}
}

func TestHandleEscalate_AssignedChatNamesTheEngineerAndLinksToThePortal(t *testing.T) {
	routing := &mockRoutingService{escalateFn: func(context.Context, routingclient.CaseInfo) (routingclient.EscalateResult, error) {
		return routingclient.EscalateResult{EngineerUserID: "u-1"}, nil
	}}
	h, alerts := newAlertingHandler(routing, true)
	h.engineers.remember("u-1", "eng@example.com")

	assertStatus(t, postEscalation(h, liveChatEscalateBody), http.StatusAccepted)

	a := awaitAlert(t, alerts)
	if a.product != "live-chat" || a.alert.PortalURL != "https://portal.example/chat" || a.alert.Title != "Live chat requested" {
		t.Errorf("unexpected alert envelope: %+v", a)
	}
	for label, want := range map[string]string{
		"Customer":    "Jane &lt;b&gt;",
		"Product":     "devant",
		"Message":     "help me",
		"Assigned to": "<b>eng@example.com</b>",
	} {
		if got := a.detail(label); got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
	if a.alert.MentionEmail != "eng@example.com" {
		t.Errorf("the assigned engineer should be mentioned, got %q", a.alert.MentionEmail)
	}
	if !strings.Contains(a.alert.Note, "Only the assigned engineer can accept") {
		t.Errorf("note should explain that only the assignee can accept: %q", a.alert.Note)
	}
}

func TestHandleEscalate_UnknownEngineerIsStillAnnounced(t *testing.T) {
	routing := &mockRoutingService{escalateFn: func(context.Context, routingclient.CaseInfo) (routingclient.EscalateResult, error) {
		return routingclient.EscalateResult{EngineerUserID: "u-never-seen"}, nil
	}}
	h, alerts := newAlertingHandler(routing, true)

	assertStatus(t, postEscalation(h, liveChatEscalateBody), http.StatusAccepted)

	a := awaitAlert(t, alerts)
	if a.detail("Assigned to") != "<b>an available engineer</b>" {
		t.Errorf("should fall back to a generic name: %+v", a.alert.Details)
	}
	if a.alert.MentionEmail != "" {
		t.Errorf("nobody can be mentioned without a known email, got %q", a.alert.MentionEmail)
	}
}

func TestHandleEscalate_QueuedChatSaysNoOneIsFreeYet(t *testing.T) {
	routing := &mockRoutingService{escalateFn: func(context.Context, routingclient.CaseInfo) (routingclient.EscalateResult, error) {
		return routingclient.EscalateResult{Queued: true, Position: 2}, nil
	}}
	h, alerts := newAlertingHandler(routing, true)

	assertStatus(t, postEscalation(h, liveChatEscalateBody), http.StatusAccepted)

	a := awaitAlert(t, alerts)
	if !strings.Contains(a.detail("Status"), "Waiting in the queue") || a.detail("Assigned to") != "" {
		t.Errorf("queued alert wrong: %+v", a.alert.Details)
	}
}

func TestPublishAssignment_ReassignmentAndQueueDrainAreAnnouncedWithoutTheMessage(t *testing.T) {
	h, alerts := newAlertingHandler(&mockRoutingService{}, true)
	h.rememberEngineer(&middleware.UserInfo{UserID: "u-2", Email: "next@example.com"})
	ci := routingclient.CaseInfo{CaseID: "c-1", CustomerName: "Jane", Message: "help me"}

	h.publishAssignment(context.Background(), "u-2", ci, liveChatReassigned)
	a := awaitAlert(t, alerts)
	if a.alert.MentionEmail != "next@example.com" {
		t.Errorf("the new assignee should be mentioned on reassignment, got %q", a.alert.MentionEmail)
	}
	if a.alert.Title != "Live chat reassigned" || a.detail("Now assigned to") != "<b>next@example.com</b>" || a.detail("Message") != "" {
		t.Errorf("reassignment alert wrong: %+v", a.alert)
	}

	h.publishAssignment(context.Background(), "u-2", ci, liveChatFromQueue)
	if a := awaitAlert(t, alerts); a.alert.Title != "Waiting chat assigned" || a.detail("Assigned to") != "<b>next@example.com</b>" {
		t.Errorf("queue-drain alert wrong: %+v", a.alert)
	}
}

func TestHandleEscalate_NoAlertWithoutConfiguredSpace(t *testing.T) {
	routing := &mockRoutingService{escalateFn: func(context.Context, routingclient.CaseInfo) (routingclient.EscalateResult, error) {
		return routingclient.EscalateResult{EngineerUserID: "u-1"}, nil
	}}
	h, alerts := newAlertingHandler(routing, false)

	assertStatus(t, postEscalation(h, liveChatEscalateBody), http.StatusAccepted)

	select {
	case <-alerts.sent:
		t.Fatal("an alert was sent although no space is configured")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleAcceptSession_PostsAcceptedCardInTheChatsThread(t *testing.T) {
	const caseID = "11111111-1111-1111-1111-111111111111"
	routing := &mockRoutingService{
		acceptFn: func(context.Context, string, string) (routingclient.AcceptResult, error) {
			return routingclient.AcceptResult{Applied: true}, nil
		},
		getCaseInfoFn: func(_ context.Context, id string) (routingclient.CaseInfo, error) {
			return routingclient.CaseInfo{CaseID: id, CustomerName: "Jane", TenantSlug: "devant"}, nil
		},
	}
	h, alerts := newAlertingHandler(routing, true)

	r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+caseID+"/accept", strings.NewReader(`{"conversationId":"conv-1"}`)))
	r.SetPathValue("id", caseID)
	w := httptest.NewRecorder()
	h.HandleAcceptSession(w, r)
	assertStatus(t, w, http.StatusOK)

	a := awaitAlert(t, alerts)
	if a.alert.Title != "Live chat accepted" {
		t.Errorf("title = %q", a.alert.Title)
	}
	if a.alert.ThreadKey != caseID {
		t.Errorf("thread key = %q, want the case id so it lands under the original card", a.alert.ThreadKey)
	}
	if a.alert.MentionEmail != "" {
		t.Errorf("an accepted card must not ping anyone, got %q", a.alert.MentionEmail)
	}
	if a.detail("Accepted by") != "<b>agent@example.com</b>" || a.detail("Customer") != "Jane" || a.detail("Product") != "devant" {
		t.Errorf("rows wrong: %+v", a.alert.Details)
	}
}

func TestHandleAcceptSession_NoCardWhenTheAcceptWasRejected(t *testing.T) {
	const caseID = "11111111-1111-1111-1111-111111111111"
	h, alerts := newAlertingHandler(&mockRoutingService{}, true)

	r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+caseID+"/accept", strings.NewReader(`{"conversationId":"conv-1"}`)))
	r.SetPathValue("id", caseID)
	w := httptest.NewRecorder()
	h.HandleAcceptSession(w, r)
	assertStatus(t, w, http.StatusConflict)

	select {
	case <-alerts.sent:
		t.Fatal("an accepted card was sent although the accept did not apply")
	case <-time.After(200 * time.Millisecond):
	}
}
