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

// OutageCommunicationHandler serves the SRE-facing outage communication
// sweep — the port of ServiceNow's `Outage Communication` flow.
//
// Three near-neighbours in this codebase, and the distinction matters when
// reading a route table:
//
//	OutageHandler                the outage entity API
//	OutageNotificationHandler    the internal STAKEHOLDER notifier (flow 1)
//	OutageCommunicationHandler   this — the SRE declaration/resolution pair
type OutageCommunicationHandler struct {
	svc service.OutageCommunicationService
}

// NewOutageCommunicationHandler constructs the handler.
func NewOutageCommunicationHandler(svc service.OutageCommunicationService) *OutageCommunicationHandler {
	return &OutageCommunicationHandler{svc: svc}
}

// SweepOutageCommunications handles POST /outage-communications/sweep.
//
// POST rather than GET because it is not read-only: every decision is
// written to the communication log before it is returned, which is what
// stops a second sweep reporting the same email again.
func (h *OutageCommunicationHandler) SweepOutageCommunications(w http.ResponseWriter, r *http.Request) {
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

// GetOutageCommunicationLog handles
// GET /outages/{number}/communication-log.
//
// Keyed on the outage NUMBER, not its id, because that is the only link the
// log has: ServiceNow's table carries a real reference column that is empty
// on every one of its 336 rows, and the association has always been the
// number string.
func (h *OutageCommunicationHandler) GetOutageCommunicationLog(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.Log(r.Context(), r.PathValue("number"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}
