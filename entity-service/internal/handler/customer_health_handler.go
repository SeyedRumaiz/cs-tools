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

// Package handler is declared in user_handler.go.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// CustomerHealthHandler handles HTTP requests for the customer-health
// risk-tracking feature (migration 0219).
type CustomerHealthHandler struct {
	svc service.CustomerHealthService
}

// NewCustomerHealthHandler constructs a CustomerHealthHandler with the given
// service.
func NewCustomerHealthHandler(svc service.CustomerHealthService) *CustomerHealthHandler {
	return &CustomerHealthHandler{svc: svc}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// OpenProjectRisk handles POST /projects/{id}/risk.
func (h *CustomerHealthHandler) OpenProjectRisk(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "project ID is required")
		return
	}
	var req domain.OpenProjectRiskRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.OpenProjectRisk(r.Context(), projectID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// CloseProjectRisk handles PUT /risks/{id}/close.
func (h *CustomerHealthHandler) CloseProjectRisk(w http.ResponseWriter, r *http.Request) {
	riskID := r.PathValue("id")
	if riskID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "risk ID is required")
		return
	}
	var req domain.CloseProjectRiskRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.CloseProjectRisk(r.Context(), riskID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// MarkProjectHealthy handles POST /projects/{id}/mark-healthy.
func (h *CustomerHealthHandler) MarkProjectHealthy(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "project ID is required")
		return
	}
	var req domain.MarkProjectHealthyRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.MarkProjectHealthy(r.Context(), projectID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// RevertProjectHealth handles POST /projects/{id}/revert-health.
func (h *CustomerHealthHandler) RevertProjectHealth(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "project ID is required")
		return
	}
	resp, err := h.svc.RevertProjectHealth(r.Context(), projectID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetAccountProjectHealthStatuses handles GET /accounts/{id}/project-health-statuses.
func (h *CustomerHealthHandler) GetAccountProjectHealthStatuses(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if accountID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "account ID is required")
		return
	}
	resp, err := h.svc.GetAccountProjectHealthStatuses(r.Context(), accountID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetAccountHealthSummary handles GET /accounts/{id}/health-summary.
func (h *CustomerHealthHandler) GetAccountHealthSummary(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if accountID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "account ID is required")
		return
	}
	resp, err := h.svc.GetAccountHealthSummary(r.Context(), accountID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetBatchAccountHealthSummaries handles POST /accounts/health-summaries/search.
func (h *CustomerHealthHandler) GetBatchAccountHealthSummaries(w http.ResponseWriter, r *http.Request) {
	var req domain.BatchAccountHealthSummariesRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.GetBatchAccountHealthSummaries(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetAccountsByHealthStatus handles POST /accounts/by-health-status/search.
func (h *CustomerHealthHandler) GetAccountsByHealthStatus(w http.ResponseWriter, r *http.Request) {
	var req domain.AccountsByHealthStatusRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.GetAccountsByHealthStatus(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetProjectRiskHistory handles GET /projects/{id}/risk-history.
func (h *CustomerHealthHandler) GetProjectRiskHistory(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "project ID is required")
		return
	}
	resp, err := h.svc.GetProjectRiskHistory(r.Context(), projectID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// InitProjectHealthTracking handles POST /accounts/{id}/init-health-tracking.
func (h *CustomerHealthHandler) InitProjectHealthTracking(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if accountID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "account ID is required")
		return
	}
	var req domain.InitProjectHealthTrackingRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	if err := h.svc.InitProjectHealthTracking(r.Context(), accountID, req); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreateRiskActionItem handles POST /risks/{id}/action-items.
func (h *CustomerHealthHandler) CreateRiskActionItem(w http.ResponseWriter, r *http.Request) {
	riskID := r.PathValue("id")
	if riskID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "risk ID is required")
		return
	}
	var req domain.CreateRiskActionItemRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.CreateRiskActionItem(r.Context(), riskID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateRiskActionItemStatus handles PUT /action-items/{id}/status.
func (h *CustomerHealthHandler) UpdateRiskActionItemStatus(w http.ResponseWriter, r *http.Request) {
	actionItemID := r.PathValue("id")
	if actionItemID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "action item ID is required")
		return
	}
	var req domain.UpdateRiskActionItemStatusRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.UpdateRiskActionItemStatus(r.Context(), actionItemID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateRiskActionItem handles PUT /action-items/{id}.
func (h *CustomerHealthHandler) UpdateRiskActionItem(w http.ResponseWriter, r *http.Request) {
	actionItemID := r.PathValue("id")
	if actionItemID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "action item ID is required")
		return
	}
	var req domain.UpdateRiskActionItemRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.UpdateRiskActionItem(r.Context(), actionItemID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetActionItemsByRisk handles GET /risks/{id}/action-items.
func (h *CustomerHealthHandler) GetActionItemsByRisk(w http.ResponseWriter, r *http.Request) {
	riskID := r.PathValue("id")
	if riskID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "risk ID is required")
		return
	}
	var status *string
	if raw := r.URL.Query().Get("status"); raw != "" {
		status = &raw
	}
	resp, err := h.svc.GetActionItemsByRisk(r.Context(), riskID, status)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetActionItemsByAccount handles GET /accounts/{id}/action-items.
func (h *CustomerHealthHandler) GetActionItemsByAccount(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if accountID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "account ID is required")
		return
	}
	var projectID, status *string
	if raw := r.URL.Query().Get("projectId"); raw != "" {
		projectID = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		status = &raw
	}
	resp, err := h.svc.GetActionItemsByAccount(r.Context(), accountID, projectID, status)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateActionItemComment handles POST /action-items/{id}/comments.
func (h *CustomerHealthHandler) CreateActionItemComment(w http.ResponseWriter, r *http.Request) {
	actionItemID := r.PathValue("id")
	if actionItemID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "action item ID is required")
		return
	}
	var req domain.CreateActionItemCommentRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.CreateActionItemComment(r.Context(), actionItemID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// GetActionItemComments handles GET /action-items/{id}/comments.
func (h *CustomerHealthHandler) GetActionItemComments(w http.ResponseWriter, r *http.Request) {
	actionItemID := r.PathValue("id")
	if actionItemID == "" {
		apierror.WriteJSON(w, http.StatusBadRequest, "action item ID is required")
		return
	}
	resp, err := h.svc.GetActionItemComments(r.Context(), actionItemID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
