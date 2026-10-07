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

// Package handler adapts chat-routing-service's HTTP API onto router.Router.
// Authentication (internal/middleware.InternalTokenHeader) is enforced by
// the middleware chain in main.go, not by this package. A handler maps a
// router.Router sentinel error to a specific HTTP status; any other error
// is reported as 502 (see writeStorageError).
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/router"
)

const maxBodyBytes = 64 << 10 // 64 KiB

// RoutingHandler adapts HTTP requests to router.Router calls.
type RoutingHandler struct {
	router              *router.Router
	pendingTimeout      time.Duration // Threshold for assignments waiting for acceptance
	queueAbandonTimeout time.Duration // Threshold used to clean up abandoned queued chats
	staleSessionTimeout time.Duration // Threshold used to clean up stale accepted sessions
}

// NewRoutingHandler constructs a RoutingHandler bound to r. pendingTimeout,
// queueAbandonTimeout, and staleSessionTimeout are SweepTimeouts' three
// thresholds — see router.Router.SweepExpiredPending, SweepAbandonedQueue,
// and SweepStaleAcceptedSessions respectively.
func NewRoutingHandler(r *router.Router, pendingTimeout, queueAbandonTimeout, staleSessionTimeout time.Duration) *RoutingHandler {
	return &RoutingHandler{
		router:              r,
		pendingTimeout:      pendingTimeout,
		queueAbandonTimeout: queueAbandonTimeout,
		staleSessionTimeout: staleSessionTimeout,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// writeStorageError logs the underlying Postgres/pool error (never sent to
// the caller) and responds 502 — the routing service's own state store is
// unavailable.
func writeStorageError(w http.ResponseWriter, route string, err error) {
	slog.Error("routing store error", "route", route, "err", err)
	writeError(w, http.StatusBadGateway, "Routing state store is unavailable.")
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload.")
		return false
	}
	return true
}

// isValidStatus reports whether s is a chat_status an engineer can
// directly request.
func isValidStatus(s string) bool {
	switch router.Status(s) {
	case router.StatusAvailable, router.StatusBusy, router.StatusOffline:
		return true
	default:
		return false
	}
}

// caseInfoRequest is the body shape shared by POST /route/escalate and
// POST /route/workitem - one field per router.CaseInfo field.
type caseInfoRequest struct {
	CaseID         string                `json:"caseId"`
	ConversationID string                `json:"conversationId"`
	ProjectID      string                `json:"projectId"`
	Source         string                `json:"source,omitempty"`
	Channel        string                `json:"channel,omitempty"`
	TenantSlug     string                `json:"tenantSlug,omitempty"`
	Subject        string                `json:"subject"`
	CustomerEmail  string                `json:"customerEmail"`
	CustomerName   string                `json:"customerName"`
	Message        string                `json:"message"`
	PriorMessages  []router.PriorMessage `json:"priorMessages,omitempty"`
}

func (req caseInfoRequest) toCaseInfo() router.CaseInfo {
	return router.CaseInfo{
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
}

// Escalate handles POST /route/escalate.
func (h *RoutingHandler) Escalate(w http.ResponseWriter, r *http.Request) {
	var req caseInfoRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.CaseID == "" || req.ConversationID == "" {
		writeError(w, http.StatusBadRequest, "caseId and conversationId are required.")
		return
	}

	result, err := h.router.Escalate(r.Context(), req.toCaseInfo())
	if err != nil {
		switch {
		case errors.Is(err, router.ErrConversationNotFound):
			writeError(w, http.StatusBadRequest, "No chat_conversation row exists for this case -- create the work item (POST /route/workitem) before escalating.")
		case errors.Is(err, router.ErrCaseAlreadyEnded):
			writeError(w, http.StatusConflict, "This chat session has already ended and cannot be escalated again.")
		default:
			writeStorageError(w, "escalate", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// presenceRequest is the body for POST /route/presence.
type presenceRequest struct {
	UserID string `json:"userId"`
	Status string `json:"status"`
}

// SetPresence handles POST /route/presence, applying an engineer's manual
// chat_status change. Requesting AVAILABLE may claim waiting cases into
// newly-open capacity — see PresenceResult.AssignedCases.
func (h *RoutingHandler) SetPresence(w http.ResponseWriter, r *http.Request) {
	var req presenceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.UserID == "" || !isValidStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "userId and a valid status (AVAILABLE|BUSY|OFFLINE) are required.")
		return
	}

	result, err := h.router.SetPresence(r.Context(), req.UserID, router.Status(req.Status))
	if err != nil {
		writeStorageError(w, "presence", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// completedRequest is the body for POST /route/completed. CaseID
// identifies which of the engineer's (possibly several concurrent) cases
// just ended.
type completedRequest struct {
	UserID string `json:"userId"`
	CaseID string `json:"caseId"`
}

// Completed handles POST /route/completed. Ended is false, not an error,
// if caseId isn't currently an open conversation assigned to userId.
func (h *RoutingHandler) Completed(w http.ResponseWriter, r *http.Request) {
	var req completedRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.UserID == "" || req.CaseID == "" {
		writeError(w, http.StatusBadRequest, "userId and caseId are required.")
		return
	}

	result, err := h.router.Completed(r.Context(), req.UserID, req.CaseID)
	if err != nil {
		writeStorageError(w, "completed", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// declineRequest is the body for POST /route/decline.
type declineRequest struct {
	UserID string `json:"userId"`
	CaseID string `json:"caseId"`
}

// Decline handles POST /route/decline: an engineer dismissing caseId
// before accepting it. See router.Router.Decline for the reassignment
// rules.
func (h *RoutingHandler) Decline(w http.ResponseWriter, r *http.Request) {
	var req declineRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.UserID == "" || req.CaseID == "" {
		writeError(w, http.StatusBadRequest, "userId and caseId are required.")
		return
	}

	result, err := h.router.Decline(r.Context(), req.UserID, req.CaseID)
	if err != nil {
		writeStorageError(w, "decline", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// acceptRequest is the body for POST /route/accept.
type acceptRequest struct {
	UserID string `json:"userId"`
	CaseID string `json:"caseId"`
}

// Accept handles POST /route/accept — confirms userId is accepting caseId
// (OPEN -> ACTIVE for that one conversation).
func (h *RoutingHandler) Accept(w http.ResponseWriter, r *http.Request) {
	var req acceptRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.UserID == "" || req.CaseID == "" {
		writeError(w, http.StatusBadRequest, "userId and caseId are required.")
		return
	}

	result, err := h.router.Accept(r.Context(), req.UserID, req.CaseID)
	if err != nil {
		writeStorageError(w, "accept", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type presenceResponse struct {
	ChatStatus            router.Status       `json:"chatStatus"`
	ActiveChats           int                 `json:"activeChats"`
	MaxConcurrentChats    int                 `json:"maxConcurrentChats"`
	AtCapacity            bool                `json:"atCapacity"`
	Cases                 []router.CaseStatus `json:"cases,omitempty"`
	PendingTimeoutSeconds int                 `json:"pendingTimeoutSeconds"`
}

// GetPresence handles GET /route/presence/{userId}.
func (h *RoutingHandler) GetPresence(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId is required.")
		return
	}
	detail, err := h.router.GetPresence(r.Context(), userID)
	if err != nil {
		writeStorageError(w, "presence:get", err)
		return
	}
	writeJSON(w, http.StatusOK, presenceResponse{
		ChatStatus: detail.ChatStatus, ActiveChats: detail.ActiveChats,
		MaxConcurrentChats: detail.MaxConcurrentChats, AtCapacity: detail.AtCapacity,
		Cases: detail.Cases, PendingTimeoutSeconds: int(h.pendingTimeout.Seconds()),
	})
}

// CreateWorkItem handles POST /route/workitem
func (h *RoutingHandler) CreateWorkItem(w http.ResponseWriter, r *http.Request) {
	var req caseInfoRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.CaseID == "" || req.CustomerEmail == "" || req.Subject == "" {
		writeError(w, http.StatusBadRequest, "caseId, customerEmail, and subject are required.")
		return
	}

	if err := h.router.CreateWorkItem(r.Context(), req.toCaseInfo()); err != nil {
		if errors.Is(err, router.ErrDuplicateOpenChat) {
			writeError(w, http.StatusConflict, "This customer already has an open live chat for this project.")
			return
		}
		writeStorageError(w, "workitem:create", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"created": true})
}

// commentRequest is the body for POST /route/comment
type commentRequest struct {
	CaseID      string `json:"caseId"`
	AuthorEmail string `json:"authorEmail"`
	Content     string `json:"content"`
}

// AddComment handles POST /route/comment
func (h *RoutingHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.CaseID == "" || req.AuthorEmail == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "caseId, authorEmail, and content are required.")
		return
	}

	if err := h.router.AddComment(r.Context(), req.CaseID, req.AuthorEmail, req.Content); err != nil {
		if errors.Is(err, router.ErrConversationEnded) {
			writeError(w, http.StatusGone, "This chat session has already ended.")
			return
		}
		writeStorageError(w, "comment:add", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"added": true})
}

// DebugWorkItem handles GET /route/debug/workitem/{caseId}
func (h *RoutingHandler) DebugWorkItem(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	if caseID == "" {
		writeError(w, http.StatusBadRequest, "caseId is required.")
		return
	}
	detail, err := h.router.DebugWorkItem(r.Context(), caseID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// DebugState handles GET /route/debug/state
func (h *RoutingHandler) DebugState(w http.ResponseWriter, r *http.Request) {
	state, err := h.router.DebugState(r.Context())
	if err != nil {
		writeStorageError(w, "debug/state", err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// SweepTimeouts handles POST /route/sweep-timeouts. Called periodically by
// csm-portal/backend rather than run as this process's own ticker, so the
// component that owns the engineer SSE hub is also the one deciding when
// to look and delivering whatever this returns.
func (h *RoutingHandler) SweepTimeouts(w http.ResponseWriter, r *http.Request) {
	results, err := h.router.SweepExpiredPending(r.Context(), h.pendingTimeout)
	if err != nil {
		writeStorageError(w, "sweep-timeouts", err)
		return
	}
	if results == nil {
		results = []router.TimeoutResult{}
	}

	abandoned, err := h.router.SweepAbandonedQueue(r.Context(), h.queueAbandonTimeout)
	if err != nil {
		writeStorageError(w, "sweep-timeouts:abandoned", err)
		return
	}
	if abandoned == nil {
		abandoned = []router.AbandonedResult{}
	}

	stale, err := h.router.SweepStaleAcceptedSessions(r.Context(), h.staleSessionTimeout)
	if err != nil {
		writeStorageError(w, "sweep-timeouts:stale", err)
		return
	}
	if stale == nil {
		stale = []router.StaleSessionResult{}
	}

	writeJSON(w, http.StatusOK, struct {
		Results   []router.TimeoutResult      `json:"results"`
		Abandoned []router.AbandonedResult    `json:"abandoned"`
		Stale     []router.StaleSessionResult `json:"stale"`
	}{Results: results, Abandoned: abandoned, Stale: stale})
}

// capacityRequest is the body for PATCH /route/capacity.
type capacityRequest struct {
	UserID             string `json:"userId"`
	MaxConcurrentChats int    `json:"maxConcurrentChats"`
}

// SetCapacity handles PATCH /route/capacity - lets an engineer set their
// own configurable concurrent-chat capacity.
func (h *RoutingHandler) SetCapacity(w http.ResponseWriter, r *http.Request) {
	var req capacityRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "userId is required.")
		return
	}

	result, err := h.router.SetMaxConcurrentChats(r.Context(), req.UserID, req.MaxConcurrentChats)
	if err != nil {
		if errors.Is(err, router.ErrInvalidCapacity) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeStorageError(w, "capacity:set", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// caseInfoResponse mirrors router.CaseInfo — kept as its own type (rather
// than encoding router.CaseInfo directly) so this endpoint's wire shape can
// diverge from the router's internal one if it ever needs to.
type caseInfoResponse struct {
	CaseID         string                `json:"caseId"`
	ConversationID string                `json:"conversationId"`
	ProjectID      string                `json:"projectId,omitempty"`
	Source         string                `json:"source,omitempty"`
	Channel        string                `json:"channel,omitempty"`
	TenantSlug     string                `json:"tenantSlug,omitempty"`
	Subject        string                `json:"subject,omitempty"`
	CustomerEmail  string                `json:"customerEmail,omitempty"`
	CustomerName   string                `json:"customerName,omitempty"`
	Message        string                `json:"message,omitempty"`
	PriorMessages  []router.PriorMessage `json:"priorMessages,omitempty"`
}

func caseInfoToResponse(c router.CaseInfo) caseInfoResponse {
	return caseInfoResponse{
		CaseID:         c.CaseID,
		ConversationID: c.ConversationID,
		ProjectID:      c.ProjectID,
		Source:         c.Source,
		Channel:        c.Channel,
		TenantSlug:     c.TenantSlug,
		Subject:        c.Subject,
		CustomerEmail:  c.CustomerEmail,
		CustomerName:   c.CustomerName,
		Message:        c.Message,
		PriorMessages:  c.PriorMessages,
	}
}

// GetCaseInfo handles POST /route/workitem/{caseId}/info, returning
// caseId's case details with PriorMessages read fresh from the comment
// transcript (see router.CaseInfo.PriorMessages). Responds 404 if caseId
// is unknown.
func (h *RoutingHandler) GetCaseInfo(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	if caseID == "" {
		writeError(w, http.StatusBadRequest, "caseId is required.")
		return
	}
	ci, err := h.router.GetCaseInfo(r.Context(), caseID)
	if err != nil {
		if errors.Is(err, router.ErrConversationNotFound) {
			writeError(w, http.StatusNotFound, "No chat_conversation row exists for this case.")
			return
		}
		writeStorageError(w, "workitem:info", err)
		return
	}
	writeJSON(w, http.StatusOK, caseInfoToResponse(ci))
}

// convertToCaseRequest is the body for POST /route/convert-to-case.
// EntityCaseID is the real entity-service case ID the caller already
// created before calling this endpoint.
type convertToCaseRequest struct {
	UserID       string `json:"userId"`
	CaseID       string `json:"caseId"`
	EntityCaseID string `json:"entityCaseId"`
}

// convertToCaseResponse is ConvertToCase's response shape. AssignedCase is
// present only when converting freed a slot that immediately backfilled
// from the waiting queue
type convertToCaseResponse struct {
	AssignedCase *caseInfoResponse `json:"assignedCase,omitempty"`
}

// ConvertToCase handles POST /route/convert-to-case — ends caseId's chat
// session and records entityCaseId against it. userId must be the engineer
// currently holding caseId in an accepted (ACTIVE) session.
func (h *RoutingHandler) ConvertToCase(w http.ResponseWriter, r *http.Request) {
	var req convertToCaseRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.UserID == "" || req.CaseID == "" || req.EntityCaseID == "" {
		writeError(w, http.StatusBadRequest, "userId, caseId, and entityCaseId are required.")
		return
	}

	result, err := h.router.ConvertToCase(r.Context(), req.UserID, req.CaseID, req.EntityCaseID)
	if err != nil {
		switch {
		case errors.Is(err, router.ErrConversationNotFound):
			writeError(w, http.StatusNotFound, "No chat_conversation row exists for this case.")
		case errors.Is(err, router.ErrNotConversationOwner):
			writeError(w, http.StatusConflict, "This case is not an active conversation held by this engineer.")
		case errors.Is(err, router.ErrAlreadyConverted):
			writeError(w, http.StatusConflict, "This chat has already been converted to a case or otherwise ended.")
		default:
			writeStorageError(w, "convert-to-case", err)
		}
		return
	}

	resp := convertToCaseResponse{}
	if result.AssignedCase != nil {
		ci := caseInfoToResponse(*result.AssignedCase)
		resp.AssignedCase = &ci
	}
	writeJSON(w, http.StatusOK, resp)
}

// endByTenantRequest is the body for POST /route/end-by-tenant.
type endByTenantRequest struct {
	CaseID     string `json:"caseId"`
	TenantSlug string `json:"tenantSlug"`
}

// completedResponse mirrors router.CompletedResult — reused as-is for both
// POST /route/completed (which just marshals the result directly, so this
// type only formalizes that existing shape) and POST /route/end-by-tenant
// below.
type completedResponse struct {
	Ended              bool              `json:"ended,omitempty"`
	AssignedCase       *caseInfoResponse `json:"assignedCase,omitempty"`
	AssignedEngineerID string            `json:"assignedEngineerId,omitempty"`
}

func completedToResponse(result router.CompletedResult) completedResponse {
	resp := completedResponse{Ended: result.Ended, AssignedEngineerID: result.AssignedEngineerID}
	if result.AssignedCase != nil {
		ci := caseInfoToResponse(*result.AssignedCase)
		resp.AssignedCase = &ci
	}
	return resp
}

// EndByTenant handles POST /route/end-by-tenant, ending caseId's chat
// session on behalf of tenantSlug rather than a specific engineer. Ended
// is false, not an error, if caseId isn't currently an open conversation
// belonging to tenantSlug.
func (h *RoutingHandler) EndByTenant(w http.ResponseWriter, r *http.Request) {
	var req endByTenantRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.CaseID == "" || req.TenantSlug == "" {
		writeError(w, http.StatusBadRequest, "caseId and tenantSlug are required.")
		return
	}

	result, err := h.router.EndByTenant(r.Context(), req.CaseID, req.TenantSlug)
	if err != nil {
		writeStorageError(w, "end-by-tenant", err)
		return
	}
	writeJSON(w, http.StatusOK, completedToResponse(result))
}

// openChatResponse is FindOpenChat's response. OpenChat is null when the
// customer has no open chat for the project.
type openChatResponse struct {
	OpenChat *router.OpenChat `json:"openChat"`
}

// FindOpenChat handles GET /route/open-chat?customerEmail=&projectId=,
// returning the customer's chat that has not ended yet, if any.
func (h *RoutingHandler) FindOpenChat(w http.ResponseWriter, r *http.Request) {
	customerEmail := r.URL.Query().Get("customerEmail")
	projectID := r.URL.Query().Get("projectId")
	if customerEmail == "" || projectID == "" {
		writeError(w, http.StatusBadRequest, "customerEmail and projectId are required.")
		return
	}
	chat, found, err := h.router.FindOpenChat(r.Context(), customerEmail, projectID)
	if err != nil {
		writeStorageError(w, "open-chat", err)
		return
	}
	resp := openChatResponse{}
	if found {
		resp.OpenChat = &chat
	}
	writeJSON(w, http.StatusOK, resp)
}
