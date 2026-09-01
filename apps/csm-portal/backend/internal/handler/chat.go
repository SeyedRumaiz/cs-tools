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

// Package handler — this file implements the live-engineer-chat escalation
// feature: a customer in the Novera AI chat (customer-portal) asks for a
// human, and an available engineer here (csm-portal) picks it up.
//
// Design, and why it looks the way it does:
//
//   - A "chat message" during a live session is just a case_comment on the
//     case the escalation created — there is no new chat/message table. See
//     HandleEngineerMessage.
//   - "Accepting" a session is just PATCH /cases/{id} with assigneeEmail —
//     no separate claim/lock table, no SELECT ... FOR UPDATE SKIP LOCKED.
//     First engineer to click Accept wins via a normal update. At this
//     project's current scale (a handful of engineers) the race this could
//     lose to essentially never happens; if it ever matters, atomic claiming
//     is a small, isolated follow-up.
//   - Which case a browser message belongs to travels as an explicit
//     conversationId/caseId pair on every request — this handler keeps no
//     server-side session table mapping one to the other. customer-portal's
//     escalate call already knows both (it just created the case), and
//     every subsequent engineer-side call carries the conversationId the
//     original SSE alert included.
//   - Engineer presence for "is anyone connected at all" still has no
//     heartbeat/presence table — that's still derived from who has
//     GET /chat/alerts/stream open (see chat_stream.go). What an engineer's
//     dropdown shows (Available/Busy/Offline), though, is now real state,
//     owned by the standalone chat-routing-service (see
//     internal/routingclient) rather than inferred.
//
// Delivery of a routed event to a specific engineer, and the broadcast
// fallback:
//
//   - Customer → engineers: an escalation is routed to exactly one engineer
//     by the routing service (internal/routingclient.Client.Escalate) and
//     delivered over that engineer's own SSE subscription key (see
//     engineerHubKey, chat_stream.go). If the routing service is down or
//     errors, HandleEscalate falls back to the pre-routing behavior —
//     publishing on the shared broadcastHubKey every connected engineer's
//     stream also subscribes to (see StreamEngineerAlerts's dual
//     registration) — so a routing-service outage doesn't strand the
//     customer with nobody ever seeing their request.
//   - Engineer → customer: relayed by pushing into customer-portal/
//     backend-v2's already-open per-conversation WebSocket (see
//     internal/chatnotify), because that connection already exists for the
//     life of the chat — there is no reason to also fan this out over SSE.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/stream"
)

// broadcastHubKey is the stream.BroadcastHub subscription key every
// connected engineer's alert stream registers under IN ADDITION TO its own
// per-engineer key (see engineerHubKey, StreamEngineerAlerts's dual
// registration). It now serves only two purposes: the escalate fallback
// path when the routing service is unreachable (see HandleEscalate), and
// HandleCustomerMessage's mid-session relay, which is still broadcast to
// every connected engineer rather than targeted (an accepted scope decision
// for this prototype phase — see this file's package doc comment).
const broadcastHubKey = "engineers"

// engineerHubKey returns the stream.BroadcastHub subscription key a
// specific engineer's alert stream registers under for targeted delivery —
// e.g. an escalation the routing service assigned to exactly this engineer.
func engineerHubKey(email string) string {
	return "engineer:" + email
}

// maxChatBodyBytes caps request bodies on the chat endpoints below —
// generous for a short escalation/chat-message payload while still bounding
// memory use.
const maxChatBodyBytes = 64 << 10 // 64 KiB

// chatNotifyTimeout bounds the best-effort push to customer-portal/
// backend-v2 (see notifyBackendV2) — this must never make an engineer's
// accept/message/complete call hang on backend-v2 being slow or down.
const chatNotifyTimeout = 5 * time.Second

// entityChatClient is the subset of the entity client this feature needs.
// customerEntityClient (cmd/server/main.go) already implements this — see
// internal/entity/customer.go's CreateCaseComment/PatchCase.
type entityChatClient interface {
	PatchCase(ctx context.Context, caseID string, body []byte) ([]byte, error)
	CreateCaseComment(ctx context.Context, caseID string, body []byte) ([]byte, error)
}

// chatEventPusher abstracts internal/chatnotify.Client so tests can fake the
// backend-v2 push.
type chatEventPusher interface {
	PushEvent(ctx context.Context, payload []byte) error
}

// routingService abstracts internal/routingclient.Client so tests can fake
// the chat-routing-service calls. Signatures match that client's own
// methods exactly, so *routingclient.Client satisfies this with no adapter.
type routingService interface {
	Escalate(ctx context.Context, ci routingclient.CaseInfo) (routingclient.EscalateResult, error)
	SetPresence(ctx context.Context, email, engineerID string, status routingclient.Status) (routingclient.PresenceResult, error)
	Completed(ctx context.Context, email string) (routingclient.CompletedResult, error)
	Decline(ctx context.Context, email, caseID string) (routingclient.DeclineResult, error)
	GetPresence(ctx context.Context, email string) (routingclient.Status, error)
}

// ChatHandler implements the live-engineer-chat escalation endpoints.
type ChatHandler struct {
	entity  entityChatClient
	hub     *stream.BroadcastHub
	notify  chatEventPusher
	routing routingService
}

// NewChatHandler creates a ChatHandler. hub must be non-nil — unlike
// CaseHandler's activity hub (only built when Event Hub is configured), the
// hub this feature uses is unconditional (see cmd/server/main.go): live
// engineer chat has no offline fallback, so there is no meaningful
// "hub == nil, degrade gracefully" mode to support here the way
// StreamCaseActivities has. routing must also be non-nil: unlike notify
// (whose failures are all best-effort), HandleEscalate's fallback path
// still needs a routing client to have attempted and failed, not a nil one.
func NewChatHandler(entity entityChatClient, hub *stream.BroadcastHub, notify chatEventPusher, routing routingService) *ChatHandler {
	return &ChatHandler{entity: entity, hub: hub, notify: notify, routing: routing}
}

// chatEvent is the single JSON envelope used for every event this feature
// publishes, both to the engineer SSE hub and to backend-v2's
// /internal/chat-events. Not every field applies to every Type — see the
// doc comment on each Handle* method for which ones it sets.
type chatEvent struct {
	Type           string `json:"type"`
	CaseID         string `json:"caseId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	Subject        string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	EngineerEmail  string `json:"engineerEmail,omitempty"`
	Message        string `json:"message,omitempty"`
	Timestamp      string `json:"timestamp"`
}

// publish marshals evt and publishes it on the given hub key. Best-effort
// by construction — stream.BroadcastHub.Publish never blocks and silently
// drops for a subscriber whose buffer is full (see that type's doc
// comment) — so this never fails a caller-facing request.
func (h *ChatHandler) publish(key string, evt chatEvent) {
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.Error("chat: failed to encode engineer event", "type", evt.Type, "err", err)
		return
	}
	h.hub.Publish(key, string(payload))
}

// publishToEngineers fans evt out to every open GET /chat/alerts/stream
// connection via the shared broadcastHubKey. See that constant's doc
// comment for the two remaining cases this is used for.
func (h *ChatHandler) publishToEngineers(evt chatEvent) {
	h.publish(broadcastHubKey, evt)
}

// publishToEngineer delivers evt only to the named engineer's own SSE
// subscription (see engineerHubKey) — used for every routing-service-backed
// delivery: a fresh routed escalation, a queue-drain assignment on
// presence/session-completion, or a reassignment after a decline.
func (h *ChatHandler) publishToEngineer(email string, evt chatEvent) {
	h.publish(engineerHubKey(email), evt)
}

// notifyBackendV2 pushes evt to customer-portal/backend-v2's
// /internal/chat-events, best-effort. A failure here is logged, not
// returned to the caller: the case/comment write this always follows
// already succeeded, and the customer falls back to their existing
// manual-refresh behaviour for this one event rather than seeing an
// otherwise-successful action reported as failed.
func (h *ChatHandler) notifyBackendV2(ctx context.Context, evt chatEvent) {
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.Error("chat: failed to encode backend-v2 push", "type", evt.Type, "err", err)
		return
	}
	pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), chatNotifyTimeout)
	defer cancel()
	if err := h.notify.PushEvent(pushCtx, payload); err != nil {
		slog.Error("chat: push to backend-v2 failed", "type", evt.Type, "conversationId", evt.ConversationID, "err", err)
	}
}

// readChatBody caps and reads a chat-endpoint request body, matching the
// MaxBytesReader/io.ReadAll/json.Valid convention used throughout this
// package (see cases.go).
func readChatBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if _, isTooLarge := err.(*http.MaxBytesError); isTooLarge {
			writeError(w, http.StatusRequestEntityTooLarge, ErrMsgTooLarge)
			return nil, false
		}
		writeError(w, http.StatusBadRequest, errMsgReadBody)
		return nil, false
	}
	if !json.Valid(body) {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return nil, false
	}
	return body, true
}

// assignedCaseEvent builds the customer_escalation-shaped chatEvent used to
// deliver ci to whichever engineer the routing service just assigned it
// to — shared by HandleEscalate, HandleSetPresence, HandleCompleteSession,
// and HandleDeclineSession, since a fresh escalation and a queue-drain/
// reassignment all look identical to the receiving engineer's browser.
func assignedCaseEvent(ci routingclient.CaseInfo) chatEvent {
	return chatEvent{
		Type:           "customer_escalation",
		CaseID:         ci.CaseID,
		ConversationID: ci.ConversationID,
		ProjectID:      ci.ProjectID,
		Subject:        ci.Subject,
		CustomerEmail:  ci.CustomerEmail,
		CustomerName:   ci.CustomerName,
		Message:        ci.Message,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}
}

// escalateRequest is the body customer-portal/backend-v2 sends to
// POST /internal/chat/escalate. CustomerName is a display label only — it is
// never used for authorization or attribution in a durable record (the case
// itself already records its own createdBy from the CreateCase call
// backend-v2 made against entity-service directly), only shown in the
// engineer's alert UI.
type escalateRequest struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId"`
	Subject        string `json:"subject"`
	CustomerEmail  string `json:"customerEmail"`
	CustomerName   string `json:"customerName"`
	Message        string `json:"message"`
}

// HandleEscalate handles POST /internal/chat/escalate. Not browser-facing —
// registered only on the internal listener behind middleware.InternalToken
// (see cmd/server/main.go), called exclusively by customer-portal/backend-v2
// once it has already created the case against entity-service directly
// (backend-v2 owns case creation for this flow because only it has the
// deployment/deployed-product context a case requires — see that backend's
// own escalation handler's doc comment). This handler's only job is to ask
// the routing service which engineer (if any) should get this case, and
// deliver it accordingly; it does not call entity-service at all.
func (h *ChatHandler) HandleEscalate(w http.ResponseWriter, r *http.Request) {
	body, ok := readChatBody(w, r)
	if !ok {
		return
	}

	var req escalateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	if req.CaseID == "" || req.ConversationID == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	slog.InfoContext(r.Context(), "chat escalation received", "caseId", req.CaseID, "conversationId", req.ConversationID)

	ci := routingclient.CaseInfo{
		CaseID:         req.CaseID,
		ConversationID: req.ConversationID,
		ProjectID:      req.ProjectID,
		Subject:        req.Subject,
		CustomerEmail:  req.CustomerEmail,
		CustomerName:   req.CustomerName,
		Message:        req.Message,
	}

	result, err := h.routing.Escalate(r.Context(), ci)
	if err != nil {
		// Routing service unreachable/erroring: fall back to broadcasting
		// to every connected engineer so this brand-new service being down
		// does not strand the customer (see the package doc comment and
		// StreamEngineerAlerts's dual registration).
		slog.ErrorContext(r.Context(), "chat: routing service escalate failed, falling back to broadcast", "caseId", req.CaseID, "err", err)
		h.publishToEngineers(assignedCaseEvent(ci))
		writeJSON(w, http.StatusAccepted, []byte(`{"message":"escalation broadcast to available engineers"}`))
		return
	}

	switch {
	case result.EngineerEmail != "":
		h.publishToEngineer(result.EngineerEmail, assignedCaseEvent(ci))
	case result.Queued:
		h.notifyBackendV2(r.Context(), chatEvent{
			Type:           "queued",
			CaseID:         req.CaseID,
			ConversationID: req.ConversationID,
			Message:        fmt.Sprintf("You're #%d in the queue. An engineer will be with you shortly.", result.Position),
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusAccepted, []byte(`{"message":"escalation routed"}`))
}

// customerMessageRequest is the body backend-v2 sends to
// POST /internal/chat/customer-message whenever the customer sends a
// message while a human is already connected.
type customerMessageRequest struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
}

// HandleCustomerMessage handles POST /internal/chat/customer-message. Also
// internal-listener-only (see HandleEscalate). Persists the customer's
// message as a case comment — the entity-service write of record for this
// feature (see the package doc comment) — then fans it out over the shared
// broadcastHubKey so whichever engineer accepted the session sees it live.
// Deliberately still broadcast rather than targeted at the accepting
// engineer specifically (this handler has no record of who that is — see
// the package doc comment on why there is no server-side session table);
// an accepted scope decision for this prototype phase.
func (h *ChatHandler) HandleCustomerMessage(w http.ResponseWriter, r *http.Request) {
	body, ok := readChatBody(w, r)
	if !ok {
		return
	}

	var req customerMessageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	if req.CaseID == "" || req.ConversationID == "" || req.Message == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	commentBody, err := json.Marshal(map[string]string{"type": "comment", "content": req.Message})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}
	if _, err := h.entity.CreateCaseComment(r.Context(), req.CaseID, commentBody); err != nil {
		slog.ErrorContext(r.Context(), "entity CreateCaseComment failed for customer chat message", "caseID", req.CaseID, "err", err)
		mapUpstreamErrorGeneric(w, err, "Failed to relay message to the engineer.")
		return
	}

	h.publishToEngineers(chatEvent{
		Type:           "customer_message",
		CaseID:         req.CaseID,
		ConversationID: req.ConversationID,
		Message:        req.Message,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})

	writeJSON(w, http.StatusCreated, []byte(`{"message":"relayed"}`))
}

// sessionActionRequest is the body an engineer's browser sends for
// accept/complete — it carries only the conversationId this case's
// escalation was created from, since the case ID is already the {id} path
// parameter.
type sessionActionRequest struct {
	ConversationID string `json:"conversationId"`
}

// HandleAcceptSession handles POST /chat/sessions/{id}/accept —
// browser-facing, behind the normal Auth middleware. {id} is the case ID.
// Assigns the case to the authenticated engineer via the same PATCH
// /cases/{id} + assigneeEmail path csm-portal's case detail page already
// uses (see cases.go's PatchCase / entity's assigneeEmail field) — there is
// no separate "chat session" record; the case IS the session's record.
// Unchanged by the routing-service work: the routing service already
// reserved this engineer's capacity at assignment time (see HandleEscalate/
// HandleSetPresence/HandleCompleteSession), so accepting stays a plain
// entity-service PATCH with no routing call of its own.
func (h *ChatHandler) HandleAcceptSession(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	caseID := r.PathValue("id")
	if caseID == "" || !uuidRe.MatchString(caseID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req sessionActionRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ConversationID == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	patchBody, err := json.Marshal(map[string]string{"assigneeEmail": user.Email})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}
	if _, err := h.entity.PatchCase(r.Context(), caseID, patchBody); err != nil {
		slog.ErrorContext(r.Context(), "entity PatchCase failed accepting chat session", "userID", user.UserID, "caseID", caseID, "err", err)
		mapUpstreamError(w, err, "Failed to accept the chat session.")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.publishToEngineers(chatEvent{
		Type:           "session_accepted",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})
	h.notifyBackendV2(r.Context(), chatEvent{
		Type:           "engineer_assigned",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})

	writeJSON(w, http.StatusOK, []byte(`{"message":"session accepted"}`))
}

// engineerMessageRequest is the body an engineer's browser sends when
// replying in an accepted chat session.
type engineerMessageRequest struct {
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
}

// HandleEngineerMessage handles POST /chat/sessions/{id}/messages —
// browser-facing, behind Auth. {id} is the case ID. Persists the engineer's
// reply as a case comment (the same write CreateCaseComment already makes
// for every other case comment — see cases.go), then relays it to the
// customer's already-open WebSocket via backend-v2's internal push (see the
// package doc comment on why this direction uses a push instead of SSE).
func (h *ChatHandler) HandleEngineerMessage(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	caseID := r.PathValue("id")
	if caseID == "" || !uuidRe.MatchString(caseID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req engineerMessageRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ConversationID == "" || req.Message == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	commentBody, err := json.Marshal(map[string]string{"type": "comment", "content": req.Message})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}
	if _, err := h.entity.CreateCaseComment(r.Context(), caseID, commentBody); err != nil {
		slog.ErrorContext(r.Context(), "entity CreateCaseComment failed for engineer chat message", "userID", user.UserID, "caseID", caseID, "err", err)
		mapUpstreamErrorGeneric(w, err, "Failed to send message.")
		return
	}

	h.notifyBackendV2(r.Context(), chatEvent{
		Type:           "engineer_message",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Message:        req.Message,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})

	writeJSON(w, http.StatusCreated, []byte(`{"message":"sent"}`))
}

// HandleCompleteSession handles POST /chat/sessions/{id}/complete —
// browser-facing, behind Auth. {id} is the case ID. Ends the live session:
// notifies other engineers (so a stale "accepted" alert clears) and the
// customer's browser (so it drops back to AI-only chat). Deliberately does
// NOT change the case's state — ending a live chat session is not the same
// as resolving/closing the case, which the engineer still does explicitly
// through the normal case detail page if and when the underlying issue is
// actually resolved.
func (h *ChatHandler) HandleCompleteSession(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	caseID := r.PathValue("id")
	if caseID == "" || !uuidRe.MatchString(caseID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req sessionActionRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ConversationID == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.publishToEngineers(chatEvent{
		Type:           "session_closed",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})
	h.notifyBackendV2(r.Context(), chatEvent{
		Type:           "engineer_disconnected",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})

	// Best-effort: release this engineer's routing-service capacity and, if
	// that immediately drained the waiting queue, deliver the next case to
	// them the same way a fresh escalation would arrive. A failure here is
	// logged, not surfaced to the caller — the session has already ended
	// successfully from the engineer's point of view, matching this
	// handler's existing best-effort treatment of notifyBackendV2 above.
	if result, err := h.routing.Completed(r.Context(), user.Email); err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service completed failed", "userID", user.UserID, "err", err)
	} else if result.AssignedCase != nil {
		h.publishToEngineer(user.Email, assignedCaseEvent(*result.AssignedCase))
	}

	writeJSON(w, http.StatusOK, []byte(`{"message":"session ended"}`))
}

// setPresenceRequest is the body an engineer's browser sends for
// POST /engineers/me/status.
type setPresenceRequest struct {
	Status string `json:"status"`
}

// HandleSetPresence handles POST /engineers/me/status — browser-facing,
// behind Auth. Applies the authenticated engineer's requested presence
// change via the routing service (see internal/routingclient and that
// service's router.Router.SetPresence for the full state machine this can
// trigger, including an immediate queue-drain assignment back to this same
// engineer). A routing-service failure here is surfaced as 502, not
// best-effort-logged like most of this file's other Handle* methods:
// unlike a chat message or a completion notice, the engineer needs to know
// their requested status change did not actually take effect.
func (h *ChatHandler) HandleSetPresence(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req setPresenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	status := routingclient.Status(req.Status)
	switch status {
	case routingclient.StatusAvailable, routingclient.StatusBusy, routingclient.StatusOffline:
		// valid
	default:
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	result, err := h.routing.SetPresence(r.Context(), user.Email, user.UserID, status)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service set presence failed", "userID", user.UserID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to update your status. Please try again.")
		return
	}

	if result.AssignedCase != nil {
		h.publishToEngineer(user.Email, assignedCaseEvent(*result.AssignedCase))
	}

	writeJSONValue(w, http.StatusOK, result)
}

// HandleGetPresence handles GET /engineers/me/status — browser-facing,
// behind Auth. Lets the status dropdown initialize correctly on load/reload
// instead of assuming a default itself — the routing service's own default
// for an engineer it has never seen (OFFLINE — see router.Router.
// GetPresence) is exposed here rather than hardcoded a second time in the
// browser.
func (h *ChatHandler) HandleGetPresence(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	status, err := h.routing.GetPresence(r.Context(), user.Email)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service get presence failed", "userID", user.UserID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to load your status. Please try again.")
		return
	}

	writeJSONValue(w, http.StatusOK, map[string]routingclient.Status{"status": status})
}

// declineSessionRequest is the body an engineer's browser sends for
// POST /chat/sessions/{id}/decline.
type declineSessionRequest struct {
	ConversationID string `json:"conversationId"`
}

// HandleDeclineSession handles POST /chat/sessions/{id}/decline —
// browser-facing, behind Auth. {id} is the case ID. Unlike Accept/Complete,
// this endpoint exists purely because of the routing service: once
// escalations are routed to exactly one engineer instead of broadcast to
// all of them, an engineer dismissing an alert before accepting it must
// explicitly hand the case back, or it would otherwise strand the customer
// with nobody ever seeing their request again — see
// internal/routingclient.Client.Decline and chat-routing-service's own
// router.Router.Decline doc comment for the full reassign-or-requeue
// behavior this triggers.
func (h *ChatHandler) HandleDeclineSession(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	caseID := r.PathValue("id")
	if caseID == "" || !uuidRe.MatchString(caseID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req declineSessionRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ConversationID == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	result, err := h.routing.Decline(r.Context(), user.Email, caseID)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service decline failed", "userID", user.UserID, "caseID", caseID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to decline the chat session. Please try again.")
		return
	}

	if result.ReassignedTo != "" && result.AssignedCase != nil {
		h.publishToEngineer(result.ReassignedTo, assignedCaseEvent(*result.AssignedCase))
	}

	writeJSON(w, http.StatusOK, []byte(`{"message":"session declined"}`))
}
