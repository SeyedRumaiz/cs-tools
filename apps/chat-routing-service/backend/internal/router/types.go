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
// chat_status -- AVAILABLE, BUSY (do-not-disturb; still holds whatever
// cases they already have, but takes no new ones), or OFFLINE -- plus a
// configurable concurrent-chat capacity (max_concurrent_chats, default 1,
// confirmed via the mentor 2026-09-10 -- see the project's
// db-schema-review-2026-09-07-outcomes.md). An escalation goes to whichever
// AVAILABLE engineer with spare capacity has taken the fewest chats today
// (ties broken by fewest currently-active chats, then who's been AVAILABLE
// longest), or gets queued FIFO if nobody qualifies; a completed session
// drains the queue.
//
// "Pending" (assigned, not yet accepted) and "which cases an engineer is
// currently holding" are per-conversation facts derived from
// chat_conversation (assignee_id/state/accepted_at/session_ended_at), not
// per-engineer state -- see Router.Escalate and Router.GetPresence for the
// assignment/read logic and Router itself for the rest of the state
// machine. This is deliberate: with concurrency, a single "current case"
// column on the engineer's own row can't express holding several at once.
//
// Engineers are identified by their IdP "userid" claim -- this package
// stores no email of its own; a caller that needs one already has it from
// its own authenticated session.
//
// Backed by PostgreSQL, so engineer presence and the queue survive a
// restart, and multiple replicas of this service can run against the same
// database safely since every state transition is a transaction rather
// than an in-process mutex. An assigned engineer who never accepts a
// specific case is caught by the timeout sweep in timeout.go, not left
// stuck forever -- and only that one case is affected, not their other
// concurrent sessions.
package router

// Status is an engineer's manual chat_status -- a plain three-way toggle,
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
// Carried through Escalate/queueing/Decline verbatim -- this service never
// interprets these fields itself. Stored as a JSONB blob rather than
// normalized columns since nothing here queries by any field but CaseID.
type CaseInfo struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	Subject        string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	Message        string `json:"message,omitempty"`
}

// CaseStatus is one case an engineer currently holds, as reported by
// GetPresence/DebugState -- CaseInfo plus whether it's still awaiting
// Accept. Replaces the old single CurrentCase/PendingSince pair now that
// an engineer can hold more than one case at once.
type CaseStatus struct {
	CaseInfo
	// Pending is true until Router.Accept confirms this specific case --
	// mirrors the old isPendingAccept check, now evaluated per case rather
	// than per engineer.
	Pending bool `json:"pending"`
	// AssignedAt is when this case was assigned to this engineer (not when
	// created) -- the countdown to the timeout sweep runs from here, same
	// as the old PendingSince.
	AssignedAt string `json:"assignedAt"`
}
