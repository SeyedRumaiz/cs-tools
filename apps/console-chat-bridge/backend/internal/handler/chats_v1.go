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

// This file implements the generic, multi-tenant /v1/{tenant}/... API:
//
//	POST /v1/{tenant}/chats                    — start an escalation
//	POST /v1/{tenant}/chats/{caseId}/messages   — send a chat message
//	GET  /v1/{tenant}/chats/{caseId}/events     — SSE delivery
//	GET  /v1/{tenant}/chats/{caseId}/history    — the transcript to date
//	POST /v1/{tenant}/chats/{caseId}/complete   — end the chat (customer side)

package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/csmchat"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

// Input limits, all UTF-8 byte lengths (len(string), matching
// maxBodyBytes' byte-based cap), not character counts.
const (
	maxSubjectBytes        = 200
	maxPriorMessagesCount  = 20
	maxMessageContentBytes = 4000
	maxTotalMessageBytes   = 32768 // 32 KiB
	maxMetadataKeyCount    = 20
	maxMetadataKeyBytes    = 64
	maxMetadataValueBytes  = 500
)

// reservedMetadataKeys are rejected case-insensitively: dedicated
// top-level request fields, or projectId (always derived from the tenant).
var reservedMetadataKeys = map[string]bool{
	"source":     true,
	"channel":    true,
	"tenant":     true,
	"tenantslug": true,
	"projectid":  true,
}

// sensitiveMetadataKeySubstrings are rejected wherever they appear inside a
// metadata key, case-insensitively — defense in depth against a caller
// accidentally (or maliciously) smuggling a credential-shaped value into a
// field that ends up logged/displayed/persisted as plain case metadata.
var sensitiveMetadataKeySubstrings = []string{"token", "secret", "password", "credential", "apikey"}

// validationError is a 400-worthy input problem — returned by
// validateEscalateInput so HandleEscalateV1 can surface the specific
// reason rather than a generic "bad request".
type validationError struct{ message string }

func (e *validationError) Error() string { return e.message }

func invalidf(format string, args ...any) *validationError {
	return &validationError{message: fmt.Sprintf(format, args...)}
}

// validateEscalateInput checks req against every limit in this file's own
// doc comment. Returns nil when req is within every limit.
func validateEscalateInput(req v1EscalateRequest) error {
	if len(req.Subject) > maxSubjectBytes {
		return invalidf("subject must be at most %d bytes.", maxSubjectBytes)
	}
	if len(req.PriorMessages) > maxPriorMessagesCount {
		return invalidf("priorMessages must have at most %d entries.", maxPriorMessagesCount)
	}

	total := len(req.Message)
	if len(req.Message) > maxMessageContentBytes {
		return invalidf("message must be at most %d bytes.", maxMessageContentBytes)
	}
	for _, m := range req.PriorMessages {
		if len(m.Content) > maxMessageContentBytes {
			return invalidf("each priorMessages entry's content must be at most %d bytes.", maxMessageContentBytes)
		}
		total += len(m.Content)
	}
	if total > maxTotalMessageBytes {
		return invalidf("total message content must be at most %d bytes.", maxTotalMessageBytes)
	}

	if len(req.Metadata) > maxMetadataKeyCount {
		return invalidf("metadata must have at most %d keys.", maxMetadataKeyCount)
	}
	for k, v := range req.Metadata {
		if len(k) > maxMetadataKeyBytes {
			return invalidf("metadata key %q must be at most %d bytes.", k, maxMetadataKeyBytes)
		}
		if len(v) > maxMetadataValueBytes {
			return invalidf("metadata value for key %q must be at most %d bytes.", k, maxMetadataValueBytes)
		}
		lower := strings.ToLower(k)
		if reservedMetadataKeys[lower] {
			return invalidf("metadata key %q is reserved.", k)
		}
		for _, sub := range sensitiveMetadataKeySubstrings {
			if strings.Contains(lower, sub) {
				return invalidf("metadata key %q is not allowed.", k)
			}
		}
	}
	return nil
}

// canonicalOwner returns the caller's Subject claim only — never falling
// back to Username the way the legacy path's ownerIdentity() does. Every
// /v1 route rejects an empty result.
func canonicalOwner(r *http.Request) string {
	return middleware.IdentityFromContext(r.Context()).Subject
}

// requireTenantCase confirms caseID belongs to t, durably. h.cases is a
// fast first-layer check only — a miss, or a stored tenantSlug that
// disagrees with t.Slug, always falls through to csm-portal/backend's
// GET /internal/chat/cases/{caseId} (the source of truth) before
// rejecting, so a stale or missing in-memory record never causes a false
// accept.
func (h *ChatsHandler) requireTenantCase(w http.ResponseWriter, r *http.Request, caseID string, t *tenant.Tenant) (caseRecord, bool) {
	h.mu.Lock()
	rec, exists := h.cases[caseID]
	h.mu.Unlock()
	if exists && rec.tenantSlug != "" && rec.tenantSlug == t.Slug {
		return rec, true
	}

	ownership, err := h.csm.GetCaseOwnership(r.Context(), caseID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Unknown chat.")
		return caseRecord{}, false
	}
	if ownership.TenantSlug != t.Slug {
		writeError(w, http.StatusForbidden, "This chat does not belong to this tenant.")
		return caseRecord{}, false
	}

	// Backfill/repair: keep whatever this process already cached while
	// correcting tenantSlug from the durable answer.
	rec.tenantSlug = ownership.TenantSlug
	h.mu.Lock()
	h.cases[caseID] = rec
	h.mu.Unlock()
	return rec, true
}

// v1PriorMessage mirrors PriorMessage — kept as a distinct type only so
// this file's request/response shapes are self-contained and don't take on
// an implicit dependency on chats.go's legacy Ask-AI-specific naming.
type v1PriorMessage = PriorMessage

// v1EscalateRequest is the body for POST /v1/{tenant}/chats.
type v1EscalateRequest struct {
	// ConversationID is a stable per-session id the caller already has.
	// Optional — when empty, this bridge mints the same fresh id it uses
	// as CaseID, so both start out equal.
	ConversationID string           `json:"conversationId,omitempty"`
	Subject        string           `json:"subject,omitempty"`
	Message        string           `json:"message"`
	CustomerName   string           `json:"customerName,omitempty"`
	PriorMessages  []v1PriorMessage `json:"priorMessages,omitempty"`
	// Metadata is arbitrary tenant-supplied key/value data, subject to
	// this file's byte/count limits and reserved/sensitive-key rejection
	// (see validateEscalateInput). Not yet forwarded to
	// chat-routing-service; accepted now so integrations can start
	// sending it.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// v1EscalateResponse is POST /v1/{tenant}/chats's response. Echoes back
// conversationId (client-supplied or bridge-minted) so later /v1 calls can
// carry it explicitly instead of relying on this bridge's in-memory cache.
type v1EscalateResponse struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
}

// HandleEscalateV1 handles POST /v1/{tenant}/chats. Behind ResolveTenant,
// CORS, TenantAuth, RequireSubject.
func (h *ChatsHandler) HandleEscalateV1(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}

	subject := canonicalOwner(r)
	if subject == "" {
		writeError(w, http.StatusUnauthorized, "A user session is required for this action.")
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req v1EscalateRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required.")
		return
	}
	if err := validateEscalateInput(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	identity := middleware.IdentityFromContext(r.Context())
	customerEmail := identity.Username
	if customerEmail == "" {
		customerEmail = identity.Subject
	}
	customerName := req.CustomerName
	if customerName == "" {
		customerName = customerEmail
	}

	caseID := newCaseID()
	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = caseID
	}
	subjectLine := req.Subject
	if subjectLine == "" {
		subjectLine = "Live chat"
	}

	upstream := escalateUpstreamBody{
		CaseID:         caseID,
		ConversationID: conversationID,
		ProjectID:      t.ProjectID,
		Source:         t.RoutingSource,
		Channel:        t.Channel,
		TenantSlug:     t.Slug,
		Subject:        subjectLine,
		CustomerEmail:  customerEmail,
		CustomerName:   customerName,
		Message:        req.Message,
		PriorMessages:  req.PriorMessages,
	}
	payload, err := json.Marshal(upstream)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}

	respBody, status, err := h.csm.Escalate(r.Context(), payload)
	if err != nil {
		if status == http.StatusConflict {
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
		conversationID: conversationID,
		customerEmail:  customerEmail,
		tenantSlug:     t.Slug,
		// The resolved canonical Subject (never Username — see
		// canonicalOwner), recorded even though requireTenantCase
		// authorizes by tenant membership, not a per-caller Subject match.
		ownerSubject: subject,
	}
	h.mu.Unlock()

	// Logs only the already-validated identity claims, never the token.
	slog.InfoContext(r.Context(), "v1 chat escalation", "tenant", t.Slug, "caseId", caseID, "ownerSubject", subject)

	writeJSON(w, http.StatusAccepted, v1EscalateResponse{CaseID: caseID, ConversationID: conversationID})
}

// v1MessageRequest is the body for
// POST /v1/{tenant}/chats/{caseId}/messages. ConversationID is optional;
// when both are available, the client-supplied value wins since it can
// never be staler than this process's cache.
type v1MessageRequest struct {
	ConversationID string `json:"conversationId,omitempty"`
	Message        string `json:"message"`
}

// HandleSendMessageV1 handles POST /v1/{tenant}/chats/{caseId}/messages.
// Behind ResolveTenant, CORS, TenantAuth, RequireSubject.
func (h *ChatsHandler) HandleSendMessageV1(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if canonicalOwner(r) == "" {
		writeError(w, http.StatusUnauthorized, "A user session is required for this action.")
		return
	}

	caseID := r.PathValue("caseId")
	rec, ok := h.requireTenantCase(w, r, caseID, t)
	if !ok {
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req v1MessageRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required.")
		return
	}
	if len(req.Message) > maxMessageContentBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("message must be at most %d bytes.", maxMessageContentBytes))
		return
	}

	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = rec.conversationID
	}
	customerEmail := rec.customerEmail
	if customerEmail == "" {
		identity := middleware.IdentityFromContext(r.Context())
		customerEmail = identity.Username
		if customerEmail == "" {
			customerEmail = identity.Subject
		}
	}

	payload, err := json.Marshal(customerMessageUpstreamBody{
		CaseID:         caseID,
		ConversationID: conversationID,
		Message:        req.Message,
		CustomerEmail:  customerEmail,
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

// historyResponse is GET /v1/{tenant}/chats/{caseId}/history's response
// shape.
type historyResponse struct {
	Messages []csmchat.HistoryMessage `json:"messages"`
}

// HandleGetHistoryV1 handles GET /v1/{tenant}/chats/{caseId}/history,
// returning caseId's full transcript to date. Behind ResolveTenant, CORS,
// TenantAuth, RequireSubject — the same authorization as HandleStreamV1.
func (h *ChatsHandler) HandleGetHistoryV1(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if canonicalOwner(r) == "" {
		writeError(w, http.StatusUnauthorized, "A user session is required for this action.")
		return
	}

	caseID := r.PathValue("caseId")
	if _, ok := h.requireTenantCase(w, r, caseID, t); !ok {
		return
	}

	messages, err := h.csm.GetCaseHistory(r.Context(), caseID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not load the conversation history right now. Please try again.")
		return
	}
	writeJSON(w, http.StatusOK, historyResponse{Messages: messages})
}

// HandleStreamV1 handles GET /v1/{tenant}/chats/{caseId}/events. Behind
// ResolveTenant, CORS, TenantAuth, RequireSubject. Identical delivery to
// the legacy HandleStream; only the authorization check (tenant
// membership via requireTenantCase, not caller-subject ownership) differs.
func (h *ChatsHandler) HandleStreamV1(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if canonicalOwner(r) == "" {
		writeError(w, http.StatusUnauthorized, "A user session is required for this action.")
		return
	}

	caseID := r.PathValue("caseId")
	if _, ok := h.requireTenantCase(w, r, caseID, t); !ok {
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

// v1CompleteRequest is the (optional) body for
// POST /v1/{tenant}/chats/{caseId}/complete. conversationId only populates
// csm-portal/backend's session_closed broadcast to engineers, so an absent
// or empty body never blocks ending the session.
type v1CompleteRequest struct {
	ConversationID string `json:"conversationId,omitempty"`
}

// HandleCompleteV1 handles POST /v1/{tenant}/chats/{caseId}/complete — the
// customer/tenant-initiated session end; this bridge's legacy path has no
// equivalent. Behind ResolveTenant, CORS, TenantAuth, RequireSubject.
func (h *ChatsHandler) HandleCompleteV1(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if canonicalOwner(r) == "" {
		writeError(w, http.StatusUnauthorized, "A user session is required for this action.")
		return
	}

	caseID := r.PathValue("caseId")
	rec, ok := h.requireTenantCase(w, r, caseID, t)
	if !ok {
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req v1CompleteRequest
	if len(body) > 0 {
		_ = json.Unmarshal(body, &req) // best-effort — see v1CompleteRequest's own doc comment
	}
	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = rec.conversationID
	}

	payload, err := json.Marshal(struct {
		CaseID         string `json:"caseId"`
		ConversationID string `json:"conversationId,omitempty"`
		TenantSlug     string `json:"tenantSlug"`
	}{CaseID: caseID, ConversationID: conversationID, TenantSlug: t.Slug})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if err := h.csm.CompleteByTenant(r.Context(), payload); err != nil {
		writeError(w, http.StatusBadGateway, "Could not end this chat right now. Please try again.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "session ended"})
}
