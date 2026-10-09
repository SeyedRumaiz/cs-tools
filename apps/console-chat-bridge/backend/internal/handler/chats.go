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

// Package handler implements this bridge's routes, in two families sharing
// one ChatsHandler: the legacy, single-tenant /support/chats routes
// (chats.go), pinned to identity-apps' Console Ask AI panel, and the
// generic, multi-tenant /v1/{tenant}/chats routes (chats_v1.go). See
// chats_v1.go's own doc comment for its route list and how its tenant-based
// authorization (requireTenantCase) differs from the legacy routes'
// caller-identity ownership (requireOwner).
package handler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/csmchat"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/stream"
	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/pushevents"
)

const maxBodyBytes = 64 << 10 // 64 KiB

// newCaseID mints a fresh RFC 4122 v4 UUID for a new escalation's
// caseId/liveChatId. Never the frontend's askAiContextId — see caseRecord.
func newCaseID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("newCaseID: crypto/rand unavailable: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// caseRecord is this bridge's in-memory record of one escalation, keyed by
// caseId. In-memory only (lost on restart, single-process), so
// HandleSendMessage/HandleStream know which conversationId/customerEmail to
// carry upstream, and requireOwner can enforce that only the case's opener
// can send messages into it or read its stream.
type caseRecord struct {
	conversationID string
	customerEmail  string
	// ownerSubject is the identity that opened this case, checked on every
	// later /messages and /stream call. Set only by the legacy path's
	// requireOwner; stays "" on a /v1 record, which authorizes by tenant
	// membership instead (see requireTenantCase).
	ownerSubject string
	// tenantSlug is set only for a case raised through /v1/{tenant}/...;
	// "" for a legacy case.
	tenantSlug string
}

// PriorMessage mirrors csm-portal/backend's escalateRequest.PriorMessages
// element shape (role/content/createdAt) — duplicated rather than
// imported, matching this repo's existing convention of small DTO structs
// duplicated per service (see chat-routing-service/sdk-go/routingclient's
// own doc comment on the same choice).
type PriorMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// escalateBody is what identity-apps' Console SPA sends to
// POST /support/chats.
type escalateBody struct {
	// AskAIContextID is Ask AI's stable per-panel-session id, generated
	// client-side. Becomes ConversationID upstream; never reused as the
	// caseId.
	AskAIContextID string         `json:"askAiContextId"`
	Question       string         `json:"question"`
	ErrorContext   string         `json:"errorContext,omitempty"`
	PriorMessages  []PriorMessage `json:"priorMessages,omitempty"`
}

// escalateUpstreamBody matches csm-portal/backend's own escalateRequest
// (internal/handler/chat.go) field-for-field.
type escalateUpstreamBody struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	Source         string `json:"source,omitempty"`
	Channel        string `json:"channel,omitempty"`
	// TenantSlug is set only by the generic /v1/{tenant}/... API (see
	// HandleEscalateV1); empty for the legacy HandleEscalate above.
	TenantSlug    string         `json:"tenantSlug,omitempty"`
	Subject       string         `json:"subject,omitempty"`
	CustomerEmail string         `json:"customerEmail,omitempty"`
	CustomerName  string         `json:"customerName,omitempty"`
	Message       string         `json:"message,omitempty"`
	PriorMessages []PriorMessage `json:"priorMessages,omitempty"`
}

// consoleProjectID is a fixed sentinel in place of a real customer-portal
// projectId (Ask AI/Console has no such concept). Since csm-portal/
// backend's duplicate-open-chat check is scoped by customerEmail+projectId,
// this means one open live chat per admin across all Ask AI usage.
const consoleProjectID = "console-ask-ai"

// sourceAsgardeo / channelAskAI are this integration's fixed source/channel
// tags — see chat-routing-service's CaseInfo.Source doc comment.
const (
	sourceAsgardeo = "asgardeo"
	channelAskAI   = "ask-ai"
)

// ChatsHandler implements both this bridge's legacy and /v1 chat routes
// (see the package doc comment). Safe for concurrent use.
type ChatsHandler struct {
	csm *csmchat.Client
	hub *stream.Hub

	mu    sync.Mutex
	cases map[string]caseRecord
}

// NewChatsHandler constructs a ChatsHandler.
func NewChatsHandler(csm *csmchat.Client, hub *stream.Hub) *ChatsHandler {
	return &ChatsHandler{csm: csm, hub: hub, cases: make(map[string]caseRecord)}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Request body too large or unreadable.")
		return nil, false
	}
	return body, true
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ownerIdentity picks the best available end-user identifier from a
// validated introspect.Identity — Subject when the local IS instance
// populates it, falling back to Username (which this POC's introspection
// response is actually expected to carry — see README).
func ownerIdentity(ctx *http.Request) string {
	id := middleware.IdentityFromContext(ctx.Context())
	if id.Subject != "" {
		return id.Subject
	}
	return id.Username
}

// HandleEscalate handles POST /support/chats. Behind Auth+RequireUser.
// Mints a fresh caseId (never AskAIContextID), calls csm-portal/backend's
// POST /internal/chat/escalate with source="asgardeo", channel="ask-ai",
// and returns {caseId} so the browser can open the SSE stream.
func (h *ChatsHandler) HandleEscalate(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req escalateBody
	if err := json.Unmarshal(body, &req); err != nil || req.AskAIContextID == "" {
		writeError(w, http.StatusBadRequest, "askAiContextId is required.")
		return
	}

	identity := middleware.IdentityFromContext(r.Context())
	owner := ownerIdentity(r)
	caseID := newCaseID()

	priorMessages := req.PriorMessages
	if req.ErrorContext != "" {
		// Surface the Ask AI error the customer saw as one more prior
		// message, so the engineer sees what went wrong, not just the
		// question.
		priorMessages = append(priorMessages, PriorMessage{
			Role:    "assistant",
			Content: req.ErrorContext,
		})
	}

	upstream := escalateUpstreamBody{
		CaseID:         caseID,
		ConversationID: req.AskAIContextID,
		ProjectID:      consoleProjectID,
		Source:         sourceAsgardeo,
		Channel:        channelAskAI,
		Subject:        "Ask AI escalation",
		CustomerEmail:  identity.Username,
		CustomerName:   identity.Username,
		Message:        req.Question,
		PriorMessages:  priorMessages,
	}
	payload, err := json.Marshal(upstream)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}

	respBody, status, err := h.csm.Escalate(r.Context(), payload)
	if err != nil {
		if status == http.StatusConflict {
			// Pass csm-portal/backend's own "you already have an open
			// live chat" message straight through rather than masking it.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write(respBody)
			return
		}
		writeError(w, http.StatusBadGateway, "Could not reach a support engineer right now. Please try again in a moment.")
		return
	}

	h.mu.Lock()
	h.cases[caseID] = caseRecord{
		conversationID: req.AskAIContextID,
		customerEmail:  identity.Username,
		ownerSubject:   owner,
	}
	h.mu.Unlock()

	writeJSON(w, http.StatusAccepted, map[string]string{"caseId": caseID})
}

// requireOwner checks that the authenticated caller is the same admin who
// opened caseID, returning the case's record if so. Writes an error
// response and returns ok=false otherwise (unknown case -> 404, someone
// else's case -> 403).
func (h *ChatsHandler) requireOwner(w http.ResponseWriter, r *http.Request, caseID string) (caseRecord, bool) {
	h.mu.Lock()
	rec, exists := h.cases[caseID]
	h.mu.Unlock()
	if !exists {
		writeError(w, http.StatusNotFound, "Unknown chat.")
		return caseRecord{}, false
	}
	if rec.ownerSubject != ownerIdentity(r) {
		writeError(w, http.StatusForbidden, "This chat does not belong to you.")
		return caseRecord{}, false
	}
	return rec, true
}

// customerMessageUpstreamBody matches csm-portal/backend's own
// customerMessageRequest (internal/handler/chat.go) field-for-field.
type customerMessageUpstreamBody struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
	CustomerEmail  string `json:"customerEmail"`
}

// HandleSendMessage handles POST /support/chats/{caseId}/messages. Behind
// Auth+RequireUser. Forwards to csm-portal/backend's EXISTING, unmodified
// POST /internal/chat/customer-message — the engineer receives it exactly
// the way an existing customer-portal chat message already arrives.
func (h *ChatsHandler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	rec, ok := h.requireOwner(w, r, caseID)
	if !ok {
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required.")
		return
	}

	payload, err := json.Marshal(customerMessageUpstreamBody{
		CaseID:         caseID,
		ConversationID: rec.conversationID,
		Message:        req.Message,
		CustomerEmail:  rec.customerEmail,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if err := h.csm.SendCustomerMessage(r.Context(), payload); err != nil {
		writeError(w, http.StatusBadGateway, "Could not send your message right now. Please try again.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"message": "sent"})
}

// sseHeartbeatInterval keeps the connection alive through any intermediate
// proxy that would otherwise time out an idle response — same interval
// csm-portal/backend's own GET /chat/alerts/stream already uses.
const sseHeartbeatInterval = 15 * time.Second

// HandleStream handles GET /support/chats/{caseId}/stream. Behind
// Auth+RequireUser. Delivers engineer_assigned/engineer_message/etc.
// events (see HandleChatEvents) to the browser over SSE.
func (h *ChatsHandler) HandleStream(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	if _, ok := h.requireOwner(w, r, caseID); !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming unsupported.")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := h.hub.Subscribe(caseID)
	defer unsubscribe()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// HandleChatEvents handles POST /internal/chat-events — csm-portal/
// backend's push contract for delivering chat events. Behind
// Auth+RequireClientID. Relays the event's raw JSON body straight onto
// this case's SSE stream.
func (h *ChatsHandler) HandleChatEvents(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var evt struct {
		CaseID string `json:"caseId"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(body, &evt); err != nil || evt.CaseID == "" {
		writeError(w, http.StatusBadRequest, "caseId is required.")
		return
	}
	// Messages are not replayed to a later subscriber: it reloads them from
	// the case history, so a replay would duplicate the last one. Status
	// notices go stale, and replaying one would also displace the event a
	// late subscriber does need (who accepted, or how the chat ended).
	switch pushevents.Type(evt.Type) {
	case pushevents.TypeEngineerMessage, pushevents.TypeEngineerStatus:
		h.hub.PublishLive(evt.CaseID, string(body))
	default:
		h.hub.Publish(evt.CaseID, string(body))
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "delivered"})
}
