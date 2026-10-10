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
	"strconv"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// OutageNotificationHandler serves the internal-stakeholder outage
// notification sweep. Distinct from OutageHandler, which is the outage entity
// API: this owns only the question "what should be emailed about outages
// right now", and it is backed by Postgres rather than ServiceNow.
type OutageNotificationHandler struct {
	svc service.OutageNotificationService
}

// NewOutageNotificationHandler constructs the handler.
func NewOutageNotificationHandler(svc service.OutageNotificationService) *OutageNotificationHandler {
	return &OutageNotificationHandler{svc: svc}
}

// SweepOutageNotifications handles POST /outage-notifications/sweep.
//
// POST rather than GET because it is not read-only: each decision is recorded
// before it is returned, so the same sweep run twice does not report the same
// email twice.
func (h *OutageNotificationHandler) SweepOutageNotifications(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeServiceError(w, r, &apierror.ValidationError{Msg: "limit must be an integer"})
			return
		}
		limit = parsed
	}

	resp, err := h.svc.Sweep(r.Context(), limit)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// GetOutageNotificationState handles
// GET /outages/{id}/notification-state — what has already been sent for one
// outage. Operational visibility, mainly for answering "why did (or didn't)
// this outage produce an email".
func (h *OutageNotificationHandler) GetOutageNotificationState(w http.ResponseWriter, r *http.Request) {
	state, err := h.svc.State(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
