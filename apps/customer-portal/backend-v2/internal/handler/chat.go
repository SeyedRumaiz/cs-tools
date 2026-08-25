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
// Case creation lives HERE, not in csm-portal/backend, even though csm-
// portal owns everything else about a case: entity-service requires a real
// deploymentId and deployedProductId to create a case (NOT NULL, FK-
// enforced — see entity-service's cases migration), and only this backend
// has any basis for resolving those from a project ID alone (a customer in
// the Novera chat has picked neither). This handler makes a best-effort
// choice — the project's first deployment (preferring one whose type is
// primary_production) and that deployment's first deployed product — and
// fails the escalation with a clear, actionable message if a project has
// neither, rather than guessing something invalid. There is deliberately no
// UI form here: escalating from underneath an AI chat reply is meant to be
// one click, and a project with zero deployments is treated as an edge case
// to surface honestly (the customer can still use the existing "Create
// Case" flow, which does prompt for these), not one to silently paper over.
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
// Resolves a deployment/deployed-product for the project (see the package
// doc comment), creates the case directly against entity-service, then
// best-effort notifies csm-portal/backend so a connected engineer sees the
// alert. The case creation itself is NOT best-effort: if it fails, the
// whole request fails, since there is nothing meaningful to notify anyone
// about without a case.
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
	deployedProduct := deployedProducts.DeployedProducts[0]

	created, err := h.entity.CreateCase(r.Context(), entity.CreateCaseRequest{
		Type:              "case",
		ProjectID:         projectID,
		DeploymentID:      deployment.ID,
		DeployedProductID: deployedProduct.ID,
		Subject:           escalationDefaultSubject,
		Description:       message,
		Severity:          escalationDefaultSeverity,
		IssueType:         escalationDefaultIssueType,
		ConversationID:    req.ConversationID,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "entity CreateCase failed during chat escalation", "userID", user.UserID, "projectID", projectID, "err", summarizeErr(err))
		mapUpstreamError(w, err, "Failed to escalate to a live engineer.")
		return
	}

	customerName := req.CustomerName
	if customerName == "" {
		customerName = user.Email
	}
	pushPayload, err := json.Marshal(map[string]string{
		"caseId":         created.Case.ID,
		"conversationId": req.ConversationID,
		"projectId":      projectID,
		"subject":        escalationDefaultSubject,
		"customerEmail":  user.Email,
		"customerName":   customerName,
		"message":        message,
	})
	if err == nil {
		pushCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), chatNotifyTimeout)
		if pushErr := h.csm.Escalate(pushCtx, pushPayload); pushErr != nil {
			// Best-effort: the case already exists and is visible in the
			// normal case list/queue either way — a failed live alert just
			// means no engineer gets pinged in real time for this one
			// escalation.
			slog.ErrorContext(r.Context(), "csm-portal escalate push failed", "userID", user.UserID, "caseID", created.Case.ID, "err", pushErr)
		}
		cancel()
	} else {
		slog.ErrorContext(r.Context(), "failed to encode csm-portal escalate push payload", "userID", user.UserID, "caseID", created.Case.ID, "err", err)
	}

	writeJSONValue(w, http.StatusCreated, map[string]string{
		"caseId":  created.Case.ID,
		"message": "Escalated to available engineers.",
	})
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
// NOT persisted here as a conversation comment — csm-portal/backend
// persists it as a case comment instead (see that backend's
// HandleCustomerMessage), since the case is the operative record once a
// human has taken over. This handler is a thin, purely relaying call.
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
	Timestamp      string `json:"timestamp"`
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
// listener as GET /ws (see cmd/server/main.go) — like that route, it cannot
// go through the normal Auth middleware, since csm-portal/backend has no
// customer x-jwt-assertion to present; it is instead gated by
// middleware.InternalToken at the route-registration layer.
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
		TS:             evt.Timestamp,
	})
	if !delivered {
		slog.InfoContext(r.Context(), "chat event had no open connection to deliver to", "type", evt.Type, "conversationId", evt.ConversationID)
	}

	w.WriteHeader(http.StatusAccepted)
}
