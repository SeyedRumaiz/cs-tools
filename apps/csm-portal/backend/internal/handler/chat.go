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
//   - A "chat message" during a live session is persisted via LOCAL
//     STAND-IN routing-service tables (work_item/chat_conversation/comment
//     inside chat-routing-service's own database — see routingService's
//     AddComment/CreateWorkItem and that service's internal/router/
//     workitem.go) rather than entity-service, since entity-service's real
//     generic work-item schema isn't ready yet (see the project's
//     chat-persistence-mapping-plan.md doc) and entity's CreateCaseComment
//     never worked for the customer's half of the conversation anyway (no
//     x-user-id-token on that internal call). See HandleCustomerMessage/
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
//     escalate call already knows both: no entity-service case exists yet
//     under chat-first escalation, so it sends its own conversationId as
//     caseId too (the two stay equal until conversion — see
//     HandleConvertToCase and backend-v2's own escalation handler). Every
//     subsequent engineer-side call carries the conversationId the original
//     SSE alert included.
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
// userID is the IdP "userid" claim (middleware.UserInfo.UserID) -- this key
// used to be built from the engineer's email; switched to userID alongside
// chat-routing-service's own engineer-identity switch (see that service's
// migrations/000014_rename_engineer_status_table) so both sides agree on
// which key a given engineer's events land on.
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
// customerEntityClient (cmd/server/main.go) already implements this — see
// internal/entity/customer.go's PatchCase. CreateCaseComment used to be
// part of this (both message directions persisted through it) but both
// HandleCustomerMessage and HandleEngineerMessage now go through the LOCAL
// STAND-IN routingService.AddComment instead (see that interface's own doc
// comment) -- entity's CreateCaseComment is no longer called by this file.
type entityChatClient interface {
	PatchCase(ctx context.Context, caseID string, body []byte) ([]byte, error)
}

// ChatEventPusher abstracts internal/chatnotify.Client so tests can fake the
// backend-v2 push.
type ChatEventPusher interface {
	PushEvent(ctx context.Context, payload []byte) error
	// CreateCase calls backend-v2's create-case endpoint synchronously
	// (unlike PushEvent) since the caller needs the new case's ID back.
	// userIDToken is forwarded as x-user-id-token because this internal
	// route has no end-user session of its own to derive one from.
	CreateCase(ctx context.Context, payload []byte, userIDToken string) ([]byte, error)
}

// routingService abstracts internal/routingclient.Client so tests can fake
// the chat-routing-service calls. Signatures match that client's own
// methods exactly, so *routingclient.Client satisfies this with no adapter.
type routingService interface {
	Escalate(ctx context.Context, ci routingclient.CaseInfo) (routingclient.EscalateResult, error)
	// SetPresence/Completed/Decline/Accept/GetPresence identify the engineer
	// by their IdP "userid" claim (middleware.UserInfo.UserID) -- see
	// chat-routing-service's migrations/000014_rename_engineer_status_table
	// for why this switched from an email.
	SetPresence(ctx context.Context, userID string, status routingclient.Status) (routingclient.PresenceResult, error)
	// Completed takes caseID because an engineer can now hold several
	// concurrent conversations at once (see the 2026-09-10
	// concurrent-chat-capacity change) -- ending one must say which.
	Completed(ctx context.Context, userID, caseID string) (routingclient.CompletedResult, error)
	Decline(ctx context.Context, userID, caseID string) (routingclient.DeclineResult, error)
	Accept(ctx context.Context, userID, caseID string) (routingclient.AcceptResult, error)
	GetPresence(ctx context.Context, userID string) (routingclient.PresenceDetail, error)
	// SweepTimeouts is polled periodically by ChatHandler.StartTimeoutSweeper
	// (see that method's doc comment) -- not called from any HTTP handler in
	// this file directly. Returns both PENDING-accept timeouts and
	// queue-abandonment results (see routingclient.SweepResult) -- the
	// 2026-09-10 fix for stale, never-assigned escalations sitting in the
	// queue forever and later ambushing whichever engineer next went
	// AVAILABLE (see that type's own doc comment).
	SweepTimeouts(ctx context.Context) (routingclient.SweepResult, error)
	// SetMaxConcurrentChats lets an engineer set their own configurable
	// concurrent-chat capacity (see HandleSetMaxConcurrentChats) -- added
	// alongside the queue-abandonment fix above so raising a specific
	// engineer's limit no longer requires a manual DB UPDATE. Returns
	// AssignedCases when raising the limit immediately drained the waiting
	// queue into this engineer's newly-opened capacity -- see
	// routingclient.SetCapacityResult's own doc comment.
	SetMaxConcurrentChats(ctx context.Context, userID string, max int) (routingclient.SetCapacityResult, error)
	// CreateWorkItem and AddComment are LOCAL STAND-IN persistence calls
	// (see routingclient.Client.CreateWorkItem's doc comment and the
	// project's chat-persistence-mapping-plan.md) -- they exist only until
	// entity-service's real generic work_item/chat_conversation/comment
	// schema ships, at which point these calls move there instead.
	//
	// CreateWorkItem now takes the full CaseInfo (rather than four loose
	// fields) since it durably stores that value as the case's display
	// blob, and must be called BEFORE Escalate for the same case -- see
	// HandleEscalate below and routingclient.Client.CreateWorkItem's own
	// doc comment for why the order matters now.
	CreateWorkItem(ctx context.Context, ci routingclient.CaseInfo) error
	AddComment(ctx context.Context, caseID, authorEmail, content string) error
	// GetCaseInfo and ConvertToCase back HandleConvertToCase (the
	// chat-first-escalation flow's engineer-initiated conversion, see that
	// method's own doc comment) -- both real, error-surfacing calls, not
	// best-effort like most of this interface's other side-channel methods.
	GetCaseInfo(ctx context.Context, caseID string) (routingclient.CaseInfo, error)
	ConvertToCase(ctx context.Context, userID, caseID, entityCaseID string) (routingclient.ConvertToCaseResult, error)
}

// ChatHandler implements the live-engineer-chat escalation endpoints.
type ChatHandler struct {
	entity    entityChatClient
	hub       *stream.BroadcastHub
	notifiers map[string]ChatEventPusher
	routing   routingService
}

// NewChatHandler creates a ChatHandler. hub must be non-nil — unlike
// CaseHandler's activity hub (only built when Event Hub is configured), the
// hub this feature uses is unconditional (see cmd/server/main.go): live
// engineer chat has no offline fallback, so there is no meaningful
// "hub == nil, degrade gracefully" mode to support here the way
// StreamCaseActivities has. routing must also be non-nil: unlike notify
// (whose failures are all best-effort), HandleEscalate's fallback path
// still needs a routing client to have attempted and failed, not a nil one.
func NewChatHandler(entity entityChatClient, hub *stream.BroadcastHub, notifiers map[string]ChatEventPusher, routing routingService) *ChatHandler {
	return &ChatHandler{entity: entity, hub: hub, notifiers: notifiers, routing: routing}
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
	// Source/Channel identify which product/surface this case originated
	// from (e.g. "customer-portal"/"" for the existing Novera flow,
	// "asgardeo"/"ask-ai" for an escalation raised from identity-apps'
	// Ask AI panel). Also doubles as the notifyOrigin routing key -- see
	// ChatHandler.notifierFor.
	Source         string `json:"source,omitempty"`
	Channel        string `json:"channel,omitempty"`
	Subject        string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	EngineerEmail  string `json:"engineerEmail,omitempty"`
	Message        string `json:"message,omitempty"`
	// EntityCaseID is set only on a "converted_to_case" event (see
	// HandleConvertToCase) -- the real entity-service case ID the customer's
	// browser should point to now that this chat has ended.
	EntityCaseID string `json:"entityCaseId,omitempty"`
	// PriorMessages is set only on customer_escalation -- the customer's
	// AI-chatbot (Novera) transcript snapshotted at escalation time, so the
	// receiving engineer's browser can seed the chat with that context
	// instead of starting cold. See routingclient.PriorMessage.
	PriorMessages []routingclient.PriorMessage `json:"priorMessages,omitempty"`
	Timestamp     string                       `json:"timestamp"`
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
// presence/session-completion, or a reassignment after a decline. userID is
// the IdP "userid" claim (middleware.UserInfo.UserID) -- the routing
// service's own EscalateResult.EngineerUserID/DeclineResult.ReassignedTo
// already return this value directly (see chat-routing-service's
// migrations/000014_rename_engineer_status_table).
func (h *ChatHandler) publishToEngineer(userID string, evt chatEvent) {
	h.publish(engineerHubKey(userID), evt)
}

// defaultNotifySource is the notifiers map key used whenever a case has no
// Source set -- every case created before this field existed, and every
// customer-portal-originated case going forward (that flow never sets
// Source). Keeping this as the fallback means the pre-existing single-
// target ("always push to backend-v2") behavior is unchanged for every
// caller that predates this field.
const defaultNotifySource = "customer-portal"

// notifierFor resolves which downstream service a case's events should be
// pushed to, keyed by CaseInfo.Source/chatEvent.Source. Falls back to
// defaultNotifySource for an empty or unrecognized source (e.g. the
// notifiers map has no entry for it, such as a deployment that hasn't
// configured the console-chat-bridge target) rather than returning nil,
// so a misconfigured/unknown source degrades to today's behavior instead
// of silently dropping the event.
func (h *ChatHandler) notifierFor(source string) ChatEventPusher {
	if source != "" {
		if n, ok := h.notifiers[source]; ok {
			return n
		}
	}
	return h.notifiers[defaultNotifySource]
}

// sourceForCase looks up which channel originated caseID (CaseInfo.Source)
// so a call site that doesn't already have the case's CaseInfo on hand
// (HandleAcceptSession, HandleEngineerMessage, HandleCompleteSession) can
// still route notifyOrigin correctly. Best-effort: any lookup failure
// returns "", which notifierFor treats as defaultNotifySource -- the same
// target these call sites always used before Source existed.
func (h *ChatHandler) sourceForCase(ctx context.Context, caseID string) string {
	ci, err := h.routing.GetCaseInfo(ctx, caseID)
	if err != nil {
		slog.WarnContext(ctx, "chat: could not resolve case source, defaulting", "caseID", caseID, "err", err)
		return ""
	}
	return ci.Source
}

// notifyOrigin pushes evt to whichever downstream service source's cases
// are pushed to (see notifierFor), best-effort. A failure here is logged,
// not returned to the caller: the case/comment write this always follows
// already succeeded, and that surface falls back to its own existing
// manual-refresh behaviour for this one event rather than seeing an
// otherwise-successful action reported as failed. Renamed from this
// package's original notifyBackendV2 now that backend-v2 is one of
// possibly several notify targets rather than the only one -- see
// ChatHandler.notifiers.
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
// POST /internal/chat/escalate. CustomerName is a display label only — it is
// never used for authorization or attribution in a durable record. No
// entity-service case exists yet at this point (chat-first escalation
// defers case creation until the engineer converts, see
// HandleConvertToCase) -- CustomerName is shown only in the engineer's
// alert UI.
type escalateRequest struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId"`
	// Source/Channel identify the originating product/surface (see
	// chatEvent's own doc comment on these two fields). Optional: an empty
	// Source is treated as "customer-portal", the only source that existed
	// before this field did, so every existing caller keeps working
	// unchanged.
	Source         string `json:"source,omitempty"`
	Channel        string `json:"channel,omitempty"`
	Subject        string `json:"subject"`
	CustomerEmail  string `json:"customerEmail"`
	CustomerName   string `json:"customerName"`
	Message        string `json:"message"`
	// PriorMessages is the customer's AI-chatbot (Novera) transcript up to
	// the moment of escalation -- see routingclient.PriorMessage. Optional:
	// omitted or empty just means the engineer's chat starts without that
	// context, same as before this field existed.
	PriorMessages []routingclient.PriorMessage `json:"priorMessages,omitempty"`
}

// HandleEscalate handles POST /internal/chat/escalate. Not browser-facing —
// registered on this backend's main API listener like every other route
// (see cmd/server/main.go), behind the same middleware.Auth JWT check;
// called by customer-portal/backend-v2's own csmchat.Client, authenticating
// with an OAuth2 client-credentials token rather than a user's own session
// token. It is invoked from HandleEscalate before any entity-service case
// exists -- chat-first
// escalation defers real case creation until the assigned engineer
// explicitly converts the chat (see HandleConvertToCase below and
// backend-v2's escalation handler's own doc comment). req.CaseID here is
// chat-routing-service's own case identity for this one live-engineer-chat
// escalation instance -- a fresh UUID minted by backend-v2's HandleEscalate
// for every new escalation, NOT the same as req.ConversationID (the stable
// Novera AI-chat conversation this escalation came from -- see
// router.CaseInfo's own doc comment on why these two are now distinct).
// Also not a real entity-service case ID. This handler's only job is to
// ask the routing service which engineer (if any) should get this case,
// and deliver it accordingly; it does not call entity-service at all.
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
		Subject:        req.Subject,
		CustomerEmail:  req.CustomerEmail,
		CustomerName:   req.CustomerName,
		Message:        req.Message,
		PriorMessages:  req.PriorMessages,
	}

	// Best-effort: LOCAL STAND-IN persistence (see routingService's own doc
	// comment) -- creates the work_item/chat_conversation/first-comment
	// record for this escalation. Must run BEFORE Escalate below: Escalate's
	// own assignment now writes chat_conversation.assignee_id directly (see
	// router.Router.CreateWorkItem's doc comment), so that row must already
	// exist. A failure here is logged, not fatal to this request -- the
	// live routing decision below still must reach the customer regardless
	// of whether this bookkeeping call succeeded (a known limitation of the
	// LOCAL STAND-IN tables, not new: this case just won't count toward the
	// assigned engineer's capacity until the row exists) -- UNLESS the
	// routing service rejected it outright with ErrDuplicateOpenChat, which
	// is not a bookkeeping failure at all: it means this customer already
	// has a live chat in progress for this project, and creating another
	// one would leave them with two concurrent escalations. That case is
	// reported to backend-v2 as a real 409 instead of being swallowed --
	// see this handler's own doc comment on why req.CaseID here is a fresh,
	// caller-minted UUID (never req.ConversationID) that only this call
	// makes durable.
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
			// Not a routing-service outage -- a genuine rejection that
			// must reach the customer as a real failure, not a silent
			// "escalated successfully" (see this handler's own doc comment
			// and router.Router.Escalate's ErrCaseAlreadyEnded). Falling
			// back to broadcast here, like the generic error path below
			// does, would create exactly the zombie-queue-row situation
			// this guard exists to prevent.
			slog.InfoContext(r.Context(), "chat: escalation rejected, case already ended", "caseId", req.CaseID, "conversationId", req.ConversationID)
			writeError(w, http.StatusConflict, "This chat session has already ended. Please start a new conversation to escalate again.")
			return
		}
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
	// (see AddComment below) -- added alongside the LOCAL STAND-IN
	// persistence work, see routingService's own doc comment.
	CustomerEmail string `json:"customerEmail"`
}

// HandleCustomerMessage handles POST /internal/chat/customer-message. Also
// service-to-service only, authenticated the same way as HandleEscalate
// above (see that handler's doc comment). Persists the customer's
// message as a comment via the LOCAL STAND-IN routing-service tables (see
// routingService's own doc comment) — entity-service's real CreateCaseComment
// was never usable here (this internal, service-to-service call from
// backend-v2 has no customer browser session behind it, so there is no
// x-user-id-token to give entity-service, and that write 401s every time)
// — then fans the message out over the shared broadcastHubKey so whichever
// engineer accepted the session sees it live. Deliberately still broadcast
// rather than targeted at the accepting engineer specifically (this handler
// has no record of who that is — see the package doc comment on why there
// is no server-side session table); an accepted scope decision for this
// prototype phase.
//
// AddComment's failure is best-effort EXCEPT for routingclient.
// ErrConversationEnded, which is checked explicitly and short-circuits with
// a 410 instead of falling through to the generic warn-and-continue below.
// That distinction used to not exist at all: this handler always answered
// 201 "relayed" no matter what, so once an engineer clicked "End session"
// the customer could keep "sending" messages that silently went nowhere,
// with no sign anything had ended. See backend-v2's HandleSendMessage and
// customer-portal's sendViaHumanChat for how this 410 propagates from here
// all the way to resetting the customer's own chat UI.
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
		// Best-effort for every other failure: must not block the live
		// relay below. The customer is mid-chat with an engineer right
		// now, and losing this message's durable record is far less
		// costly than silently dropping the message itself (which
		// returning early here used to do, back when this was a blocking
		// entity-service call).
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

// HandleAcceptSession handles POST /chat/sessions/{id}/accept —
// browser-facing, behind the normal Auth middleware. {id} is the case ID.
// First confirms the accept with the routing service (PENDING -> BUSY —
// see internal/routingclient.Client.Accept / chat-routing-service's
// router.Router.Accept) — unlike the rest of this method, that call is
// real and error-surfacing, not best-effort: this is the one moment the
// engineer actually needs to know whether the case is still theirs to
// take, since the alert could have been declined, reassigned, or already
// accepted elsewhere in the meantime. Only once that succeeds does it
// assign the case to the authenticated engineer via the same PATCH
// /cases/{id} + assigneeEmail path csm-portal's case detail page already
// uses (see cases.go's PatchCase / entity's assigneeEmail field) — there is
// no separate "chat session" record; the case IS the session's record.
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
	// Best-effort: entity-service's Postgres-backed cases table has no
	// assignee column today — UpdateCase unconditionally rejects
	// assigneeEmail with 400, regardless of the case's actual data source
	// (see case_service.go's UpdateCase). Every case created via the chat-
	// escalation path is a Postgres case, so this call is expected to fail
	// there. This is no longer the only record of who accepted, though:
	// Router.Accept (just above) already durably set chat_conversation.
	// assignee_id in the LOCAL STAND-IN tables in the same transaction as
	// the PENDING -> BUSY flip. This PatchCase attempt is kept anyway, on
	// the chance entity-service ever adds a real assignee column — a
	// failure here must not block accepting either way.
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

// HandleEngineerMessage handles POST /chat/sessions/{id}/messages —
// browser-facing, behind Auth. {id} is the case ID. Persists the engineer's
// reply via the LOCAL STAND-IN routing-service tables (see routingService's
// own doc comment), then relays it to the customer's already-open WebSocket
// via backend-v2's internal push (see the package doc comment on why this
// direction uses a push instead of SSE).
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

	// Best-effort, matching HandleCustomerMessage's own philosophy for the
	// other direction of this same transcript (see routingService's doc
	// comment) -- an engineer's message must still reach the customer over
	// notifyOrigin below even if this LOCAL STAND-IN persistence call
	// fails. Previously this was a blocking entity.CreateCaseComment call;
	// moved here so both directions of the conversation land in the same
	// place (the stand-in comment table) instead of being split across two
	// storage systems -- see the project's chat-persistence-mapping-plan.md.
	//
	// ErrConversationEnded is the one exception, exactly as in
	// HandleCustomerMessage -- normally the engineer's own UI removes an
	// ended session's tab before another message can be sent, but a
	// request already in flight when "End session" is clicked shouldn't
	// silently report success either.
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
	h.notifyOrigin(r.Context(), h.sourceForCase(r.Context(), caseID), chatEvent{
		Type:           "engineer_disconnected",
		CaseID:         caseID,
		ConversationID: req.ConversationID,
		EngineerEmail:  user.Email,
		Timestamp:      now,
	})

	// Best-effort: release this engineer's routing-service capacity for
	// this specific case (they may still hold other concurrent chats, see
	// the 2026-09-10 concurrent-chat-capacity change) and, if that
	// immediately drained the waiting queue, deliver the next case to them
	// the same way a fresh escalation would arrive. A failure here is
	// logged, not surfaced to the caller — the session has already ended
	// successfully from the engineer's point of view, matching this
	// handler's existing best-effort treatment of notifyOrigin above.
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
	// AVAILABLE, BUSY, and OFFLINE are all requestable directly now (see
	// the 2026-09-10 concurrent-chat-capacity change): chat_status is a
	// plain manual toggle independent of case load, and BUSY is a real
	// do-not-disturb an engineer can set without dropping any case they
	// already hold (see router.Router.SetPresence's doc comment). There is
	// still no PENDING here -- that's a per-case fact, never a top-level
	// status an engineer requests (see routingclient.CaseStatus.Pending).
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

	// Going AVAILABLE can drain more than one queued case at once now (see
	// router.Router.SetPresence's queue-drain, capped at this engineer's own
	// max_concurrent_chats) -- unlike Completed/Decline/a timeout, which
	// each free at most one slot, so deliver every one of them.
	for _, c := range result.AssignedCases {
		h.publishToEngineer(user.UserID, assignedCaseEvent(c))
	}

	writeJSONValue(w, http.StatusOK, result)
}

// HandleGetPresence handles GET /engineers/me/status — browser-facing,
// behind Auth. Lets the status dropdown initialize correctly on load/reload
// instead of assuming a default itself — the routing service's own default
// for an engineer it has never seen (OFFLINE — see router.Router.
// GetPresence) is exposed here rather than hardcoded a second time in the
// browser. Also passes through every case the engineer currently holds
// (pending or accepted alike, see routingclient.PresenceDetail.Cases), so
// the browser can rehydrate lost pending alerts or active sessions' local
// widget state after losing it (a refresh, a closed tab) instead of the
// engineer being stuck with nothing in the UI to act on.
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

// minMaxConcurrentChats/maxMaxConcurrentChats mirror chat-routing-service's
// own cs_engineer_status.max_concurrent_chats CHECK constraint (see that
// service's migrations/000018_concurrent_chat_capacity.up.sql, lowered
// from 1-20 to 1-10 by 000020_lower_max_concurrent_chats.up.sql -- Sajith
// confirmed 10 as the intended maximum) -- checked here too so an
// out-of-range value gets a clean 400 from this browser-facing endpoint
// instead of a raw upstream error surfacing as a 502.
const (
	minMaxConcurrentChats = 1
	maxMaxConcurrentChats = 10
)

// HandleSetMaxConcurrentChats handles PATCH /engineers/me/capacity —
// browser-facing, behind Auth. Lets an engineer set their own configurable
// concurrent-chat capacity (see internal/routingclient.Client.
// SetMaxConcurrentChats and chat-routing-service's router.Router.
// SetMaxConcurrentChats), replacing the manual pgAdmin `UPDATE
// cs_engineer_status` this previously required (see the project's
// db-schema-review-2026-09-07-outcomes.md). Deliberately does not disturb
// any case the engineer already holds -- lowering the limit below their
// current active count just stops new work from routing to them until
// they fall back under it, it never drops an in-progress chat.
//
// Raising the limit, however, can immediately open up spare capacity: if a
// customer was left waiting in the queue because this engineer was at
// capacity, they must not go on sitting there once the engineer makes room
// for them just by raising their own limit. router.Router.
// SetMaxConcurrentChats now drains the queue into that new capacity the
// same way going AVAILABLE does, so any case it hands back here is
// delivered to this engineer exactly like HandleSetPresence already does
// for its own queue-drain.
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

	// Raising the limit can immediately drain more than one queued case
	// into this engineer's newly-opened capacity (see router.Router.
	// SetMaxConcurrentChats's queue-drain) -- same delivery
	// HandleSetPresence already does for its own queue-drain.
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
// POST /internal/chat/create-case — exactly the fields routingclient.
// CaseInfo already carries, since GetCaseInfo below is where they come
// from; kept as its own type (rather than encoding routingclient.CaseInfo
// directly) so this outbound wire shape can diverge from that SDK type if
// either one changes independently later.
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
// the chat's stored details, creates the case, then ends the chat session
// -- failing the whole request if any step fails, since a partially
// converted chat (case created but not recorded here, or vice versa) is
// worse than just failing the click.
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
		// The case above already exists even though this failed -- surface
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
