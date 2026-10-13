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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/chatevents"
)

// fakeChatEventPublisher records published events; err makes every publish
// fail.
type fakeChatEventPublisher struct {
	mu        sync.Mutex
	err       error
	published []chatevents.AssignedWhileAwayPayload
	types     []string
	keys      []string
	done      chan struct{}
}

func newFakeChatEventPublisher() *fakeChatEventPublisher {
	return &fakeChatEventPublisher{done: make(chan struct{}, 10)}
}

func (f *fakeChatEventPublisher) Publish(_ context.Context, eventType, entityID string, payload any) error {
	defer func() { f.done <- struct{}{} }()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.types = append(f.types, eventType)
	f.keys = append(f.keys, entityID)
	if p, ok := payload.(chatevents.AssignedWhileAwayPayload); ok {
		f.published = append(f.published, p)
	}
	return f.err
}

// wait blocks until n publishes have finished, since emailIfAway publishes
// in the background.
func (f *fakeChatEventPublisher) wait(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-f.done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a publish")
		}
	}
}

func (f *fakeChatEventPublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.types)
}

func newAwayEmailHandler(t *testing.T) (*ChatHandler, *fakeChatEventPublisher) {
	t.Helper()
	h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{})
	pub := newFakeChatEventPublisher()
	h.WithAwayEngineerEmails(pub, "https://csm.example/")
	h.engineers.remember("eng-1", "eng@example.com")
	return h, pub
}

var awayTestCase = routingclient.CaseInfo{
	CaseID: "case-1", Subject: "Deploy fails", CustomerName: "Jane Doe",
	CustomerEmail: "jane@example.com", TenantSlug: "devant",
}

func TestEngineerAway(t *testing.T) {
	h, _ := newAwayEmailHandler(t)
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

	if !h.engineerAway("eng-1", now) {
		t.Error("an engineer never seen since start must count as away")
	}

	ch := h.hub.Register(engineerHubKey("eng-1"))
	h.engineerTabOpened("eng-1")
	if h.engineerAway("eng-1", now) {
		t.Error("an engineer with an open tab is not away")
	}

	h.hub.Unregister(engineerHubKey("eng-1"), ch)
	h.tabs.closed("eng-1", now)
	if h.engineerAway("eng-1", now.Add(10*time.Second)) {
		t.Error("a tab closed for less than the grace period (a reload) is not away")
	}
	if !h.engineerAway("eng-1", now.Add(h.idlePolicy.TabClosedGrace)) {
		t.Error("a tab closed for the grace period is away")
	}
}

// TestEngineerTabClosed_OtherTabStillOpen: closing one of two tabs is not
// leaving.
func TestEngineerTabClosed_OtherTabStillOpen(t *testing.T) {
	h, _ := newAwayEmailHandler(t)
	key := engineerHubKey("eng-1")
	first := h.hub.Register(key)
	h.hub.Register(key)
	h.engineerTabOpened("eng-1")

	h.hub.Unregister(key, first)
	h.engineerTabClosed("eng-1")
	if _, seen := h.tabs.closedFor("eng-1", time.Now()); seen {
		t.Error("closing one tab while another is open must not record a close")
	}
}

func TestEmailIfAway_PublishesForAwayEngineer(t *testing.T) {
	h, pub := newAwayEmailHandler(t)

	h.emailIfAway(context.Background(), "eng-1", awayTestCase)
	pub.wait(t, 1)

	if pub.types[0] != chatevents.TypeAssignedWhileAway || pub.keys[0] != "case-1" {
		t.Errorf("published %s keyed %s", pub.types[0], pub.keys[0])
	}
	got := pub.published[0]
	if got.CaseID != "case-1" || got.EngineerEmail != "eng@example.com" || got.CustomerName != "Jane Doe" ||
		got.Product != "devant" || got.Subject != "Deploy fails" || got.ChatLink != "https://csm.example/chat" {
		t.Errorf("payload = %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.AssignedOn); err != nil {
		t.Errorf("assignedOn %q is not RFC3339", got.AssignedOn)
	}
}

func TestEmailIfAway_NotWhileTabOpen(t *testing.T) {
	h, pub := newAwayEmailHandler(t)
	h.hub.Register(engineerHubKey("eng-1"))
	h.engineerTabOpened("eng-1")

	h.emailIfAway(context.Background(), "eng-1", awayTestCase)
	if pub.count() != 0 {
		t.Errorf("published %d events for an engineer with an open tab", pub.count())
	}
}

func TestEmailIfAway_OncePerChatAndEngineer(t *testing.T) {
	h, pub := newAwayEmailHandler(t)

	h.emailIfAway(context.Background(), "eng-1", awayTestCase)
	pub.wait(t, 1)
	h.emailIfAway(context.Background(), "eng-1", awayTestCase)
	if pub.count() != 1 {
		t.Errorf("published %d events for the same chat and engineer, want 1", pub.count())
	}

	other := awayTestCase
	other.CaseID = "case-2"
	h.emailIfAway(context.Background(), "eng-1", other)
	pub.wait(t, 1)
	if pub.count() != 2 {
		t.Errorf("a different chat must be emailed too; published %d", pub.count())
	}
}

// TestEmailIfAway_FailedPublishCanRetry: a failed publish is forgotten, so
// the next assignment of the same chat tries again.
func TestEmailIfAway_FailedPublishCanRetry(t *testing.T) {
	h, pub := newAwayEmailHandler(t)
	pub.err = errors.New("event hub down")

	h.emailIfAway(context.Background(), "eng-1", awayTestCase)
	pub.wait(t, 1)
	// The claim is released after the publish returns.
	deadline := time.Now().Add(2 * time.Second)
	for !h.awaySent.claim("case-1|eng-1", time.Now()) {
		if time.Now().After(deadline) {
			t.Fatal("a failed publish must release the chat for a retry")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEmailIfAway_SkipsWithoutEmailOrPublisher(t *testing.T) {
	h, pub := newAwayEmailHandler(t)
	h.emailIfAway(context.Background(), "eng-unknown", awayTestCase)
	if pub.count() != 0 {
		t.Error("an engineer whose email is unknown cannot be emailed")
	}

	plain := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{})
	plain.engineers.remember("eng-1", "eng@example.com")
	plain.emailIfAway(context.Background(), "eng-1", awayTestCase) // must not panic
}
