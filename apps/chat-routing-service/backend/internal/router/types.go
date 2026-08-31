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
// for the live-engineer-chat routing feature: engineers are AVAILABLE,
// BUSY, or OFFLINE; an escalation is assigned to an available engineer
// immediately or queued FIFO if none are free; a completed session drains
// the queue. See Router's own doc comment for the full state machine.
//
// Backed by PostgreSQL (see this service's migrations/ and internal/db) --
// engineer presence and the escalation queue survive a restart and, since
// every state transition is a transaction against a shared database rather
// than an in-process mutex, this is also safe for multiple replicas of this
// service to run against the same database concurrently. The one limitation
// that predates this and remains: no timeout/reassignment if an assigned
// engineer never accepts or goes unreachable -- that stays a prototype gap,
// unrelated to where the state lives.
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
// case ends up assigned to (see that backend's internal/handler/chat.go) --
// carried through Escalate/queueing/Decline verbatim, this service never
// interprets these fields itself. Persisted as a JSONB blob (see
// migrations/000001_create_engineers.up.sql and
// 000002_create_escalation_queue.up.sql) rather than normalized columns --
// this service never queries by any field other than CaseID, and a blob
// keeps it a one-file change if csm-portal/backend ever adds a field.
type CaseInfo struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	Subject        string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	Message        string `json:"message,omitempty"`
}
