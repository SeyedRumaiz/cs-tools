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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// SLADurationPolicyHandler handles HTTP requests for GET /sla-duration-policy.
type SLADurationPolicyHandler struct {
	svc service.SLADurationPolicyService
}

// NewSLADurationPolicyHandler constructs an SLADurationPolicyHandler with the
// given service.
func NewSLADurationPolicyHandler(svc service.SLADurationPolicyService) *SLADurationPolicyHandler {
	return &SLADurationPolicyHandler{svc: svc}
}

// ListSLADurationPolicy handles GET /sla-duration-policy -- a plain GET with
// no query params, no pagination: at most 15 rows total, so there is nothing
// a caller would ever need to filter or page through.
func (h *SLADurationPolicyHandler) ListSLADurationPolicy(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.ListSLADurationPolicy(r.Context())
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
