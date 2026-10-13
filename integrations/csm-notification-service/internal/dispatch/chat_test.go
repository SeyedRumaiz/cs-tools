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

package dispatch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/eventbus"
)

const awayChatCase = "11111111-1111-1111-1111-111111111111"

func chatAssignedWhileAwayRecord(engineerEmail string) eventbus.Record {
	return eventbus.Record{Value: []byte(`{"type":"chat.assigned_while_away","entityId":"` + awayChatCase + `","payload":{` +
		`"caseId":"` + awayChatCase + `","engineerEmail":"` + engineerEmail + `",` +
		`"customerName":"Jane <b>Doe</b>","product":"devant","subject":"Deploy fails",` +
		`"assignedOn":"2026-10-12T04:43:05Z","chatLink":"https://csm.example/chat"}}`)}
}

// TestDispatcher_Handle_ChatAssignedWhileAway_EmailsEngineerOnly: the
// email goes to the engineer alone, with the chat's details and a link to
// the Chat page, and no message text.
func TestDispatcher_Handle_ChatAssignedWhileAway_EmailsEngineerOnly(t *testing.T) {
	mock := &mockEmailSender{}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})

	if err := d.Handle(context.Background(), chatAssignedWhileAwayRecord("eng@wso2.com")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mock.calls))
	}
	got := mock.calls[0]
	if want := []string{"eng@wso2.com"}; !reflect.DeepEqual(got.to, want) {
		t.Errorf("to = %v, want %v", got.to, want)
	}
	if len(got.cc) != 0 || len(got.bcc) != 0 {
		t.Errorf("a private email must have no cc/bcc, got cc=%v bcc=%v", got.cc, got.bcc)
	}
	if want := "[WSO2 Support] A live chat is waiting for you"; got.subject != want {
		t.Errorf("subject = %q, want %q", got.subject, want)
	}
	for _, want := range []string{
		"A live chat is waiting for you", "you have no CSM Portal tab open",
		">Customer<", ">Jane &lt;b&gt;Doe&lt;/b&gt;<", ">Product<", ">devant<", ">Subject<", ">Deploy fails<",
		">Assigned At<", ">2026-10-12 10:13:05<",
		`href="https://csm.example/chat"`, "Open Chat",
		"offered to the next available engineer",
		"This message was sent by WSO2's support system.", "WSO2-Logo-Black.png",
	} {
		if !strings.Contains(got.htmlBody, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(got.htmlBody, "Expected sent to") {
		t.Error("the debug-mode recipient line must not render outside debug mode")
	}
}

func TestDispatcher_Handle_ChatAssignedWhileAway_DebugModeRedirects(t *testing.T) {
	mock := &mockEmailSender{}
	d := NewDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{}, &mockLinkResolver{}, true, true, []string{"debug@wso2.com"}, true, "", nil)

	if err := d.Handle(context.Background(), chatAssignedWhileAwayRecord("eng@wso2.com")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	got := mock.calls[0]
	if want := []string{"debug@wso2.com"}; !reflect.DeepEqual(got.to, want) {
		t.Errorf("to = %v, want %v", got.to, want)
	}
	if !strings.Contains(got.htmlBody, "Expected sent to: eng@wso2.com") {
		t.Error("debug mode must show who the mail was meant for")
	}
}

func TestDispatcher_Handle_ChatAssignedWhileAway_DisabledSendsNothing(t *testing.T) {
	mock := &mockEmailSender{}
	d := NewDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{}, &mockLinkResolver{}, false, false, nil, true, "", nil)
	if err := d.Handle(context.Background(), chatAssignedWhileAwayRecord("eng@wso2.com")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(mock.calls) != 0 {
		t.Errorf("sent %d emails with sending disabled", len(mock.calls))
	}
}

func TestDispatcher_Handle_ChatAssignedWhileAway_SendFailureRetries(t *testing.T) {
	mock := &mockEmailSender{err: errors.New("smtp down")}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})
	if err := d.Handle(context.Background(), chatAssignedWhileAwayRecord("eng@wso2.com")); err == nil {
		t.Fatal("a failed send must return an error so the consumer retries")
	}
}

func TestDispatcher_Handle_ChatAssignedWhileAway_InvalidNotSent(t *testing.T) {
	mock := &mockEmailSender{}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})
	if err := d.Handle(context.Background(), chatAssignedWhileAwayRecord("not-an-email")); err == nil {
		t.Fatal("an event without a valid engineer email must fail validation")
	}
	if len(mock.calls) != 0 {
		t.Errorf("sent %d emails for an invalid event", len(mock.calls))
	}
}
