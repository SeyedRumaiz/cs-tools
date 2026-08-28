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

// Package handler implements the HTTP surface over internal/router.Router.
// Every route here is server-to-server only (see internal/middleware.
// InternalToken) — csm-portal/backend is the only real caller, via its own
// internal/routingclient package.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/router"
)

// maxBodyBytes bounds request bodies — generous for these small JSON
// payloads while still capping memory use.
const maxBodyBytes = 64 << 10 // 64 KiB

// RoutingHandler adapts HTTP requests to router.Router calls.
type RoutingHandler struct {
	router *router.Router
}

// NewRoutingHandler constructs a RoutingHandler over r.
func NewRoutingHandler(r *router.Router) *RoutingHandler {
	return &RoutingHandler{router: r}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload.")
		return false
	}
	return true
}

func isValidStatus(s string) bool {
	switch router.Status(s) {
	case router.StatusAvailable, router.StatusBusy, router.StatusOffline:
		return true
	default:
		return false
	}
}

// escalateRequest is the body csm-portal/backend sends for
// POST /route/escalate — one field per router.CaseInfo field.
type escalateRequest struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId"`
	Subject        string `json:"subject"`
	CustomerEmail  string `json:"customerEmail"`
	CustomerName   string `json:"customerName"`
	Message        string `json:"message"`
}

// Escalate handles POST /route/escalate.
func (h *RoutingHandler) Escalate(w http.ResponseWriter, r *http.Request) {
	var req escalateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.CaseID == "" || req.ConversationID == "" {
		writeError(w, http.StatusBadRequest, "caseId and conversationId are required.")
		return
	}

	result := h.router.Escalate(router.CaseInfo{
		CaseID:         req.CaseID,
		ConversationID: req.ConversationID,
		ProjectID:      req.ProjectID,
		Subject:        req.Subject,
		CustomerEmail:  req.CustomerEmail,
		CustomerName:   req.CustomerName,
		Message:        req.Message,
	})
	writeJSON(w, http.StatusOK, result)
}

// presenceRequest is the body for POST /route/presence.
type presenceRequest struct {
	Email  string `json:"email"`
	Status string `json:"status"`
}

// SetPresence handles POST /route/presence.
func (h *RoutingHandler) SetPresence(w http.ResponseWriter, r *http.Request) {
	var req presenceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Email == "" || !isValidStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "email and a valid status (AVAILABLE|BUSY|OFFLINE) are required.")
		return
	}

	result := h.router.SetPresence(req.Email, router.Status(req.Status))
	writeJSON(w, http.StatusOK, result)
}

// completedRequest is the body for POST /route/completed.
type completedRequest struct {
	Email string `json:"email"`
}

// Completed handles POST /route/completed.
func (h *RoutingHandler) Completed(w http.ResponseWriter, r *http.Request) {
	var req completedRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required.")
		return
	}

	result := h.router.Completed(req.Email)
	writeJSON(w, http.StatusOK, result)
}

// declineRequest is the body for POST /route/decline.
type declineRequest struct {
	Email  string `json:"email"`
	CaseID string `json:"caseId"`
}

// Decline handles POST /route/decline.
func (h *RoutingHandler) Decline(w http.ResponseWriter, r *http.Request) {
	var req declineRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Email == "" || req.CaseID == "" {
		writeError(w, http.StatusBadRequest, "email and caseId are required.")
		return
	}

	result := h.router.Decline(req.Email, req.CaseID)
	writeJSON(w, http.StatusOK, result)
}

// GetPresence handles GET /route/presence/{email}.
func (h *RoutingHandler) GetPresence(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("email")
	if email == "" {
		writeError(w, http.StatusBadRequest, "email is required.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]router.Status{"status": h.router.GetPresence(email)})
}

// DebugState handles GET /route/debug/state — see router.DebugState's own
// doc comment on why this exists.
func (h *RoutingHandler) DebugState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.router.DebugState())
}
