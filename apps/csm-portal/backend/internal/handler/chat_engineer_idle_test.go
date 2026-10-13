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
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/chatevents"
)

// idleFixture is one accepted chat whose engineer last wrote at start.
type idleFixture struct {
	t         *testing.T
	h         *ChatHandler
	notifier  *mockChatEventPusher
	chat      routingclient.ActiveChat
	start     time.Time
	completed []string
}

func newIdleFixture(t *testing.T) *idleFixture {
	t.Helper()
	f := &idleFixture{t: t, notifier: &mockChatEventPusher{}, start: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)}
	f.chat = routingclient.ActiveChat{
		CaseID: "case-1", ConversationID: "conv-1", AssigneeID: "eng-1", EngineerActiveAt: f.start,
	}
	routing := &mockRoutingService{
		activeChatsFn: func(context.Context) ([]routingclient.ActiveChat, error) {
			if len(f.completed) > 0 {
				return nil, nil
			}
			return []routingclient.ActiveChat{f.chat}, nil
		},
		completedFn: func(_ context.Context, userID, caseID string) (routingclient.CompletedResult, error) {
			f.completed = append(f.completed, userID+"/"+caseID)
			return routingclient.CompletedResult{}, nil
		},
	}
	f.h = newTestChatHandler(routing, f.notifier)
	f.h.engineers.remember("eng-1", "eng@example.com")
	return f
}

// at runs one sweep the given time after the engineer last wrote.
func (f *idleFixture) at(after time.Duration) {
	f.h.sweepEngineerIdleOnce(context.Background(), f.start.Add(after))
}

// openTab registers an engineer alert stream, as an open csm-portal tab does.
func (f *idleFixture) openTab() func() {
	key := engineerHubKey(f.chat.AssigneeID)
	ch := f.h.hub.Register(key)
	return func() { f.h.hub.Unregister(key, ch) }
}

// pushed returns the customer-facing events sent so far, as "type" or
// "type:status" / "type:reason".
func (f *idleFixture) pushed() []string {
	var out []string
	for _, payload := range f.notifier.pushedEvents {
		var evt chatEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			f.t.Fatalf("decode pushed event: %v", err)
		}
		label := evt.Type
		if evt.Status != "" {
			label += ":" + evt.Status
		}
		if evt.Reason != "" {
			label += ":" + evt.Reason
		}
		out = append(out, label)
	}
	return out
}

func (f *idleFixture) expectPushed(want ...string) {
	f.t.Helper()
	got := f.pushed()
	if len(got) != len(want) {
		f.t.Fatalf("pushed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			f.t.Fatalf("pushed %v, want %v", got, want)
		}
	}
}

func TestEngineerIdle_TabClosedWarnsThenEnds(t *testing.T) {
	f := newIdleFixture(t)

	f.at(90 * time.Second) // tab first seen closed
	f.at(2 * time.Minute)  // closed 30s, idle 2m
	f.expectPushed("engineer_status:away")

	f.at(3 * time.Minute)
	f.expectPushed("engineer_status:away")
	if len(f.completed) != 0 {
		t.Fatalf("chat ended too early: %v", f.completed)
	}

	f.at(5 * time.Minute)
	if len(f.completed) != 1 || f.completed[0] != "eng-1/case-1" {
		t.Fatalf("completed = %v, want the engineer's chat ended", f.completed)
	}
	f.expectPushed("engineer_status:away", "engineer_disconnected:inactive")
	var last chatEvent
	_ = json.Unmarshal(f.notifier.pushedEvents[1], &last)
	if last.EngineerEmail != "eng@example.com" || last.ConversationID != "conv-1" {
		t.Errorf("end event = %+v, want the engineer's email and the conversation", last)
	}
}

func TestEngineerIdle_OpenTabIsNeverEnded(t *testing.T) {
	f := newIdleFixture(t)
	defer f.openTab()()

	f.at(4 * time.Minute)
	f.expectPushed()
	f.at(5 * time.Minute)
	f.at(6 * time.Minute)
	f.expectPushed("engineer_status:busy")

	f.at(2 * time.Hour)
	if len(f.completed) != 0 {
		t.Fatalf("a chat whose engineer has the portal open must not be ended: %v", f.completed)
	}
	f.expectPushed("engineer_status:busy")
}

func TestEngineerIdle_ReloadWithinGraceIsNotAway(t *testing.T) {
	f := newIdleFixture(t)

	f.at(3 * time.Minute) // closed for this one sweep, as during a reload
	close := f.openTab()
	defer close()
	f.at(3*time.Minute + 15*time.Second)
	f.expectPushed()
}

func TestEngineerIdle_ComingBackIsAnnounced(t *testing.T) {
	f := newIdleFixture(t)

	f.at(2 * time.Minute)
	f.at(3 * time.Minute)
	f.expectPushed("engineer_status:away")

	defer f.openTab()()
	f.at(4 * time.Minute)
	f.expectPushed("engineer_status:away", "engineer_status:back")

	// Still quiet past the busy limit: the customer hears it once.
	f.at(6 * time.Minute)
	f.at(10 * time.Minute)
	f.expectPushed("engineer_status:away", "engineer_status:back", "engineer_status:busy")
	if len(f.completed) != 0 {
		t.Fatalf("chat ended after the engineer came back: %v", f.completed)
	}
}

func TestEngineerIdle_ComingBackLateSkipsTheBusyNotice(t *testing.T) {
	f := newIdleFixture(t)
	f.h.idlePolicy.AwayEndAfter = time.Hour

	f.at(2 * time.Minute)
	f.at(3 * time.Minute)
	defer f.openTab()()
	f.at(6 * time.Minute) // back after the busy limit
	f.at(10 * time.Minute)
	f.expectPushed("engineer_status:away", "engineer_status:back")
}

func TestEngineerIdle_AMessageStartsOver(t *testing.T) {
	f := newIdleFixture(t)

	f.at(2 * time.Minute)
	f.at(3 * time.Minute)
	f.expectPushed("engineer_status:away")

	// The engineer writes from another device; the clock starts again.
	f.chat.EngineerActiveAt = f.start.Add(4 * time.Minute)
	f.at(5 * time.Minute)
	if len(f.completed) != 0 {
		t.Fatalf("a fresh engineer message must reset the limit: %v", f.completed)
	}
	f.expectPushed("engineer_status:away")

	f.at(6*time.Minute + 30*time.Second)
	f.expectPushed("engineer_status:away", "engineer_status:away")
}

// withWarningEmails turns on the away emails for the fixture's handler.
func (f *idleFixture) withWarningEmails() *fakeChatEventPublisher {
	pub := newFakeChatEventPublisher()
	f.h.WithAwayEngineerEmails(pub, "https://csm.example")
	return pub
}

func TestEngineerIdle_AwayWarningEmailsTheEngineerOnce(t *testing.T) {
	f := newIdleFixture(t)
	f.h.routing.(*mockRoutingService).getCaseInfoFn = func(_ context.Context, caseID string) (routingclient.CaseInfo, error) {
		return routingclient.CaseInfo{CaseID: caseID, CustomerEmail: "jane@example.com", TenantSlug: "devant", Subject: "Deploy fails"}, nil
	}
	pub := f.withWarningEmails()

	f.at(90 * time.Second)
	f.at(2 * time.Minute)
	pub.wait(t, 1)
	f.at(3 * time.Minute)
	f.at(4 * time.Minute)
	if pub.count() != 1 {
		t.Fatalf("published %d events, want one warning per quiet spell", pub.count())
	}
	if pub.types[0] != chatevents.TypeEngineerAwayWarning || pub.keys[0] != "case-1" {
		t.Errorf("published %s keyed %s", pub.types[0], pub.keys[0])
	}
	got := pub.warnings[0]
	want := chatevents.EngineerAwayWarningPayload{
		CaseID: "case-1", EngineerEmail: "eng@example.com", CustomerName: "jane@example.com",
		Product: "devant", Subject: "Deploy fails", LastActiveAt: "2026-10-08T10:00:00Z",
		EndsAt: "2026-10-08T10:05:00Z", ChatLink: "https://csm.example/chat",
	}
	if got != want {
		t.Errorf("payload = %+v\nwant      %+v", got, want)
	}
}

func TestEngineerIdle_AwayWarningAgainAfterTheEngineerWrites(t *testing.T) {
	f := newIdleFixture(t)
	pub := f.withWarningEmails()

	f.at(90 * time.Second)
	f.at(2 * time.Minute)
	pub.wait(t, 1)

	// The engineer replies from their phone, without the portal, then goes
	// quiet again.
	f.start = f.start.Add(3 * time.Minute)
	f.chat.EngineerActiveAt = f.start
	f.at(0)
	f.at(2 * time.Minute)
	pub.wait(t, 1)
	if pub.count() != 2 {
		t.Fatalf("published %d events, want a second warning for the new quiet spell", pub.count())
	}
}

func TestEngineerIdle_NoAwayWarningWithTheTabOpen(t *testing.T) {
	f := newIdleFixture(t)
	pub := f.withWarningEmails()
	defer f.openTab()()

	f.at(4 * time.Minute)
	f.at(6 * time.Minute)
	if pub.count() != 0 {
		t.Errorf("published %d events for an engineer with the portal open", pub.count())
	}
}
