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

// Package handler — this file implements the customer-facing side of the
// live-engineer-chat escalation feature (see csm-portal/backend's own
// internal/handler/chat.go for the engineer-facing side and the shared
// design notes).
//
// Chat-first escalation: HandleEscalate no longer creates a real
// entity-service case. It only resolves and validates that this project
// COULD have one created for it later (see below), then hands the chat off
// using req.ConversationID as its own identity — see that method's own doc
// comment and the project's chat-first-escalation-plan.md. A real case is
// created later, exactly once, only if and when the assigned engineer
// explicitly converts the chat — see HandleCreateCase below, this file's
// other exported method.
//
// Case creation (whenever it does happen) lives HERE, not in csm-portal/
// backend, even though csm-portal owns everything else about a case:
// entity-service requires a real deploymentId and deployedProductId to
// create a case (NOT NULL, FK-enforced — see entity-service's cases
// migration), and only this backend has any basis for resolving those from
// a project ID alone (a customer in the Novera chat has picked neither).
// Both HandleEscalate (as a fail-fast check) and HandleCreateCase (for
// real) make the same best-effort choice — the project's first deployment
// (preferring one whose type is primary_production) and that deployment's
// first deployed product — failing with a clear, actionable message if a
// project has neither, rather than guessing something invalid. There is
// deliberately no UI form for any of this: escalating from underneath an AI
// chat reply is meant to be one click, and a project with zero deployments
// is treated as an edge case to surface honestly (the customer can still
// use the existing "Create Case" flow, which does prompt for these), not
// one to silently paper over.
package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/middleware"
)

// escalationEntityClient is the subset of the entity client this feature
// needs. entityClient (cmd/server/main.go) already implements this.
//
// Deliberately no SearchComments here (unlike ai_chat.go's own entity
// client interface, which still uses it for the AI-chat transcript itself):
// HandleEscalate's priorMessages now comes directly from the browser (see
// escalateRequestBody.PriorMessages below), not from an entity-service
// fetch -- this deployment's DATA_SOURCE=postgres entity-service has no
// conversation/comment persistence of its own to fetch from, so a
// SearchComments call here would either fail or (on a ServiceNow-backed
// deployment) risk double-counting the same history the frontend already
// sent. See this file's package doc comment and the project's
// chat-persistence-mapping-plan.md / chat-first-escalation-plan.md.
type escalationEntityClient interface {
	GetProject(ctx context.Context, id string) (entity.ProjectDetailsView, error)
	SearchDeployments(ctx context.Context, req entity.SearchDeploymentsRequest) (entity.SearchDeploymentsResponse, error)
	SearchDeployedProducts(ctx context.Context, req entity.SearchDeployedProductsRequest) (entity.SearchDeployedProductsResponse, error)
	CreateCase(ctx context.Context, req entity.CreateCaseRequest) (entity.CreateCaseResponse, error)
}

// csmChatPusher abstracts internal/csmchat.Client so tests can fake the
// csm-portal/backend calls.
type csmChatPusher interface {
	Escalate(ctx context.Context, payload []byte) error
	SendCustomerMessage(ctx context.Context, payload []byte) error
}

// chatEventPushTarget is the subset of WebSocketHandler this feature's
// internal receiver (HandleChatEvents) needs — just enough to relay an
// event into an already-open browser connection. Kept as its own small
// interface (rather than depending on *WebSocketHandler directly) so this
// file and websocket.go stay independently testable.
type chatEventPushTarget interface {
	PushEvent(conversationID string, evt wsEvent) bool
}

// ChatEscalationHandler implements the customer-facing live-engineer-chat
// endpoints.
type ChatEscalationHandler struct {
	entity escalationEntityClient
	csm    csmChatPusher
}

// NewChatEscalationHandler creates a ChatEscalationHandler.
func NewChatEscalationHandler(entityClient escalationEntityClient, csm csmChatPusher) *ChatEscalationHandler {
	return &ChatEscalationHandler{entity: entityClient, csm: csm}
}

// escalationDefaultSeverity/IssueType are the values used for every case
// this handler creates. A live-engineer request has no severity/issue-type
// picker of its own (it is one click under an AI reply, not a form) — these
// match entity-service's own enums (see the package doc comment) and are
// deliberately mid-range defaults; the engineer who picks it up can correct
// them from the normal case detail page like any other case field.
const (
	escalationDefaultSeverity  = "medium"
	escalationDefaultIssueType = "question"
	escalationDefaultSubject   = "Live engineer requested via Novera chat"
	escalationDefaultMessage   = "Customer requested a live engineer."
)

// escalationSearchLimit bounds the deployment/deployed-product searches
// below — this handler only ever looks at the first result, but the search
// endpoints require a pagination limit.
const escalationSearchLimit = 25

// chatNotifyTimeout bounds the best-effort push to csm-portal/backend.
const chatNotifyTimeout = 5 * time.Second

// escalatePriorMessageRole is who sent one escalatePriorMessage -- the
// customer or the Novera AI assistant. Mirrors chat-routing-service's
// router.PriorMessageRole / csm-portal/backend's routingclient.
// PriorMessageRole exactly (same two values, same wire tag), since this
// value is forwarded through both of those services unchanged before
// finally being persisted as chat_routing.comment.created_by.
type escalatePriorMessageRole string

const (
	escalatePriorMessageRoleCustomer  escalatePriorMessageRole = "customer"
	escalatePriorMessageRoleAssistant escalatePriorMessageRole = "assistant"
)

// escalatePriorMessage is one message from the customer's AI-chatbot
// (Novera) conversation, sent by the browser (NoveraChatPage's own
// messages[] state -- see that page's handleEscalateToEngineer) and
// forwarded verbatim in the escalation push to csm-portal/backend so the
// assigned engineer's chat opens with the same context the customer already
// gave the AI. Field names mirror chat-routing-service's router.
// PriorMessage / csm-portal/backend's routingclient.PriorMessage.
type escalatePriorMessage struct {
	Role      escalatePriorMessageRole `json:"role"`
	Content   string                   `json:"content"`
	CreatedAt string                   `json:"createdAt,omitempty"`
}

// escalatePushPayload is the JSON body sent to csm-portal/backend's
// POST /internal/chat/escalate. Replaces the old inline map[string]string
// now that PriorMessages needs a nested array rather than a flat string
// value.
type escalatePushPayload struct {
	CaseID         string                 `json:"caseId"`
	ConversationID string                 `json:"conversationId"`
	ProjectID      string                 `json:"projectId"`
	Subject        string                 `json:"subject"`
	CustomerEmail  string                 `json:"customerEmail"`
	CustomerName   string                 `json:"customerName"`
	Message        string                 `json:"message"`
	PriorMessages  []escalatePriorMessage `json:"priorMessages,omitempty"`
}

// escalateRequestBody is the body the customer's browser sends.
type escalateRequestBody struct {
	ConversationID string `json:"conversationId"`
	// Message is the customer's own opening text, shown to the engineer as
	// context and stored as the case description. Optional — falls back to
	// escalationDefaultMessage.
	Message string `json:"message"`
	// CustomerName is a display label only, used solely in the engineer's
	// alert UI — never for authorization or attribution in a durable
	// record (the case's own createdBy comes from entity-service's auth
	// context, exactly like every other case this backend creates).
	CustomerName string `json:"customerName,omitempty"`
	// PriorMessages is the customer's AI-chatbot (Novera) conversation
	// history already visible in the browser at the moment "Chat with an
	// Engineer" was clicked -- see escalatePriorMessage. Sent directly by
	// NoveraChatPage's own messages[] state, not fetched from
	// entity-service (see escalationEntityClient's own doc comment).
	// Optional -- omitted or empty just means the engineer's chat starts
	// without that context, same as before this field existed.
	PriorMessages []escalatePriorMessage `json:"priorMessages,omitempty"`
}

// pickDeployment prefers a primary_production deployment when one exists,
// otherwise returns the first result. entity-service's deployment search
// has no "default" concept (see the package doc comment), so this is a
// heuristic, not a guarantee of correctness — it is why this whole flow is
// documented as best-effort.
func pickDeployment(deployments []entity.DeploymentView) entity.DeploymentView {
	for _, d := range deployments {
		if d.Type == "primary_production" {
			return d
		}
	}
	return deployments[0]
}

// HandleEscalate handles POST /projects/{id}/support/chat/escalate.
// Resolves a deployment/deployed-product for the project as a fail-fast
// check, then best-effort notifies csm-portal/backend so a connected
// engineer sees the alert. No entity-service case is created here -- one
// is only created later, if the engineer converts the chat (HandleCreateCase).
func (h *ChatEscalationHandler) HandleEscalate(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" || !uuidRe.MatchString(projectID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req escalateRequestBody
	if err := json.Unmarshal(body, &req); err != nil || req.ConversationID == "" || !uuidRe.MatchString(req.ConversationID) {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	message := req.Message
	if message == "" {
		message = escalationDefaultMessage
	}

	deployments, err := h.entity.SearchDeployments(r.Context(), entity.SearchDeploymentsRequest{
		Pagination: entity.Pagination{Limit: escalationSearchLimit},
		ProjectIDs: []string{projectID},
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "entity SearchDeployments failed during chat escalation", "userID", user.UserID, "projectID", projectID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to escalate to a live engineer.")
		return
	}
	if len(deployments.Deployments) == 0 {
		writeError(w, http.StatusConflict, "This project has no deployment on file, so a case can't be created automatically. Please use \"Create Case\" instead.")
		return
	}
	deployment := pickDeployment(deployments.Deployments)

	deployedProducts, err := h.entity.SearchDeployedProducts(r.Context(), entity.SearchDeployedProductsRequest{
		Pagination:    entity.Pagination{Limit: escalationSearchLimit},
		DeploymentIDs: []string{deployment.ID},
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "entity SearchDeployedProducts failed during chat escalation", "userID", user.UserID, "projectID", projectID, "deploymentID", deployment.ID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to escalate to a live engineer.")
		return
	}
	if len(deployedProducts.DeployedProducts) == 0 {
		writeError(w, http.StatusConflict, "This project's deployment has no product on file, so a case can't be created automatically. Please use \"Create Case\" instead.")
		return
	}
	// This is a fail-fast UX guard only -- no case is created from it. The
	// resolution runs again for real at conversion time (HandleCreateCase
	// below), since a deployment cached from here could go stale by then.

	// req.ConversationID becomes chat-routing-service's own case_id via
	// this push -- no entity-service case exists yet.
	customerName := req.CustomerName
	if customerName == "" {
		customerName = user.Email
	}

	// Prior messages come straight from the browser now, not from an
	// entity-service fetch (see escalateRequestBody.PriorMessages's own doc
	// comment and escalationEntityClient's) -- NoveraChatPage already sends
	// its own visible transcript, in chronological order, as of the moment
	// "Chat with an Engineer" was clicked. Only light validation here: an
	// unrecognized role collapses to "customer" rather than rejecting the
	// whole escalation over one bad value, and a message with no content is
	// dropped.
	priorMessages := make([]escalatePriorMessage, 0, len(req.PriorMessages))
	for _, m := range req.PriorMessages {
		if m.Content == "" {
			continue
		}
		role := m.Role
		if role != escalatePriorMessageRoleAssistant {
			role = escalatePriorMessageRoleCustomer
		}
		priorMessages = append(priorMessages, escalatePriorMessage{
			Role:      role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt,
		})
	}

	pushPayload, err := json.Marshal(escalatePushPayload{
		CaseID:         req.ConversationID,
		ConversationID: req.ConversationID,
		ProjectID:      projectID,
		Subject:        escalationDefaultSubject,
		CustomerEmail:  user.Email,
		CustomerName:   customerName,
		Message:        message,
		PriorMessages:  priorMessages,
	})
	if err == nil {
		pushCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), chatNotifyTimeout)
		if pushErr := h.csm.Escalate(pushCtx, pushPayload); pushErr != nil {
			// Best-effort: a dropped push means this escalation has no
			// record anywhere until the customer retries, but failing the
			// request here would bypass csm-portal/backend's own broadcast
			// fallback for an unreachable chat-routing-service.
			slog.ErrorContext(r.Context(), "csm-portal escalate push failed", "userID", user.UserID, "conversationID", req.ConversationID, "err", pushErr)
		}
		cancel()
	} else {
		slog.ErrorContext(r.Context(), "failed to encode csm-portal escalate push payload", "userID", user.UserID, "conversationID", req.ConversationID, "err", err)
	}

	writeJSONValue(w, http.StatusCreated, map[string]string{
		"caseId":  req.ConversationID,
		"message": "Escalated to available engineers.",
	})
}

// createCaseRequestBody is the body csm-portal/backend sends to
// POST /internal/chat/create-case: the chat's original escalation details,
// so nothing here is resent by an engineer's browser or invented fresh.
type createCaseRequestBody struct {
	// CaseID is chat-routing-service's own case identity for this chat
	// (equal to ConversationID), included only for logging on this end.
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId"`
	Subject        string `json:"subject"`
	CustomerEmail  string `json:"customerEmail"`
	CustomerName   string `json:"customerName"`
	Message        string `json:"message"`
}

// createCaseResponseBody is this handler's response shape.
type createCaseResponseBody struct {
	EntityCaseID string `json:"entityCaseId"`
}

// HandleCreateCase handles POST /internal/chat/create-case: creates a real
// entity-service case for a chat, called only when the assigned engineer
// explicitly converts it. Internal and synchronous (unlike
// ChatEventsHandler's fire-and-forget events) since the caller needs a
// real case ID or error back. Reuses HandleEscalate's deployment/
// deployed-product resolution, run for real this time against req.ProjectID.
func (h *ChatEscalationHandler) HandleCreateCase(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req createCaseRequestBody
	if err := json.Unmarshal(body, &req); err != nil || req.ProjectID == "" ||
		req.ConversationID == "" || !uuidRe.MatchString(req.ConversationID) {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	// This route has no browser session of its own, so it reads the
	// engineer's x-user-id-token (forwarded by csm-portal/backend) off the
	// request header instead; the resulting case is attributed to the
	// converting engineer, not the original customer.
	ctx := r.Context()
	if token := r.Header.Get("x-user-id-token"); token != "" {
		ctx = entity.WithUserIDToken(ctx, token)
	}

	subject := req.Subject
	if subject == "" {
		subject = escalationDefaultSubject
	}
	message := req.Message
	if message == "" {
		message = escalationDefaultMessage
	}

	deployments, err := h.entity.SearchDeployments(ctx, entity.SearchDeploymentsRequest{
		Pagination: entity.Pagination{Limit: escalationSearchLimit},
		ProjectIDs: []string{req.ProjectID},
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "entity SearchDeployments failed converting chat to case", "caseID", req.CaseID, "projectID", req.ProjectID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to create a case for this chat.")
		return
	}
	if len(deployments.Deployments) == 0 {
		writeError(w, http.StatusConflict, "This project has no deployment on file, so a case can't be created automatically.")
		return
	}
	deployment := pickDeployment(deployments.Deployments)

	deployedProducts, err := h.entity.SearchDeployedProducts(ctx, entity.SearchDeployedProductsRequest{
		Pagination:    entity.Pagination{Limit: escalationSearchLimit},
		DeploymentIDs: []string{deployment.ID},
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "entity SearchDeployedProducts failed converting chat to case", "caseID", req.CaseID, "projectID", req.ProjectID, "deploymentID", deployment.ID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to create a case for this chat.")
		return
	}
	if len(deployedProducts.DeployedProducts) == 0 {
		writeError(w, http.StatusConflict, "This project's deployment has no product on file, so a case can't be created automatically.")
		return
	}
	deployedProduct := deployedProducts.DeployedProducts[0]

	created, err := h.entity.CreateCase(ctx, entity.CreateCaseRequest{
		Type:              "case",
		ProjectID:         req.ProjectID,
		DeploymentID:      deployment.ID,
		DeployedProductID: deployedProduct.ID,
		Subject:           subject,
		Description:       message,
		Severity:          escalationDefaultSeverity,
		IssueType:         escalationDefaultIssueType,
		ConversationID:    req.ConversationID,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "entity CreateCase failed converting chat to case", "caseID", req.CaseID, "projectID", req.ProjectID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to create a case for this chat.")
		return
	}

	writeJSONValue(w, http.StatusCreated, createCaseResponseBody{EntityCaseID: created.Case.ID})
}

// sendMessageRequestBody is the body the customer's browser sends once a
// human has accepted the session.
type sendMessageRequestBody struct {
	CaseID  string `json:"caseId"`
	Message string `json:"message"`
}

// HandleSendMessage handles
// POST /projects/{id}/support/chat/{conversationId}/message. Unlike the AI
// chat path (WebSocketHandler.handleMessage), a human-attended message is
// NOT persisted here — csm-portal/backend persists it instead, via the
// LOCAL STAND-IN routing-service tables (see that backend's
// HandleCustomerMessage and the project's chat-persistence-mapping-plan.md),
// since the case is still the operative record for routing purposes once a
// human has taken over, even though message storage itself has moved off
// entity-service's case_comments. This handler is a thin, purely relaying
// call.
func (h *ChatEscalationHandler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	projectID := r.PathValue("id")
	conversationID := r.PathValue("conversationId")
	if projectID == "" || !uuidRe.MatchString(projectID) || conversationID == "" || !uuidRe.MatchString(conversationID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req sendMessageRequestBody
	if err := json.Unmarshal(body, &req); err != nil || req.CaseID == "" || !uuidRe.MatchString(req.CaseID) || req.Message == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	payload, err := json.Marshal(map[string]string{
		"caseId":         req.CaseID,
		"conversationId": conversationID,
		"message":        req.Message,
		// customerEmail attributes this message's persisted record to the
		// actual sender -- see csm-portal/backend's HandleCustomerMessage,
		// which uses it as internal/router.AddComment's authorEmail (a
		// local stand-in for entity-service's eventual generic comment
		// table; see the project's chat-persistence-mapping-plan.md).
		"customerEmail": user.Email,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}

	if err := h.csm.SendCustomerMessage(r.Context(), payload); err != nil {
		slog.ErrorContext(r.Context(), "csm-portal customer-message push failed", "userID", user.UserID, "caseID", req.CaseID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to send message to the engineer.")
		return
	}

	writeJSONValue(w, http.StatusCreated, map[string]string{"message": "sent"})
}

// chatEventPushBody is the body csm-portal/backend sends to
// POST /internal/chat-events.
type chatEventPushBody struct {
	Type           string `json:"type"`
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	EngineerEmail  string `json:"engineerEmail"`
	Message        string `json:"message"`
	// EntityCaseID is set only on a "converted_to_case" event (see
	// csm-portal/backend's HandleConvertToCase) -- the real entity-service
	// case ID the customer's browser should point to now that this chat
	// has ended. Must stay in sync with that handler's chatEvent.EntityCaseID
	// field name/tag; dropping this silently discards the field on
	// unmarshal instead of erroring, which is what happened before this
	// field existed here.
	EntityCaseID string `json:"entityCaseId,omitempty"`
	Timestamp    string `json:"timestamp"`
}

// ChatEventsHandler receives the internal push from csm-portal/backend and
// relays it into the matching open customer WebSocket connection, if any.
type ChatEventsHandler struct {
	ws chatEventPushTarget
}

// NewChatEventsHandler creates a ChatEventsHandler.
func NewChatEventsHandler(ws chatEventPushTarget) *ChatEventsHandler {
	return &ChatEventsHandler{ws: ws}
}

// Handle implements POST /internal/chat-events. Registered on the same
// listener as GET /ws (see cmd/server/main.go) purely because that is where
// this backend's internal/service-facing surface already lives -- unlike
// GET /ws, this route DOES go through the normal authMiddleware
// (middleware.Auth/AuthWithValidator), since csm-portal/backend is a
// regular HTTP client that can set any header it likes and authenticates
// with an OAuth2 client-credentials token carried as x-jwt-assertion, not a
// browser unable to set one on a WebSocket handshake.
//
// A conversationId with no currently-open connection is not an error — the
// customer may simply have the tab closed — so this always responds 202,
// never propagating "no connection" as a client-visible failure.
func (h *ChatEventsHandler) Handle(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var evt chatEventPushBody
	if err := json.Unmarshal(body, &evt); err != nil || evt.ConversationID == "" || evt.Type == "" {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	delivered := h.ws.PushEvent(evt.ConversationID, wsEvent{
		Type:           evt.Type,
		Message:        evt.Message,
		ConversationID: evt.ConversationID,
		EngineerEmail:  evt.EngineerEmail,
		EntityCaseID:   evt.EntityCaseID,
		TS:             evt.Timestamp,
	})
	if !delivered {
		slog.InfoContext(r.Context(), "chat event had no open connection to deliver to", "type", evt.Type, "conversationId", evt.ConversationID)
	}

	w.WriteHeader(http.StatusAccepted)
}
