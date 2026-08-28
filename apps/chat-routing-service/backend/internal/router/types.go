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

// Package router implements the in-memory engineer-availability/queue state
// machine for the live-engineer-chat routing prototype: engineers are
// AVAILABLE, BUSY, or OFFLINE; an escalation is assigned to an available
// engineer immediately or queued FIFO if none are free; a completed session
// drains the queue. See Router's own doc comment for the full state machine.
//
// Deliberately in-memory only and single-process — this is a prototype, not
// a durable service. State is lost on restart, and there is no multi-replica
// coordination (matching the same accepted limitation customer-portal/
// backend-v2's per-conversation WebSocket registry already documents for
// this feature).
package router

// Status is an engineer's current availability.
type Status string

const (
	StatusAvailable Status = "AVAILABLE"
	StatusBusy      Status = "BUSY"
	StatusOffline   Status = "OFFLINE"
)

// CaseInfo is everything csm-portal/backend needs to reconstruct the
// customer_escalation chatEvent it publishes to whichever engineer this
// case ends up assigned to (see that backend's internal/handler/chat.go) —
// carried through Escalate/queueing/Decline verbatim, this service never
// interprets these fields itself.
type CaseInfo struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	Subject        string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	Message        string `json:"message,omitempty"`
}
