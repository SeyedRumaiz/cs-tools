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

// PagingChainHandler serves the Team Schedule's Case Paging tab.
type PagingChainHandler struct {
	svc service.PagingChainService
}

// NewPagingChainHandler constructs a PagingChainHandler.
func NewPagingChainHandler(svc service.PagingChainService) *PagingChainHandler {
	return &PagingChainHandler{svc: svc}
}

// GetPagingChain handles GET /team-schedule/paging-chain?family=CRE|SRE.
func (h *PagingChainHandler) GetPagingChain(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.GetPagingChain(r.Context(), r.URL.Query().Get("family"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// UpdatePagingMember handles PATCH /team-schedule/paging-chain/members/{membershipId}.
func (h *PagingChainHandler) UpdatePagingMember(w http.ResponseWriter, r *http.Request) {
	var req domain.UpdatePagingMemberRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.MembershipID = r.PathValue("membershipId")
	member, err := h.svc.UpdatePagingMember(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(member)
}

// GetPagingReadiness handles GET /team-schedule/paging-readiness?days=7.
func (h *PagingChainHandler) GetPagingReadiness(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.GetPagingReadiness(r.Context(), r.URL.Query().Get("days"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ListPagingContacts handles GET /team-schedule/paging-contacts?emails=a,b.
func (h *PagingChainHandler) ListPagingContacts(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.ListPagingContacts(r.Context(), splitCommaList(r.URL.Query().Get("emails")))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// SetPagingPhone handles PUT /team-schedule/paging-contacts/{userId}.
func (h *PagingChainHandler) SetPagingPhone(w http.ResponseWriter, r *http.Request) {
	var req domain.SetPagingPhoneRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.UserID = r.PathValue("userId")
	phone, err := h.svc.SetPagingPhone(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(phone)
}

// DeletePagingPhone handles DELETE /team-schedule/paging-contacts/{userId}.
func (h *PagingChainHandler) DeletePagingPhone(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeletePagingPhone(r.Context(), r.PathValue("userId")); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RequestTestCall handles POST /team-schedule/paging-contacts/{userId}/test.
// 202: the call is placed by another service, after this returns.
func (h *PagingChainHandler) RequestTestCall(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.RequestTestCall(r.Context(), r.PathValue("userId"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

// RecordTestResult handles PUT /team-schedule/paging-contacts/{userId}/test-result.
func (h *PagingChainHandler) RecordTestResult(w http.ResponseWriter, r *http.Request) {
	var req domain.PagingTestResultRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.UserID = r.PathValue("userId")
	if err := h.svc.RecordTestResult(r.Context(), req); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
