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

// Package pushevents is the shared wire shape for a live-engineer-chat
// lifecycle event, pushed by csm-portal/backend to whichever origin a case
// came from (customer-portal/backend-v2, console-chat-bridge, or a future
// target) via POST {origin}/internal/chat-events, and also used internally
// for the CSM-engineer-facing broadcast events on the same case.
//
// Before this package existed, the sender (csm-portal/backend's own
// chatEvent struct) and each receiver (backend-v2's chatEventPushBody,
// console-chat-bridge's anonymous decode) independently hand-copied this
// shape. csm-portal/backend's version had already grown fields
// (ProjectID/Source/Channel/Subject/CustomerEmail/CustomerName/
// PriorMessages) that backend-v2's own struct never declared -- Go's
// encoding/json silently drops a field the receiving struct doesn't know
// about, so that drift was invisible until read closely. Importing this one
// type from every sender and receiver closes that gap outright instead of
// relying on the two copies being kept in sync by convention.
package pushevents

// Type is the value of ChatEvent.Type -- see the const block below for the
// full, confirmed vocabulary (every literal actually used across
// csm-portal/backend's internal/handler/{chat.go,chat_timeout_sweeper.go},
// enumerated and traced to its push mechanism during the SDK-extraction
// design pass).
type Type string

const (
	// Pushed via notifyOrigin -- these cross to whichever product/tenant
	// originated the case (customer-portal/backend-v2, console-chat-bridge,
	// or a future target). This is the vocabulary a live-chat client SDK's
	// own public event union maps onto 1:1.
	TypeQueued               Type = "queued"
	TypeEngineerAssigned     Type = "engineer_assigned"
	TypeEngineerMessage      Type = "engineer_message"
	TypeEngineerDisconnected Type = "engineer_disconnected"
	TypeConvertedToCase      Type = "converted_to_case"
	// TypeChatAbandoned fires when a case sat WAITING_FOR_ENGINEER, never
	// assigned to anyone at all, past chat-routing-service's own
	// QUEUE_ABANDON_SECONDS -- a genuinely terminal, customer-facing state
	// (nobody was ever connected, distinct from TypeEngineerDisconnected's
	// "an engineer connected then left" semantics).
	TypeChatAbandoned Type = "chat_abandoned"

	// Pushed via publishToEngineers/publishToEngineer -- CSM-internal only,
	// an engineer's own case-list/alert UI. Never reaches a tenant/product's
	// own client.
	TypeCustomerEscalation Type = "customer_escalation"
	TypeCustomerMessage    Type = "customer_message"
	TypeSessionAccepted    Type = "session_accepted"
	TypeSessionClosed      Type = "session_closed"
	// TypeSessionClosedByCustomer is the engineer-facing twin of a
	// tenant/customer-initiated completeChat (see EndByTenant) -- mirrors
	// TypeSessionClosed's own role for an engineer-initiated "End session",
	// so a stale "accepted" alert clears the same way either direction.
	TypeSessionClosedByCustomer Type = "session_closed_by_customer"
	// TypeCaseTimedOut fires when an assigned-but-unconfirmed case exceeds
	// PENDING_TIMEOUT_SECONDS -- the case gets reassigned to a different
	// engineer, so it is NOT terminal from the customer's point of view and
	// deliberately never crosses notifyOrigin (unlike TypeChatAbandoned).
	TypeCaseTimedOut Type = "case_timed_out"
)

// PriorMessageRole says who sent one PriorMessage -- duplicated from
// chat-routing-service/sdk-go/routingclient.PriorMessageRole rather than
// imported, matching that module's own stated philosophy (its HTTP API is
// the only coupling point between independently-versioned SDKs, see its own
// package doc comment) and keeping this module free of any dependency on
// another nested module.
type PriorMessageRole string

const (
	PriorMessageRoleCustomer  PriorMessageRole = "customer"
	PriorMessageRoleAssistant PriorMessageRole = "assistant"
	// PriorMessageRoleEngineer is only ever produced reading a live,
	// post-acceptance transcript back (see commentsForWorkItem in
	// chat-routing-service) -- never written for a pre-escalation message.
	PriorMessageRoleEngineer PriorMessageRole = "engineer"
)

// PriorMessage mirrors routingclient.PriorMessage's wire shape exactly.
type PriorMessage struct {
	Role      PriorMessageRole `json:"role"`
	Content   string           `json:"content"`
	CreatedAt string           `json:"createdAt,omitempty"`
}

// ChatEvent is the one shared shape for every live-engineer-chat push --
// both the notifyOrigin-bound (tenant/product-facing) and
// publishToEngineers-bound (CSM-internal) directions use this same struct;
// Type says which kind a given instance is. Field-for-field identical to
// csm-portal/backend's own pre-existing chatEvent (already the superset of
// every receiver's own independent copy).
type ChatEvent struct {
	Type           Type   `json:"type"`
	CaseID         string `json:"caseId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	// Source/Channel identify which product/surface this case originated
	// from (e.g. "customer-portal"/"" for the existing Novera flow,
	// "asgardeo"/"ask-ai" for identity-apps' legacy Ask AI escalation path).
	// Source also doubles as csm-portal/backend's notifyOrigin routing key
	// -- see ChatHandler.notifierFor. Once console-chat-bridge is
	// multi-tenant, Source stops varying per tenant (every tenant sharing
	// one bridge deployment shares the same routing Source); a case's own
	// tenant identity is carried separately -- see TenantSlug.
	Source  string `json:"source,omitempty"`
	Channel string `json:"channel,omitempty"`
	// TenantSlug is the tenant/product config identity a case belongs to
	// (e.g. "identity-console", "acme-portal") -- distinct from Source
	// (the physical push-target routing key) and Channel (the interaction
	// channel within a tenant). Server-stamped only, from the resolved
	// tenant config at escalate time -- never caller-supplied. Used for
	// tenant-isolation checks on every case-scoped operation, not for
	// notifyOrigin routing.
	TenantSlug    string `json:"tenantSlug,omitempty"`
	Subject       string `json:"subject,omitempty"`
	CustomerEmail string `json:"customerEmail,omitempty"`
	CustomerName  string `json:"customerName,omitempty"`
	EngineerEmail string `json:"engineerEmail,omitempty"`
	Message       string `json:"message,omitempty"`
	// EntityCaseID is set only on a converted_to_case event -- the real
	// entity-service case ID the customer's browser should point to now
	// that this chat has ended.
	EntityCaseID string `json:"entityCaseId,omitempty"`
	// PriorMessages is set only on customer_escalation -- the customer's
	// AI-chatbot transcript snapshotted at escalation time, so the
	// receiving engineer's browser can seed the chat with that context
	// instead of starting cold.
	PriorMessages []PriorMessage `json:"priorMessages,omitempty"`
	Timestamp     string         `json:"timestamp"`
}
