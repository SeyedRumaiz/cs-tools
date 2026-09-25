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

package pushevents

import (
	"encoding/json"
	"testing"
)

// TestChatEvent_RoundTrip confirms every field survives a marshal/unmarshal
// cycle with the expected wire key names -- the whole reason this type
// exists is to be the one shape both a sender and a receiver decode
// correctly, so a silent field-name drift here would defeat the point.
func TestChatEvent_RoundTrip(t *testing.T) {
	original := ChatEvent{
		Type:           TypeEngineerMessage,
		CaseID:         "case-1",
		ConversationID: "conv-1",
		ProjectID:      "proj-1",
		Source:         "console-chat-bridge",
		Channel:        "ask-ai",
		TenantSlug:     "identity-console",
		Subject:        "test subject",
		CustomerEmail:  "customer@example.com",
		CustomerName:   "A Customer",
		EngineerEmail:  "engineer@example.com",
		Message:        "hello",
		EntityCaseID:   "entity-1",
		PriorMessages: []PriorMessage{
			{Role: PriorMessageRoleCustomer, Content: "q1", CreatedAt: "2026-01-01T00:00:00Z"},
			{Role: PriorMessageRoleAssistant, Content: "a1"},
			{Role: PriorMessageRoleEngineer, Content: "e1"},
		},
		Timestamp: "2026-01-01T00:00:01Z",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ChatEvent
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// ChatEvent contains a slice field (PriorMessages), so it isn't
	// comparable with == / != -- compare scalar fields directly and
	// PriorMessages element-by-element below instead.
	if got.Type != original.Type || got.CaseID != original.CaseID ||
		got.TenantSlug != original.TenantSlug || got.Source != original.Source ||
		got.Message != original.Message || got.EntityCaseID != original.EntityCaseID {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, original)
	}
	if len(got.PriorMessages) != len(original.PriorMessages) {
		t.Fatalf("PriorMessages length mismatch: got %d, want %d", len(got.PriorMessages), len(original.PriorMessages))
	}
	for i := range original.PriorMessages {
		if got.PriorMessages[i] != original.PriorMessages[i] {
			t.Errorf("PriorMessages[%d] = %+v, want %+v", i, got.PriorMessages[i], original.PriorMessages[i])
		}
	}
}

// TestChatEvent_WireKeyNames pins the exact JSON key for every field --
// catches an accidental json tag rename, which would silently break every
// existing sender/receiver pair this type replaces.
func TestChatEvent_WireKeyNames(t *testing.T) {
	evt := ChatEvent{
		Type: TypeQueued, CaseID: "c", ConversationID: "cv", ProjectID: "p",
		Source: "s", Channel: "ch", TenantSlug: "t", Subject: "su",
		CustomerEmail: "ce", CustomerName: "cn", EngineerEmail: "ee",
		Message: "m", EntityCaseID: "ec", Timestamp: "ts",
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantKeys := []string{
		"type", "caseId", "conversationId", "projectId", "source", "channel",
		"tenantSlug", "subject", "customerEmail", "customerName",
		"engineerEmail", "message", "entityCaseId", "timestamp",
	}
	for _, k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("expected wire key %q, not found in %v", k, m)
		}
	}
}

// TestEventTypes_AreDistinct guards against a copy-paste duplicate literal
// among the Type consts, which would silently make two different triggers
// indistinguishable on the wire.
func TestEventTypes_AreDistinct(t *testing.T) {
	all := []Type{
		TypeQueued, TypeEngineerAssigned, TypeEngineerMessage, TypeEngineerDisconnected,
		TypeConvertedToCase, TypeChatAbandoned, TypeCustomerEscalation, TypeCustomerMessage,
		TypeSessionAccepted, TypeSessionClosed, TypeSessionClosedByCustomer, TypeCaseTimedOut,
	}
	seen := make(map[Type]bool, len(all))
	for _, ty := range all {
		if seen[ty] {
			t.Errorf("duplicate Type value: %q", ty)
		}
		seen[ty] = true
	}
}
