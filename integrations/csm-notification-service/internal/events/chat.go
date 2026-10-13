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

package events

// TypeChatAssignedWhileAway: a live chat was assigned to an engineer whose
// csm-portal tab had been closed for longer than the grace period, so the
// in-portal alert could not reach them. Published by csm-portal-backend on
// its own topic (CHAT_EVENT_HUB_TOPIC, e.g. "chat-events") and emailed here
// to that engineer alone. Kept in sync by hand with csm-portal-backend's
// internal/chatevents/chatevents.go (separate Go modules); that file is the
// source of truth for the wire shape. EntityID is the chat's case id.
const TypeChatAssignedWhileAway Type = "chat.assigned_while_away"

// ChatAssignedWhileAwayPayload is TypeChatAssignedWhileAway's payload. It
// carries no message text: the email only tells the engineer a chat is
// waiting.
type ChatAssignedWhileAwayPayload struct {
	CaseID        string `json:"caseId"`
	EngineerEmail string `json:"engineerEmail"`
	// CustomerName is the customer's name, else their email.
	CustomerName string `json:"customerName,omitempty"`
	// Product is the tenant the chat came from, e.g. "devant".
	Product string `json:"product,omitempty"`
	Subject string `json:"subject,omitempty"`
	// AssignedOn is when the chat was assigned, RFC3339.
	AssignedOn string `json:"assignedOn"`
	// ChatLink is csm-portal's Chat page. Supplied by the publisher, unlike
	// the case.* links: the Chat page is a csm-portal route this service
	// has no other way to know, and the recipient is always an engineer.
	ChatLink string `json:"chatLink"`
}
