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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// SalesforcePartnerHandler handles POST /salesforce/accounts/{sfId}/refresh-partners.
type SalesforcePartnerHandler struct {
	svc service.PartnerRefreshService
}

// NewSalesforcePartnerHandler constructs a SalesforcePartnerHandler.
func NewSalesforcePartnerHandler(svc service.PartnerRefreshService) *SalesforcePartnerHandler {
	return &SalesforcePartnerHandler{svc: svc}
}

// RefreshPartners handles POST /salesforce/accounts/{sfId}/refresh-partners:
// it re-reads the account's partners from Salesforce, stores them, and
// answers with the resulting set. Internal callers only (the service
// enforces it).
func (h *SalesforcePartnerHandler) RefreshPartners(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Refresh(r.Context(), r.PathValue("sfId"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
