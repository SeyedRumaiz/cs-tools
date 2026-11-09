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

package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// WSO2CloudRequestHandler serves the requests raised from the CSM portal's WSO2 Cloud tab.
type WSO2CloudRequestHandler struct {
	svc service.WSO2CloudRequestService
}

// NewWSO2CloudRequestHandler constructs a WSO2CloudRequestHandler.
func NewWSO2CloudRequestHandler(svc service.WSO2CloudRequestService) *WSO2CloudRequestHandler {
	return &WSO2CloudRequestHandler{svc: svc}
}

func writeWSO2CloudRequestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// CreateWSO2CloudRequest handles POST /wso2-cloud-requests.
func (h *WSO2CloudRequestHandler) CreateWSO2CloudRequest(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateWSO2CloudRequestRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	out, err := h.svc.Create(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeWSO2CloudRequestJSON(w, http.StatusCreated, out)
}

// SearchWSO2CloudRequests handles POST /wso2-cloud-requests/search.
func (h *WSO2CloudRequestHandler) SearchWSO2CloudRequests(w http.ResponseWriter, r *http.Request) {
	var req domain.SearchWSO2CloudRequestsRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	out, err := h.svc.Search(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeWSO2CloudRequestJSON(w, http.StatusOK, out)
}

// GetWSO2CloudRequest handles GET /wso2-cloud-requests/{id}.
func (h *WSO2CloudRequestHandler) GetWSO2CloudRequest(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeWSO2CloudRequestJSON(w, http.StatusOK, out)
}

// ResolveWSO2CloudRequest handles POST /wso2-cloud-requests/{id}/resolve.
func (h *WSO2CloudRequestHandler) ResolveWSO2CloudRequest(w http.ResponseWriter, r *http.Request) {
	var req domain.ResolveWSO2CloudRequestRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	out, err := h.svc.Resolve(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeWSO2CloudRequestJSON(w, http.StatusOK, out)
}

// DeleteWSO2CloudRequest handles DELETE /wso2-cloud-requests/{id}: cancels a pending request.
func (h *WSO2CloudRequestHandler) DeleteWSO2CloudRequest(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
