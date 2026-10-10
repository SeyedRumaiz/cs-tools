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
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/eventbus"
)

// outageRecord builds an outage email record as entity-service's outage notice
// drainer publishes it.
func outageRecord(eventType, kind, body string) eventbus.Record {
	return eventbus.Record{Value: []byte(`{"type":"` + eventType + `","entityId":"7d48b7c5-3d28-4d2e-876f-51a5ce231755","payload":{` +
		`"outageId":"7d48b7c5-3d28-4d2e-876f-51a5ce231755","number":"OUT0010001","kind":"` + kind + `",` +
		`"subject":"Choreo Outage OUT0010001 on Login errors","body":"` + body + `",` +
		`"recipients":["` + testRecipient + `"]}}`)}
}

func TestDispatcher_Handle_OutageCommunication_SendsTheMessageWithItsLink(t *testing.T) {
	mock := &mockEmailSender{}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})

	rec := outageRecord("outage.communication_due", "DECLARED", `Hello Team,\n  Impact: 2 - High <now>`)
	if err := d.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mock.calls))
	}
	sent := mock.calls[0]
	if sent.subject != "Choreo Outage OUT0010001 on Login errors" {
		t.Errorf("subject = %q, want entity-service's verbatim", sent.subject)
	}
	if len(sent.to) != 1 || sent.to[0] != testRecipient {
		t.Errorf("to = %v, want the payload's recipients", sent.to)
	}
	for _, want := range []string{
		"Impact: 2 - High &lt;now&gt;", // escaped, not interpolated
		"<br>",                         // line breaks kept
		`href="https://csm.example/operations/outages/7d48b7c5-3d28-4d2e-876f-51a5ce231755"`,
	} {
		if !strings.Contains(sent.htmlBody, want) {
			t.Errorf("body missing %q:\n%s", want, sent.htmlBody)
		}
	}
}

func TestDispatcher_Handle_OutageNotification_UsesThePhaseShell(t *testing.T) {
	mock := &mockEmailSender{}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})

	if err := d.Handle(context.Background(), outageRecord("outage.notification_due", "RESOLVED", "Outage OUT0010001 resolved.")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mock.calls))
	}
	body := mock.calls[0].htmlBody
	for _, want := range []string{"Resolved", "OUT0010001", "Outage OUT0010001 resolved.", "/operations/outages/7d48b7c5"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "<!-- [") {
		t.Error("a template placeholder was left unreplaced")
	}
}

func TestDispatcher_Handle_Outage_DebugModeAndKillswitch(t *testing.T) {
	debug := &mockEmailSender{}
	d := NewDispatcher(debug, &mockGoogleChatSender{}, &mockCallSender{}, &mockLinkResolver{},
		true, true, []string{"debug@wso2.com"}, true, "", nil)
	if err := d.Handle(context.Background(), outageRecord("outage.communication_due", "RESOLVED", "x")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := debug.calls[0].to; len(got) != 1 || got[0] != "debug@wso2.com" {
		t.Errorf("debug mode: to = %v, want only the debug list", got)
	}

	off := &mockEmailSender{}
	d = NewDispatcher(off, &mockGoogleChatSender{}, &mockCallSender{}, &mockLinkResolver{},
		false, false, nil, true, "", nil)
	if err := d.Handle(context.Background(), outageRecord("outage.notification_due", "DECLARED", "x")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(off.calls) != 0 {
		t.Errorf("sent %d emails with sending disabled, want 0", len(off.calls))
	}
}

// The outage communication has no Update arm, so an UPDATE on its type is a
// malformed record and must be rejected rather than mailed.
func TestDispatcher_Handle_Outage_RejectsAKindItsFlowDoesNotHave(t *testing.T) {
	mock := &mockEmailSender{}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})

	if err := d.Handle(context.Background(), outageRecord("outage.communication_due", "UPDATE", "x")); err == nil {
		t.Fatal("an UPDATE outage communication was accepted")
	}
	if len(mock.calls) != 0 {
		t.Errorf("sent %d emails for a rejected record", len(mock.calls))
	}
}

// entity-service has already recorded the email as sent, so a failed send must
// come back as an error: the consumer retries it and then dead-letters it,
// which is the last place it can still be recovered.
func TestDispatcher_Handle_Outage_ReturnsSendFailures(t *testing.T) {
	mock := &mockEmailSender{err: errors.New("email service unavailable")}
	d := newTestDispatcher(mock, &mockGoogleChatSender{}, &mockCallSender{})

	if err := d.Handle(context.Background(), outageRecord("outage.notification_due", "DECLARED", "x")); err == nil {
		t.Fatal("a failed send was swallowed; it must reach the consumer's retry and DLQ")
	}
}
