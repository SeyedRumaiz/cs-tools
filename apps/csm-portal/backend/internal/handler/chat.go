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

// This file (plus chat_stream.go and chat_timeout_sweeper.go) implements
// csm-portal/backend's side of the live-engineer-chat feature: browser-
// facing routes for an engineer's own session, and M2M routes other
// services call on a tenant/customer's behalf (see middleware.
// m2mExemptRoutes). Each mutating handler delegates the state transition
// to chat-routing-service (routingService), then delivers the resulting
// event(s) over SSE — see routingclient's "No server-initiated push" note.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/stream"
)

// broadcastHubKey is the stream.BroadcastHub key every connected engineer's
// alert stream registers under, in addition to its own per-engineer key
// (see engineerHubKey). Used for the escalate fallback when the routing
// service is unreachable, and for the customer-message relay during an
// active session.
const broadcastHubKey = "engineers"

// engineerHubKey returns the stream.BroadcastHub key for targeted delivery
// to one engineer. userID is the IdP "userid" claim
// (middleware.UserInfo.UserID).
func engineerHubKey(userID string) string {
	return "engineer:" + userID
}

// maxChatBodyBytes caps request bodies on the chat endpoints below —
// generous for a short escalation/chat-message payload while still bounding
// memory use.
const maxChatBodyBytes = 64 << 10 // 64 KiB

// chatNotifyTimeout bounds the best-effort push to customer-portal/
// backend-v2 (see notifyOrigin) — this must never make an engineer's
// accept/message/complete call hang on backend-v2 being slow or down.
const chatNotifyTimeout = 5 * time.Second

// entityChatClient is the subset of the entity client this feature needs.
type entityChatClient interface {
	PatchCase(ctx context.Context, caseID string, body []byte) ([]byte, error)
}

// ChatEventPusher abstracts internal/chatnotify.Client so tests can fake the
// backend-v2 push.
type ChatEventPusher interface {
	PushEvent(ctx context.Context, payload []byte) error
	// CreateCase is synchronous, unlike PushEvent — the caller needs the
	// new case's ID back. userIDToken is forwarded as x-user-id-token.
	CreateCase(ctx context.Context, payload []byte, userIDToken string) ([]byte, error)
}

// routingService abstracts internal/routingclient.Client so tests can fake
// the chat-routing-service calls.
type routingService interface {
	Escalate(ctx context.Context, ci routingclient.CaseInfo) (routingclient.EscalateResult, error)
	// SetPresence, Completed, Decline, Accept, and GetPresence identify the
	// engineer by their IdP "userid" claim (middleware.UserInfo.UserID).
	SetPresence(ctx context.Context, userID string, status routingclient.Status) (routingclient.PresenceResult, error)
	// Completed takes caseID since an engineer may hold several concurrent
	// conversations at once.
	Completed(ctx context.Context, userID, caseID string) (routingclient.CompletedResult, error)
	Decline(ctx context.Context, userID, caseID string) (routingclient.DeclineResult, error)
	Accept(ctx context.Context, userID, caseID string) (routingclient.AcceptResult, error)
	GetPresence(ctx context.Context, userID string) (routingclient.PresenceDetail, error)
	// SweepTimeouts is polled by ChatHandler.StartTimeoutSweeper. It
	// returns both PENDING-accept timeouts and queue-abandonment results.
	SweepTimeouts(ctx context.Context) (routingclient.SweepResult, error)
	// SetMaxConcurrentChats sets an engineer's concurrent-chat capacity.
	// AssignedCases is populated when raising the limit drains the waiting
	// queue into the newly opened capacity.
	SetMaxConcurrentChats(ctx context.Context, userID string, max int) (routingclient.SetCapacityResult, error)
	// CreateWorkItem must be called before Escalate for the same case; it
	// stores ci as the case's durable display record.
	CreateWorkItem(ctx context.Context, ci routingclient.CaseInfo) error
	AddComment(ctx context.Context, caseID, authorEmail, content string) error
	// GetCaseInfo and ConvertToCase back HandleConvertToCase and are real,
	// error-surfacing calls, unlike this interface's other best-effort
	// side-channel methods.
	GetCaseInfo(ctx context.Context, caseID string) (routingclient.CaseInfo, error)
	ConvertToCase(ctx context.Context, userID, caseID, entityCaseID string) (routingclient.ConvertToCaseResult, error)
	// EndByTenant backs HandleCompleteByTenant: tenant-initiated session
	// completion, with no engineer userID to authorize against.
	EndByTenant(ctx context.Context, caseID, tenantSlug string) (routingclient.CompletedResult, error)
}

// ChatHandler implements the live-engineer-chat escalation endpoints.
type ChatHandler struct {
	entity    entityChatClient
	hub       *stream.BroadcastHub
	notifiers map[string]ChatEventPusher
	routing   routingService
}

// NewChatHandler creates a ChatHandler. hub and routing must both be
// non-nil: live engineer chat has no offline fallback, and HandleEscalate's
// fallback path requires a routing client to have attempted and failed.
func NewChatHandler(entity entityChatClient, hub *stream.BroadcastHub, notifiers map[string]ChatEventPusher, routing routingService) *ChatHandler {
	return &ChatHandler{entity: entity, hub: hub, notifiers: notifiers, routing: routing}
}

// chatEvent is the JSON envelope for every event this feature publishes, to
// both the engineer SSE hub and backend-v2's /internal/chat-events. Not
// every field is set for every Type.
type chatEvent struct {
	Type           string `json:"type"`
	CaseID         string `json:"caseId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	// Source/Channel identify the originating product/surface (e.g.
	// "customer-portal"/"" or "asgardeo"/"ask-ai") and double as the
	// notifyOrigin routing key — see ChatHandler.notifierFor.
	Source        string `json:"source,omitempty"`
	Channel       string `json:"channel,omitempty"`
	Subject       string `json:"subject,omitempty"`
	CustomerEmail string `json:"customerEmail,omitempty"`
	CustomerName  string `json:"customerName,omitempty"`
	EngineerEmail string `json:"engineerEmail,omitempty"`
	Message       string `json:"message,omitempty"`
	// EntityCaseID is set only on a "converted_to_case" event: the
	// entity-service case ID the customer's browser should switch to.
	EntityCaseID string `json:"entityCaseId,omitempty"`
	// PriorMessages is set only on customer_escalation: the customer's
	// AI-chatbot transcript, so the receiving engineer can see prior
	// context. See routingclient.PriorMessage.
	PriorMessages []routingclient.PriorMessage `json:"priorMessages,omitempty"`
	Timestamp     string                       `json:"timestamp"`
}

// publish marshals evt and publishes it on the given hub key. Best-effort:
// stream.BroadcastHub.Publish never blocks and silently drops the event
// for a subscriber whose buffer is full.
func (h *ChatHandler) publish(key string, evt chatEvent) {
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.Error("chat: failed to encode engineer event", "type", evt.Type, "err", err)
		return
	}
	h.hub.Publish(key, string(payload))
}

// publishToEngineers fans evt out to every open GET /chat/alerts/stream
// connection via broadcastHubKey.
func (h *ChatHandler) publishToEngineers(evt chatEvent) {
	h.publish(broadcastHubKey, evt)
}

// publishToEngineer delivers evt only to userID's own SSE subscription (see
// engineerHubKey). userID is the IdP "userid" claim
// (middleware.UserInfo.UserID), as returned directly by
// EscalateResult.EngineerUserID / DeclineResult.ReassignedTo.
func (h *ChatHandler) publishToEngineer(userID string, evt chatEvent) {
	h.publish(engineerHubKey(userID), evt)
}

// defaultNotifySource is the notifiers map key used when a case has no
// Source set (e.g. every customer-portal-originated case).
const defaultNotifySource = "customer-portal"

// notifierFor resolves which downstream service a case's events should be
// pushed to, keyed by source. Falls back to defaultNotifySource for an
// empty or unrecognized source rather than returning nil.
func (h *ChatHandler) notifierFor(source string) ChatEventPusher {
	if source != "" {
		if n, ok := h.notifiers[source]; ok {
			return n
		}
	}
	return h.notifiers[defaultNotifySource]
}

// sourceForCase looks up caseID's originating source (CaseInfo.Source) for
// callers that route notifyOrigin without already holding the case's
// CaseInfo. Best-effort: a lookup failure returns "", which notifierFor
// treats as defaultNotifySource.
func (h *ChatHandler) sourceForCase(ctx context.Context, caseID string) string {
	ci, err := h.routing.GetCaseInfo(ctx, caseID)
	if err != nil {
		slog.WarnContext(ctx, "chat: could not resolve case source, defaulting", "caseID", caseID, "err", err)
		return ""
	}
	return ci.Source
}

// notifyOrigin pushes evt to source's downstream service (see notifierFor),
// best-effort: a failure is logged, not returned, since the case/comment
// write this always follows has already succeeded.
func (h *ChatHandler) notifyOrigin(ctx context.Context, source string, evt chatEvent) {
	notifier := h.notifierFor(source)
	if notifier == nil {
		return
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.Error("chat: failed to encode origin push", "type", evt.Type, "err", err)
		return
	}
	pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), chatNotifyTimeout)
	defer cancel()
	if err := notifier.PushEvent(pushCtx, payload); err != nil {
		slog.Error("chat: push to origin failed", "type", evt.Type, "source", source, "conversationId", evt.ConversationID, "err", err)
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

// assignedCaseEvent builds the customer_escalation chatEvent used to
// deliver ci to the engineer the routing service just assigned it to.
func assignedCaseEvent(ci routingclient.CaseInfo) chatEvent {
	return chatEvent{
		Type:           "customer_escalation",
		CaseID:         ci.CaseID,
		ConversationID: ci.ConversationID,
		ProjectID:      ci.ProjectID,
		Source:         ci.Source,
		Channel:        ci.Channel,
		Subject:        ci.Subject,
		CustomerEmail:  ci.CustomerEmail,
		CustomerName:   ci.CustomerName,
		Message:        ci.Message,
		PriorMessages:  ci.PriorMessages,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}
}

// escalateRequest is the body customer-portal/backend-v2 sends to
// POST /internal/chat/escalate. CustomerName is a display label only, shown
// in the engineer's alert UI — never used for authorization or attribution.
type escalateRequest struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId"`
	// Source/Channel identify the originating product/surface (see
	// chatEvent). Optional: empty Source defaults to "customer-portal".
	Source  string `json:"source,omitempty"`
	Channel string `json:"channel,omitempty"`
	// TenantSlug mirrors routingclient.CaseInfo.TenantSlug, set only by
	// console-chat-bridge's multi-tenant API. Optional.
	TenantSlug    string `json:"tenantSlug,omitempty"`
	Subject       string `json:"subject"`
	CustomerEmail string `json:"customerEmail"`
	CustomerName  string `json:"customerName"`
	Message       string `json:"message"`
	// PriorMessages is the customer's AI-chatbot transcript up to the
	// moment of escalation. Optional. See routingclient.PriorMessage.
	PriorMessages []routingclient.PriorMessage `json:"priorMessages,omitempty"`
}

// HandleEscalate handles POST /internal/chat/escalate, the service-to-
// service endpoint customer-portal/backend-v2 calls (OAuth2 client-
// credentials, not a user session) to start a live-engineer-chat
// escalation. req.CaseID is a fresh UUID minted by the caller for this
// escalation, distinct from req.ConversationID; no entity-service case
// exists yet — creating one is deferred until the engineer converts the
// chat (see HandleConvertToCase). It records the escalation, then asks the
// routing service to route it: targeted delivery to an assigned engineer,
// a queued notification, or — if the routing service is unreachable — a
// broadcast fallback to every connected engineer. Returns 409 if the
// customer already has an open chat for this project, or if the case has
// already ended.
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
		Source:         req.Source,
		Channel:        req.Channel,
		TenantSlug:     req.TenantSlug,
		Subject:        req.Subject,
		CustomerEmail:  req.CustomerEmail,
		CustomerName:   req.CustomerName,
		Message:        req.Message,
		PriorMessages:  req.PriorMessages,
	}

	// Best-effort: creates the work_item/chat_conversation/first-comment
	// record for this escalation before routing. A failure here doesn't
	// block delivery, but ErrDuplicateOpenChat is reported as a real 409
	// since it means the customer already has a live chat for this project.
	if err := h.routing.CreateWorkItem(r.Context(), ci); err != nil {
		if errors.Is(err, routingclient.ErrDuplicateOpenChat) {
			slog.InfoContext(r.Context(), "chat: escalation rejected, customer already has an open live chat", "caseId", req.CaseID, "conversationId", req.ConversationID, "customerEmail", req.CustomerEmail, "projectId", req.ProjectID)
			writeError(w, http.StatusConflict, "You already have a live chat in progress for this project. Please continue in that chat, or wait for it to end before starting a new one.")
			return
		}
		slog.WarnContext(r.Context(), "chat: routing service create work item failed (non-blocking)", "caseId", req.CaseID, "err", err)
	}

	result, err := h.routing.Escalate(r.Context(), ci)
	if err != nil {
		if errors.Is(err, routingclient.ErrCaseAlreadyEnded) {
			// A genuine rejection, not a routing-service outage — must
			// reach the customer as a real failure rather than falling
			// back to broadcast below.
			slog.InfoContext(r.Context(), "chat: escalation rejected, case already ended", "caseId", req.CaseID, "conversationId", req.ConversationID)
			writeError(w, http.StatusConflict, "This chat session has already ended. Please start a new conversation to escalate again.")
			return
		}
		// Routing service unreachable/erroring: broadcast to every
		// connected engineer so the outage doesn't strand the customer.
		slog.ErrorContext(r.Context(), "chat: routing service escalate failed, falling back to broadcast", "caseId", req.CaseID, "err", err)
		h.publishToEngineers(assignedCaseEvent(ci))
		writeJSON(w, http.StatusAccepted, []byte(`{"message":"escalation broadcast to available engineers"}`))
		return
	}

	switch {
	case result.EngineerUserID != "":
		h.publishToEngineer(result.EngineerUserID, assignedCaseEvent(ci))
	case result.Queued:
		h.notifyOrigin(r.Context(), req.Source, chatEvent{
			Type:           "queued",
			CaseID:         req.CaseID,
			ConversationID: req.ConversationID,
			Source:         req.Source,
			Channel:        req.Channel,
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
	// CustomerEmail attributes the persisted comment to its actual sender
	// (see AddComment below and routingService's own doc comment).
	CustomerEmail string `json:"customerEmail"`
}

// HandleCustomerMessage handles POST /internal/chat/customer-message,
// service-to-service only (same auth as HandleEscalate). It persists the
// customer's message as a comment via the routing service, then broadcasts
// it over broadcastHubKey so the accepting engineer sees it live — the
// broadcast is untargeted since this handler doesn't track who accepted.
// Returns 410 if the session has already ended
// (routingclient.ErrConversationEnded); any other persistence failure is
// logged and does not block the relay.
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

	if err := h.routing.AddComment(r.Context(), req.CaseID, req.CustomerEmail, req.Message); err != nil {
		if errors.Is(err, routingclient.ErrConversationEnded) {
			writeError(w, http.StatusGone, "This chat session has already ended.")
			return
		}
		// Best-effort: must not block the live relay below — losing the
		// durable record is far less costly than dropping the message the
		// customer just sent.
		slog.WarnContext(r.Context(), "chat: routing service add comment failed for customer chat message (non-blocking)", "caseID", req.CaseID, "err", err)
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

// HandleAcceptSession handles POST /chat/sessions/{id}/accept, behind Auth.
// {id} is the case ID. It confirms the accept with the routing service
// (PENDING -> BUSY) — the one call in this method that's error-surfacing
// rather than best-effort, since the engineer needs to know if the case
// was already declined, reassigned, or accepted elsewhere. On success it
// also best-effort assigns the case to the engineer via entity's PATCH
// /cases/{id} assigneeEmail; there is no separate chat-session record, the
// case row itself doubles as it. Returns 409 if the accept was not applied.
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

	acceptResult, err := h.routing.Accept(r.Context(), user.UserID, caseID)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service accept failed", "userID", user.UserID, "caseID", caseID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to accept the chat session. Please try again.")
		return
	}
	if !acceptResult.Applied {
		writeError(w, http.StatusConflict, "This chat request is no longer available.")
		return
	}

	patchBody, err := json.Marshal(map[string]string{"assigneeEmail": user.Email})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}
	// Best-effort: entity's Postgres cases table has no assignee column
	// today, so this is expected to fail — Router.Accept above already
	// durably recorded the assignee in the routing service's own tables.
	// Kept in case entity-service ever adds a real assignee column.
	if _, err := h.entity.PatchCase(r.Context(), caseID, patchBody); err != nil {
		slog.WarnContext(r.Context(), "entity PatchCase failed accepting chat session (non-blocking)", "userID", user.UserID, "caseID", caseID, "err", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.publishToEngineers(chatEvent{
		Type:           "session_accepted",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})
	h.notifyOrigin(r.Context(), h.sourceForCase(r.Context(), caseID), chatEvent{
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

// HandleEngineerMessage handles POST /chat/sessions/{id}/messages, behind
// Auth. {id} is the case ID. It persists the engineer's reply via the
// routing service, then relays it to the customer through notifyOrigin.
// Returns 410 if the session has already ended.
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

	// Best-effort, matching HandleCustomerMessage: the engineer's message
	// must still reach the customer via notifyOrigin below even if this
	// persistence call fails. ErrConversationEnded is the one exception —
	// a request already in flight when "End session" is clicked shouldn't
	// silently report success.
	if err := h.routing.AddComment(r.Context(), caseID, user.Email, req.Message); err != nil {
		if errors.Is(err, routingclient.ErrConversationEnded) {
			writeError(w, http.StatusGone, "This chat session has already ended.")
			return
		}
		slog.WarnContext(r.Context(), "chat: routing service add comment failed for engineer chat message (non-blocking)", "userID", user.UserID, "caseID", caseID, "err", err)
	}

	h.notifyOrigin(r.Context(), h.sourceForCase(r.Context(), caseID), chatEvent{
		Type:           "engineer_message",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Message:        req.Message,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})

	writeJSON(w, http.StatusCreated, []byte(`{"message":"sent"}`))
}

// HandleCompleteSession handles POST /chat/sessions/{id}/complete, behind
// Auth. {id} is the case ID. It ends the live session — notifying other
// engineers and the customer's browser — without changing the case's
// state; resolving/closing the case is still a separate, explicit action.
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
	h.notifyOrigin(r.Context(), h.sourceForCase(r.Context(), caseID), chatEvent{
		Type:           "engineer_disconnected",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})

	// Best-effort: releases this engineer's capacity for this case and, if
	// that drains the queue, delivers the next case to them.
	if result, err := h.routing.Completed(r.Context(), user.UserID, caseID); err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service completed failed", "userID", user.UserID, "err", err)
	} else if result.AssignedCase != nil {
		h.publishToEngineer(user.UserID, assignedCaseEvent(*result.AssignedCase))
	}

	writeJSON(w, http.StatusOK, []byte(`{"message":"session ended"}`))
}

// setPresenceRequest is the body an engineer's browser sends for
// POST /engineers/me/status.
type setPresenceRequest struct {
	Status string `json:"status"`
}

// HandleSetPresence handles POST /engineers/me/status, behind Auth. It
// applies the engineer's requested presence change via the routing
// service, which may immediately drain queued cases back to them. Unlike
// most handlers in this file, a routing-service failure here is surfaced
// as 502 rather than logged best-effort, since the engineer needs to know
// the change didn't take effect.
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
	// AVAILABLE, BUSY, and OFFLINE are requestable directly; PENDING is a
	// per-case state, never a top-level status an engineer sets.
	status := routingclient.Status(req.Status)
	switch status {
	case routingclient.StatusAvailable, routingclient.StatusBusy, routingclient.StatusOffline:
		// valid
	default:
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	result, err := h.routing.SetPresence(r.Context(), user.UserID, status)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service set presence failed", "userID", user.UserID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to update your status. Please try again.")
		return
	}

	// Going AVAILABLE can drain more than one queued case at once (capped
	// at this engineer's max_concurrent_chats), unlike Completed/Decline/a
	// timeout which each free at most one slot.
	for _, c := range result.AssignedCases {
		h.publishToEngineer(user.UserID, assignedCaseEvent(c))
	}

	writeJSONValue(w, http.StatusOK, result)
}

// HandleGetPresence handles GET /engineers/me/status, behind Auth. Returns
// the engineer's current presence (defaulting to OFFLINE if never seen)
// along with every case they currently hold, pending or accepted, so the
// browser can rehydrate alerts and session state after a refresh.
func (h *ChatHandler) HandleGetPresence(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	detail, err := h.routing.GetPresence(r.Context(), user.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service get presence failed", "userID", user.UserID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to load your status. Please try again.")
		return
	}

	writeJSONValue(w, http.StatusOK, detail)
}

// setMaxConcurrentChatsRequest is the body an engineer's browser sends for
// PATCH /engineers/me/capacity.
type setMaxConcurrentChatsRequest struct {
	MaxConcurrentChats int `json:"maxConcurrentChats"`
}

// minMaxConcurrentChats/maxMaxConcurrentChats mirror the routing service's
// max_concurrent_chats CHECK constraint, checked here for a clean 400.
const (
	minMaxConcurrentChats = 1
	maxMaxConcurrentChats = 10
)

// HandleSetMaxConcurrentChats handles PATCH /engineers/me/capacity, behind
// Auth. It sets the engineer's concurrent-chat capacity. Lowering the
// limit never drops an in-progress chat, it only stops new work from
// routing to them until they fall back under it. Raising the limit can
// immediately drain queued cases into the newly opened capacity, delivered
// to the engineer the same way HandleSetPresence delivers its own
// queue-drain.
func (h *ChatHandler) HandleSetMaxConcurrentChats(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req setMaxConcurrentChatsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	if req.MaxConcurrentChats < minMaxConcurrentChats || req.MaxConcurrentChats > maxMaxConcurrentChats {
		writeError(w, http.StatusBadRequest, "maxConcurrentChats must be between 1 and 10.")
		return
	}

	result, err := h.routing.SetMaxConcurrentChats(r.Context(), user.UserID, req.MaxConcurrentChats)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service set max concurrent chats failed", "userID", user.UserID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to update your chat capacity. Please try again.")
		return
	}

	// Raising the limit can drain more than one queued case into this
	// engineer's newly opened capacity.
	for _, c := range result.AssignedCases {
		h.publishToEngineer(user.UserID, assignedCaseEvent(c))
	}

	writeJSON(w, http.StatusOK, []byte(`{"applied":true}`))
}

// declineSessionRequest is the body an engineer's browser sends for
// POST /chat/sessions/{id}/decline.
type declineSessionRequest struct {
	ConversationID string `json:"conversationId"`
}

// HandleDeclineSession handles POST /chat/sessions/{id}/decline, behind
// Auth. {id} is the case ID. It hands a routed case back so it can be
// reassigned or requeued instead of stranding the customer with nobody
// seeing their request. If reassigned, the new engineer is notified.
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

	result, err := h.routing.Decline(r.Context(), user.UserID, caseID)
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

// createCaseRequestBody is the payload sent to backend-v2's
// POST /internal/chat/create-case.
type createCaseRequestBody struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId"`
	Subject        string `json:"subject"`
	CustomerEmail  string `json:"customerEmail"`
	CustomerName   string `json:"customerName"`
	Message        string `json:"message"`
}

// createCaseResponseBody is backend-v2's response shape for
// POST /internal/chat/create-case.
type createCaseResponseBody struct {
	EntityCaseID string `json:"entityCaseId"`
}

// HandleConvertToCase handles POST /chat/sessions/{id}/convert-to-case,
// letting the engineer holding a chat turn it into a real case. It fetches
// the chat's stored details, creates the case, then ends the chat session,
// failing the whole request if any step fails to avoid a partially
// converted chat. Only chats sourced from customer-portal can convert
// (console-chat-bridge cases have no account/project to attach a case to);
// any other source returns 409.
func (h *ChatHandler) HandleConvertToCase(w http.ResponseWriter, r *http.Request) {
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

	ci, err := h.routing.GetCaseInfo(r.Context(), caseID)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service get case info failed converting to case", "userID", user.UserID, "caseID", caseID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to look up this chat's details. Please try again.")
		return
	}

	// Rejected up front rather than a wasted call that would otherwise
	// surface as a misleading 502.
	if ci.Source != "" && ci.Source != defaultNotifySource {
		writeError(w, http.StatusConflict, "This chat can't be converted into a case — it didn't originate from the customer portal.")
		return
	}

	createPayload, err := json.Marshal(createCaseRequestBody{
		CaseID:         ci.CaseID,
		ConversationID: ci.ConversationID,
		ProjectID:      ci.ProjectID,
		Subject:        ci.Subject,
		CustomerEmail:  ci.CustomerEmail,
		CustomerName:   ci.CustomerName,
		Message:        ci.Message,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}

	// This request has no customer session to draw a token from, so the
	// engineer's own is forwarded instead; the resulting case is
	// attributed to the engineer, not the original customer.
	engineerToken := entity.UserIDTokenFromContext(r.Context())
	respBody, err := h.notifierFor(ci.Source).CreateCase(r.Context(), createPayload, engineerToken)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: backend-v2 create-case failed converting to case", "userID", user.UserID, "caseID", caseID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to create a case for this chat. Please try again.")
		return
	}
	var created createCaseResponseBody
	if err := json.Unmarshal(respBody, &created); err != nil || created.EntityCaseID == "" {
		slog.ErrorContext(r.Context(), "chat: backend-v2 create-case returned an unusable response", "userID", user.UserID, "caseID", caseID, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to create a case for this chat. Please try again.")
		return
	}

	convertResult, err := h.routing.ConvertToCase(r.Context(), user.UserID, caseID, created.EntityCaseID)
	if err != nil {
		// The case above already exists even though this failed — surface
		// its ID so the engineer can find it manually rather than lose it.
		slog.ErrorContext(r.Context(), "chat: routing service convert to case failed AFTER a real case was already created", "userID", user.UserID, "caseID", caseID, "entityCaseId", created.EntityCaseID, "err", err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Case %s was created, but ending the chat session failed. Please refresh and check the case.", created.EntityCaseID))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.notifyOrigin(r.Context(), ci.Source, chatEvent{
		Type:           "converted_to_case",
		CaseID:         caseID,
		ConversationID: ci.ConversationID,
		EngineerEmail:  user.Email,
		EntityCaseID:   created.EntityCaseID,
		Timestamp:      now,
	})
	if convertResult.AssignedCase != nil {
		h.publishToEngineer(user.UserID, assignedCaseEvent(*convertResult.AssignedCase))
	}

	writeJSONValue(w, http.StatusOK, createCaseResponseBody{EntityCaseID: created.EntityCaseID})
}

// caseOwnershipResponse is GET /internal/chat/cases/{caseId}'s response
// shape.
type caseOwnershipResponse struct {
	TenantSlug string `json:"tenantSlug,omitempty"`
	Source     string `json:"source,omitempty"`
	Channel    string `json:"channel,omitempty"`
}

// HandleGetCaseOwnership handles GET /internal/chat/cases/{caseId}. Not
// browser-facing — authenticated by an OAuth2 client-credentials token
// (see middleware.m2mExemptRoutes). Used by console-chat-bridge's
// requireTenantCase as the durable source of truth for case ownership.
// Responds 404 for both an unknown case and a routing-service failure,
// since either way this endpoint has nothing to confirm.
func (h *ChatHandler) HandleGetCaseOwnership(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	if caseID == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	ci, err := h.routing.GetCaseInfo(r.Context(), caseID)
	if err != nil {
		slog.WarnContext(r.Context(), "chat: get case ownership failed", "caseID", caseID, "err", err)
		writeError(w, http.StatusNotFound, "No case found for this ID.")
		return
	}
	writeJSONValue(w, http.StatusOK, caseOwnershipResponse{
		TenantSlug: ci.TenantSlug,
		Source:     ci.Source,
		Channel:    ci.Channel,
	})
}

// caseHistoryResponse is GET /internal/chat/cases/{caseId}/history's
// response shape.
type caseHistoryResponse struct {
	PriorMessages []routingclient.PriorMessage `json:"priorMessages"`
}

// HandleGetCaseHistory handles GET /internal/chat/cases/{caseId}/history,
// returning caseId's full transcript. Not browser-facing (see
// middleware.m2mExemptRoutes); the caller must have already confirmed the
// requester owns this case, same as HandleGetCaseOwnership's own caller.
// Kept separate from HandleGetCaseOwnership so that endpoint's hot-path
// ownership check never has to pull a potentially large transcript.
func (h *ChatHandler) HandleGetCaseHistory(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	if caseID == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	ci, err := h.routing.GetCaseInfo(r.Context(), caseID)
	if err != nil {
		slog.WarnContext(r.Context(), "chat: get case history failed", "caseID", caseID, "err", err)
		writeError(w, http.StatusNotFound, "No case found for this ID.")
		return
	}
	writeJSONValue(w, http.StatusOK, caseHistoryResponse{
		PriorMessages: ci.PriorMessages,
	})
}

// completeByTenantRequest is the body for POST /internal/chat/complete.
// ConversationID is optional — used only to populate the session_closed
// broadcast event.
type completeByTenantRequest struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId,omitempty"`
	TenantSlug     string `json:"tenantSlug"`
}

// HandleCompleteByTenant handles POST /internal/chat/complete, the
// tenant-initiated counterpart to HandleCompleteSession — lets a case be
// ended from the customer/tenant side, not just by the assigned engineer.
// Not browser-facing — exempt from middleware.Auth, authenticated by an
// OAuth2 client-credentials token. The routing service re-checks
// tenantSlug before ending anything; a mismatched tenant or an
// already-ended case both return 404.
func (h *ChatHandler) HandleCompleteByTenant(w http.ResponseWriter, r *http.Request) {
	body, ok := readChatBody(w, r)
	if !ok {
		return
	}
	var req completeByTenantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.CaseID == "" || req.TenantSlug == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	result, err := h.routing.EndByTenant(r.Context(), req.CaseID, req.TenantSlug)
	if err != nil {
		slog.ErrorContext(r.Context(), "chat: routing service end-by-tenant failed", "caseID", req.CaseID, "tenantSlug", req.TenantSlug, "err", err)
		writeError(w, http.StatusBadGateway, "Failed to end this chat session. Please try again.")
		return
	}
	if !result.Ended {
		writeError(w, http.StatusNotFound, "No open chat session found for this case and tenant.")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.publishToEngineers(chatEvent{
		Type:           "session_closed",
		CaseID:         req.CaseID,
		ConversationID: req.ConversationID,
		Timestamp:      now,
	})
	if result.AssignedCase != nil && result.AssignedEngineerID != "" {
		h.publishToEngineer(result.AssignedEngineerID, assignedCaseEvent(*result.AssignedCase))
	}

	writeJSON(w, http.StatusOK, []byte(`{"message":"session ended"}`))
}
