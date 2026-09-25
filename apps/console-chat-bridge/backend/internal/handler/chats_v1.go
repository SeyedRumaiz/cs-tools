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
//	POST /v1/{tenant}/chats                    -- start an escalation
//	POST /v1/{tenant}/chats/{caseId}/messages   -- send a chat message
//	GET  /v1/{tenant}/chats/{caseId}/events     -- SSE delivery
//	POST /v1/{tenant}/chats/{caseId}/complete   -- end the chat (customer side)
//
// Additive alongside chats.go's four legacy /support/chats routes, which
// stay completely unchanged -- identity-apps' Console needs zero changes
// for this to exist (see this repo's live-chat-SDK extraction plan, Stage
// 1). ChatsHandler, its csm client, its SSE hub, and its in-memory
// h.cases map are all shared between the legacy and v1 surfaces; only the
// authorization model and wire shapes differ (tenant-scoped rather than
// single-pinned-instance/owner-subject-scoped -- see requireTenantCase and
// canonicalOwner below).
package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

// Input limits, all UTF-8 BYTE lengths (len(string), not
// utf8.RuneCountInString) -- matches maxBodyBytes' own byte-based cap
// (http.MaxBytesReader), not a character count. A message full of
// multi-byte characters (emoji, CJK) hits its limit sooner in byte terms
// than in rune terms -- expected and consistent with the whole-body cap it
// sits inside.
const (
	maxSubjectBytes        = 200
	maxPriorMessagesCount  = 20
	maxMessageContentBytes = 4000
	maxTotalMessageBytes   = 32768 // 32 KiB
	maxMetadataKeyCount    = 20
	maxMetadataKeyBytes    = 64
	maxMetadataValueBytes  = 500
)

// reservedMetadataKeys are rejected case-insensitively -- these names are
// either already dedicated top-level fields on the escalate request
// (source/channel/tenant/tenantSlug never being metadata's job to carry)
// or a reserved internal field (projectId, always derived from the
// resolved tenant, never client-supplied).
var reservedMetadataKeys = map[string]bool{
	"source":     true,
	"channel":    true,
	"tenant":     true,
	"tenantslug": true,
	"projectid":  true,
}

// sensitiveMetadataKeySubstrings are rejected wherever they appear inside a
// metadata key, case-insensitively -- defense in depth against a caller
// accidentally (or maliciously) smuggling a credential-shaped value into a
// field that ends up logged/displayed/persisted as plain case metadata.
var sensitiveMetadataKeySubstrings = []string{"token", "secret", "password", "credential", "apikey"}

// validationError is a 400-worthy input problem -- returned by
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

// canonicalOwner returns the caller's Subject claim only -- never falling
// back to Username the way the legacy path's ownerIdentity() does (see that
// function's own doc comment). Every /v1 route calls this and rejects an
// empty result the same way: Subject is the one claim every TokenValidator
// kind (introspection today, JWKS later) normalizes consistently, so it's
// the only claim this generic, multi-IdP API trusts as a caller's identity.
func canonicalOwner(r *http.Request) string {
	return middleware.IdentityFromContext(r.Context()).Subject
}

// requireTenantCase confirms caseID belongs to t, durably. h.cases is a
// fast first-layer check only -- a miss, or a stored tenantSlug that
// disagrees with t.Slug, always falls through to csm-portal/backend's own
// GET /internal/chat/cases/{caseId} (the durable source of truth) before
// rejecting, so a stale, wrong, or simply missing in-memory record (e.g.
// after this bridge process restarted) can never itself cause a false
// accept -- at worst it costs one extra outbound call before a correct
// 403/404. Used by every case-scoped v1 route (stream, sendMessage,
// completeChat) alike; completeChat additionally gets its own SQL-level
// tenantSlug check inside router.Router.EndByTenant, belt-and-suspenders
// specifically on that one irreversible action.
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

	// Backfill/repair -- preserves whatever this process already knew
	// (conversationId/customerEmail, if this same case's HandleEscalateV1
	// ran in this process) while correcting tenantSlug from the durable
	// answer; a genuine cache miss just gets tenantSlug populated, and
	// conversationId/customerEmail stay zero-valued until a caller that
	// needs them (HandleSendMessageV1) supplies conversationId itself --
	// see v1EscalateResponse/v1MessageRequest's own doc comments on why
	// this generic API always round-trips conversationId through the
	// client rather than depending solely on this best-effort cache.
	rec.tenantSlug = ownership.TenantSlug
	h.mu.Lock()
	h.cases[caseID] = rec
	h.mu.Unlock()
	return rec, true
}

// v1PriorMessage mirrors PriorMessage -- kept as a distinct type only so
// this file's request/response shapes are self-contained and don't take on
// an implicit dependency on chats.go's legacy Ask-AI-specific naming.
type v1PriorMessage = PriorMessage

// v1EscalateRequest is the body for POST /v1/{tenant}/chats.
type v1EscalateRequest struct {
	// ConversationID is a stable per-session id the caller already has
	// (e.g. an existing AI-chatbot conversation this escalates from).
	// Optional -- when empty, this bridge mints the same fresh id it uses
	// as CaseID, so ConversationID and CaseID start out equal (mirrors
	// chat-routing-service's own CaseInfo.ConversationID doc comment on
	// why a case-first escalation with no pre-existing conversation does
	// this).
	ConversationID string           `json:"conversationId,omitempty"`
	Subject        string           `json:"subject,omitempty"`
	Message        string           `json:"message"`
	CustomerName   string           `json:"customerName,omitempty"`
	PriorMessages  []v1PriorMessage `json:"priorMessages,omitempty"`
	// Metadata is arbitrary tenant-supplied key/value data, subject to
	// this file's own byte/count limits and reserved/sensitive-key
	// rejection (see validateEscalateInput) -- NOT currently forwarded to
	// chat-routing-service (CaseInfo has no metadata field yet); accepted
	// and validated now so a tenant's integration can start sending it
	// without a breaking wire change later.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// v1EscalateResponse is POST /v1/{tenant}/chats's response. Echoes back
// conversationId (whether client-supplied or bridge-minted) so the caller
// never has to separately remember which case it applied -- every later
// /v1 call for this case carries both ids explicitly instead of relying on
// this bridge's own best-effort in-memory cache (see requireTenantCase's
// own doc comment on why that cache is fast-path-only, never a dependency).
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
		// The resolved canonical Subject (never Username -- see
		// canonicalOwner's own doc comment), recorded here even though
		// requireTenantCase authorizes by tenant membership only, not a
		// per-caller Subject match (see that function's own doc comment --
		// unchanged by this). This durably records, in the one place this
		// codebase already models "who owns this case" (ownerSubject,
		// otherwise only ever set by the legacy path's requireOwner), that
		// a v1 case's owner identity really is the IdP's stable Subject
		// claim, not the customerEmail display field above (which
		// deliberately still prefers the human-readable Username for
		// engineer-UI legibility and stays unchanged here).
		ownerSubject: subject,
	}
	h.mu.Unlock()

	// Never logs the bearer token itself -- only the already-validated,
	// non-sensitive identity claims TokenValidator resolved from it. Useful
	// to confirm, per tenant, which identity-resolution path actually fired
	// (introspection's own "sub" vs. the userinfo fallback both land here
	// identically) without needing a debug endpoint.
	slog.InfoContext(r.Context(), "v1 chat escalation", "tenant", t.Slug, "caseId", caseID, "ownerSubject", subject)

	writeJSON(w, http.StatusAccepted, v1EscalateResponse{CaseID: caseID, ConversationID: conversationID})
}

// v1MessageRequest is the body for
// POST /v1/{tenant}/chats/{caseId}/messages. ConversationID is optional --
// used when this bridge's own in-memory record has no cached value for it
// yet (see requireTenantCase's own doc comment); when both are available,
// the client-supplied value here wins, since it can never be staler than
// this process's own cache.
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

// HandleStreamV1 handles GET /v1/{tenant}/chats/{caseId}/events. Behind
// ResolveTenant, CORS, TenantAuth, RequireSubject. Identical delivery
// mechanism to the legacy HandleStream (same per-case stream.Hub, same SSE
// framing/heartbeat) -- only the authorization check (tenant membership,
// not caller-subject ownership) differs, so the loop itself isn't
// duplicated beyond what's needed to call requireTenantCase instead of
// requireOwner.
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
// POST /v1/{tenant}/chats/{caseId}/complete -- conversationId is only used
// to populate csm-portal/backend's session_closed broadcast to engineers
// (a UI refresh trigger, see that handler's own doc comment), so an absent
// or empty body never blocks ending the session.
type v1CompleteRequest struct {
	ConversationID string `json:"conversationId,omitempty"`
}

// HandleCompleteV1 handles POST /v1/{tenant}/chats/{caseId}/complete -- the
// customer/tenant-initiated session end this bridge's legacy path has no
// equivalent for (an Ask AI admin never ends a chat from that side; only
// the engineer does, via csm-portal/backend's own browser-facing route).
// Behind ResolveTenant, CORS, TenantAuth, RequireSubject.
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
		_ = json.Unmarshal(body, &req) // best-effort -- see v1CompleteRequest's own doc comment
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
