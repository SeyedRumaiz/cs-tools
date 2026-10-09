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

// Package assistant answers a customer's questions with an AI model before
// (or instead of) a live engineer. A Provider does the answering; the
// bridge picks one per deployment (see Config) and relays its events to
// the customer's browser.
package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Event types a Provider emits, in order: any number of "status" and
// "token" events, then exactly one "done" or "error".
const (
	// EventStatus is progress worth showing while the answer is prepared,
	// such as "Searching the knowledge base".
	EventStatus = "status"
	// EventToken is the next piece of the answer's text.
	EventToken = "token"
	// EventDone ends the answer; Text is the complete answer.
	EventDone = "done"
	// EventError ends the answer without one; Text is safe to show.
	EventError = "error"
)

// Event is one step of an answer, sent to the browser as JSON.
type Event struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Turn is one customer message to answer.
type Turn struct {
	// AccountID scopes the model's usage budget; see AccountID.
	AccountID string
	// ConversationID keeps the turns of one conversation together, so the
	// model sees the earlier ones.
	ConversationID string
	Message        string
}

// Provider answers one Turn, calling emit for each Event. It must end with
// EventDone or EventError and must stop when ctx is cancelled.
type Provider interface {
	Answer(ctx context.Context, turn Turn, emit func(Event))
}

// AccountID is the model account a customer's questions are counted
// against. Customers of these products may have no WSO2 support account,
// so each user gets their own: the tenant slug plus a short hash of their
// subject, which keeps usage budgets per user without exposing the
// subject.
func AccountID(tenantSlug, subject string) string {
	sum := sha256.Sum256([]byte(tenantSlug + "\x00" + subject))
	return tenantSlug + "." + hex.EncodeToString(sum[:8])
}
