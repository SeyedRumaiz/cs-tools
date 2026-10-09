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

// Package pushevents defines the shared wire shape for a live-engineer-chat
// lifecycle event. csm-portal/backend pushes it to whichever origin a case
// came from (customer-portal/backend-v2, console-chat-bridge, or a future
// target) via POST {origin}/internal/chat-events, and it is also used
// internally for CSM-engineer-facing broadcast events on the same case.
package pushevents

// Type is the value of ChatEvent.Type; see the const block below for the
// full vocabulary.
type Type string

const (
	// Pushed via notifyOrigin: these cross to whichever product/tenant
	// originated the case (customer-portal/backend-v2, console-chat-bridge,
	// or a future target).
	TypeQueued               Type = "queued"
	TypeEngineerAssigned     Type = "engineer_assigned"
	TypeEngineerMessage      Type = "engineer_message"
	TypeEngineerDisconnected Type = "engineer_disconnected"
	TypeConvertedToCase      Type = "converted_to_case"
	// TypeChatAbandoned fires when a case sits WAITING_FOR_ENGINEER, never
	// assigned to anyone, past chat-routing-service's QUEUE_ABANDON_SECONDS.
	// Terminal and customer-facing, distinct from TypeEngineerDisconnected
	// (where an engineer had connected before leaving).
	TypeChatAbandoned Type = "chat_abandoned"
	// TypeEngineerStatus tells the customer about the assigned engineer's
	// availability during an accepted chat; Status says which. Not terminal.
	TypeEngineerStatus Type = "engineer_status"

	// Pushed via publishToEngineers/publishToEngineer — CSM-internal only,
	// an engineer's own case-list/alert UI. Never reaches a tenant/product's
	// own client.
	TypeCustomerEscalation Type = "customer_escalation"
	TypeCustomerMessage    Type = "customer_message"
	TypeSessionAccepted    Type = "session_accepted"
	TypeSessionClosed      Type = "session_closed"
	// TypeSessionClosedByCustomer is the engineer-facing counterpart to a
	// customer-initiated completeChat (see EndByTenant), clearing a stale
	// "accepted" alert the same way TypeSessionClosed does.
	TypeSessionClosedByCustomer Type = "session_closed_by_customer"
	// TypeCaseTimedOut fires when an assigned-but-unconfirmed case exceeds
	// PENDING_TIMEOUT_SECONDS and gets reassigned to a different engineer.
	// It is not terminal and, unlike TypeChatAbandoned, never crosses
	// notifyOrigin.
	TypeCaseTimedOut Type = "case_timed_out"
)

// PriorMessageRole says who sent one PriorMessage. It duplicates
// chat-routing-service/sdk-go/routingclient.PriorMessageRole rather than
// importing it, to keep this module free of any dependency on another
// nested module.
// EngineerStatus is ChatEvent.Status on a TypeEngineerStatus event.
type EngineerStatus string

const (
	// EngineerAway: the engineer's tab is closed and they have not written
	// for a while.
	EngineerAway EngineerStatus = "away"
	// EngineerBack: the engineer reopened their tab after EngineerAway.
	EngineerBack EngineerStatus = "back"
	// EngineerBusy: the engineer's tab is open but they have not written
	// for a while, for example while reading logs.
	EngineerBusy EngineerStatus = "busy"
)

// ReasonInactive is ChatEvent.Reason on a TypeEngineerDisconnected event
// sent because the engineer was away too long, rather than ending the chat.
const ReasonInactive = "inactive"

type PriorMessageRole string

const (
	PriorMessageRoleCustomer  PriorMessageRole = "customer"
	PriorMessageRoleAssistant PriorMessageRole = "assistant"
	// PriorMessageRoleEngineer only appears when reading back a live,
	// post-acceptance transcript (see commentsForWorkItem in
	// chat-routing-service); it is never written for a pre-escalation
	// message.
	PriorMessageRoleEngineer PriorMessageRole = "engineer"
)

// PriorMessage mirrors routingclient.PriorMessage's wire shape exactly.
type PriorMessage struct {
	Role      PriorMessageRole `json:"role"`
	Content   string           `json:"content"`
	CreatedAt string           `json:"createdAt,omitempty"`
}

// ChatEvent is the shared shape for every live-engineer-chat push, both
// the notifyOrigin-bound (tenant/product-facing) and
// publishToEngineers-bound (CSM-internal) directions. Type says which
// kind a given instance is.
type ChatEvent struct {
	Type           Type   `json:"type"`
	CaseID         string `json:"caseId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	// Source/Channel identify which product/surface this case originated
	// from (e.g. "customer-portal"/"" for the existing Novera flow,
	// "asgardeo"/"ask-ai" for identity-apps' legacy Ask AI escalation path).
	// Source also doubles as csm-portal/backend's notifyOrigin routing key
	// (see ChatHandler.notifierFor); tenant identity is carried
	// separately, see TenantSlug.
	Source  string `json:"source,omitempty"`
	Channel string `json:"channel,omitempty"`
	// TenantSlug is the tenant/product config identity a case belongs to
	// (e.g. "identity-console", "acme-portal"), distinct from Source (the
	// push-target routing key) and Channel. It is server-stamped from the
	// resolved tenant config at escalate time, never caller-supplied, and
	// used for tenant-isolation checks, not notifyOrigin routing.
	TenantSlug    string `json:"tenantSlug,omitempty"`
	Subject       string `json:"subject,omitempty"`
	CustomerEmail string `json:"customerEmail,omitempty"`
	CustomerName  string `json:"customerName,omitempty"`
	EngineerEmail string `json:"engineerEmail,omitempty"`
	Message       string `json:"message,omitempty"`
	// EntityCaseID is set only on a converted_to_case event — the real
	// entity-service case ID the customer's browser should point to now
	// that this chat has ended.
	EntityCaseID string `json:"entityCaseId,omitempty"`
	// PriorMessages is set only on customer_escalation — the customer's
	// AI-chatbot transcript snapshotted at escalation time, so the
	// receiving engineer's browser can seed the chat with that context
	// instead of starting cold.
	PriorMessages []PriorMessage `json:"priorMessages,omitempty"`
	// Status is set only on TypeEngineerStatus.
	Status EngineerStatus `json:"status,omitempty"`
	// Reason is set on TypeEngineerDisconnected when the chat was ended for
	// the engineer rather than by them (ReasonInactive).
	Reason    string `json:"reason,omitempty"`
	Timestamp string `json:"timestamp"`
}
