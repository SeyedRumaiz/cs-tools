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
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// RotaGenerateHandler serves the "Generate month" action of the Team Schedule.
type RotaGenerateHandler struct {
	svc service.RotaGenerateService
}

// NewRotaGenerateHandler constructs a RotaGenerateHandler.
func NewRotaGenerateHandler(svc service.RotaGenerateService) *RotaGenerateHandler {
	return &RotaGenerateHandler{svc: svc}
}

// GenerateRotaMonth handles POST /team-schedule/rotas/{code}/generate.
func (h *RotaGenerateHandler) GenerateRotaMonth(w http.ResponseWriter, r *http.Request) {
	var req domain.GenerateRotaMonthRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.RotaCode = r.PathValue("code")
	res, err := h.svc.GenerateMonth(r.Context(), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	status := http.StatusOK
	if !res.DryRun {
		status = http.StatusCreated
	}
	writeScheduleJSON(w, status, res)
}
