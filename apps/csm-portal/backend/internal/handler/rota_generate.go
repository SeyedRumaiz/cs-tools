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
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

// entityRotaGenerateClient is the one entity-service call the "Generate
// month" action needs. Its own interface rather than a method on
// entityScheduleClient, so nothing that fakes the schedule client changes.
type entityRotaGenerateClient interface {
	GenerateRotaMonth(ctx context.Context, code string, body []byte) ([]byte, error)
}

// RotaGenerateHandler proxies the Team Schedule's "Generate month" action.
// Who may generate is decided by entity-service (a lead of the rota's teams,
// or its admin), as it is for every rota edit; this only forwards the
// caller's request and their identity.
type RotaGenerateHandler struct {
	entity entityRotaGenerateClient
}

// NewRotaGenerateHandler creates a RotaGenerateHandler.
func NewRotaGenerateHandler(entity entityRotaGenerateClient) *RotaGenerateHandler {
	return &RotaGenerateHandler{entity: entity}
}

// GenerateRotaMonth handles POST /team-schedule/rotas/{code}/generate.
func (h *RotaGenerateHandler) GenerateRotaMonth(w http.ResponseWriter, r *http.Request) {
	body, userID, ok := readScheduleBody(w, r)
	if !ok {
		return
	}
	var req struct {
		DryRun bool `json:"dryRun"`
	}
	_ = json.Unmarshal(body, &req)

	result, err := h.entity.GenerateRotaMonth(r.Context(), r.PathValue("code"), body)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity GenerateRotaMonth failed", "userID", userID, "err", err)
		// The refusals say what to do next ("regenerate", "that month is
		// over", "needs a lead of one of its teams"), so they reach the lead
		// in entity-service's words; anything else is the generic message.
		var apiErr *apierror.Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict:
				writeError(w, apiErr.StatusCode, upstreamErrorMessageStrict(apiErr.Body, "Failed to generate the rota."))
				return
			}
		}
		mapUpstreamErrorGeneric(w, err, "Failed to generate the rota.")
		return
	}

	status := http.StatusCreated
	if req.DryRun {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}
