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

// Package router implements the engineer-availability/queue state machine
// for the live-engineer-chat routing feature. Each engineer has a manual
// chat_status — AVAILABLE, BUSY (do-not-disturb: holds existing cases,
// takes no new ones), or OFFLINE — plus a configurable concurrent-chat
// capacity (max_concurrent_chats, default 1). An escalation goes to
// whichever AVAILABLE engineer with spare capacity has taken the fewest
// chats today (ties broken by fewest active chats, then longest
// AVAILABLE), or is queued FIFO if nobody qualifies; a completed session
// drains the queue.
//
// "Pending" and "which cases an engineer holds" are per-conversation facts
// derived from chat_conversation (assignee_id/state/accepted_at/
// session_ended_at), not per-engineer state — see Router.Escalate and
// Router.GetPresence.
//
// Engineers are identified by their IdP "userid" claim; this package
// stores no email of its own.
//
// Backed by PostgreSQL: every state transition is a transaction, so
// multiple replicas can run against the same database safely. An engineer
// who never accepts an assigned case is caught by the timeout sweep in
// timeout.go.
package router

// Status is an engineer's manual chat_status — a plain three-way toggle,
// independent of how many cases they're actually holding (see the package
// doc comment). Never PENDING: "pending" describes one conversation, not
// an engineer, and is reported per-case (see CaseStatus) rather than as a
// top-level Status value.
type Status string

const (
	StatusAvailable Status = "AVAILABLE"
	// StatusBusy is a manual do-not-disturb toggle: takes no new
	// assignments, but does not affect cases already held.
	StatusBusy    Status = "BUSY"
	StatusOffline Status = "OFFLINE"
)

// CaseInfo is everything csm-portal/backend needs to reconstruct the chat
// event it publishes to whichever engineer a case ends up assigned to.
// Carried through Escalate/queueing/Decline verbatim — this service never
// interprets these fields itself. Stored as a JSONB blob rather than
// normalized columns since nothing here queries by any field but CaseID.
type CaseInfo struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	// Source/Channel identify which product/surface raised this case (e.g.
	// "customer-portal"/"" for the Novera "Chat with an Engineer" flow,
	// "asgardeo"/"ask-ai" for identity-apps' Ask AI panel via
	// console-chat-bridge). Persisted as-is and echoed back by GetCaseInfo.
	// Empty Source means "customer-portal".
	Source  string `json:"source,omitempty"`
	Channel string `json:"channel,omitempty"`
	// TenantSlug identifies which console-chat-bridge tenant raised this
	// case through the generic /v1/{tenant}/... API — empty for the legacy
	// customer-portal/console-chat-bridge paths. Used by Router.EndByTenant
	// to scope a tenant-initiated completeChat to that tenant's own case.
	TenantSlug    string `json:"tenantSlug,omitempty"`
	Subject       string `json:"subject,omitempty"`
	CustomerEmail string `json:"customerEmail,omitempty"`
	CustomerName  string `json:"customerName,omitempty"`
	Message       string `json:"message,omitempty"`
	// PriorMessages is the customer's AI-chatbot (Novera) conversation
	// history at the moment "Chat with an Engineer" was clicked, sent by
	// the frontend itself (see PriorMessage). CreateWorkItem is the only
	// place that reads this field (persisting it into chat_routing.comment
	// once); every other CaseInfo/CaseStatus this package returns instead
	// has PriorMessages populated fresh from that table (see
	// commentsForCase).
	PriorMessages []PriorMessage `json:"priorMessages,omitempty"`
}

// PriorMessageRole says who sent one PriorMessage: Customer or Assistant
// for CreateWorkItem's pre-escalation transcript, plus Engineer for the
// live post-acceptance conversation that commentsForCase/
// commentsForWorkItem read back from the same table (see AddComment).
type PriorMessageRole string

const (
	PriorMessageRoleCustomer  PriorMessageRole = "customer"
	PriorMessageRoleAssistant PriorMessageRole = "assistant"
	PriorMessageRoleEngineer  PriorMessageRole = "engineer"
)

// priorMessageAssistantAuthor is the comment.created_by value meaning "the
// Novera AI assistant said this".
const priorMessageAssistantAuthor = "Novera"

// PriorMessage is one message from the customer's pre-escalation AI-chatbot
// (Novera) conversation. CreatedAt is RFC 3339, optional, and carried as a
// string since CaseInfo crosses service boundaries as JSON before
// CreateWorkItem (the only parser) reads it, falling back to now() if
// empty or malformed.
type PriorMessage struct {
	Role      PriorMessageRole `json:"role"`
	Content   string           `json:"content"`
	CreatedAt string           `json:"createdAt,omitempty"`
}

// CaseStatus is one case an engineer currently holds, as reported by
// GetPresence/DebugState — CaseInfo plus whether it's still awaiting
// Accept. Replaces the old single CurrentCase/PendingSince pair now that
// an engineer can hold more than one case at once.
type CaseStatus struct {
	CaseInfo
	// Pending is true until Router.Accept confirms this specific case --
	// mirrors the old isPendingAccept check, now evaluated per case rather
	// than per engineer.
	Pending bool `json:"pending"`
	// AssignedAt is when this case was assigned to this engineer (not when
	// created) — the countdown to the timeout sweep runs from here, same
	// as the old PendingSince.
	AssignedAt string `json:"assignedAt"`
}
