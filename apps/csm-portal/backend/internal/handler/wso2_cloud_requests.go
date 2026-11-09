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
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/wso2cloud"
)

// Messages for the refusals this handler makes itself.
const (
	errMsgWso2CloudNotPending       = "This request is no longer pending."
	errMsgWso2CloudOwnApproval      = "You cannot approve a request you raised."
	errMsgWso2CloudCancelNotAllowed = "Only the person who raised this request, or an admin, can cancel it."
	errMsgWso2CloudNotConnected     = "WSO2 Cloud is not connected yet, so the change cannot be applied. The request stays pending."
	errMsgWso2CloudApplyFailed      = "The change could not be applied in WSO2 Cloud. The request stays pending."
)

// entityWso2CloudRequestClient is the entity-service call the WSO2 Cloud tab's
// requests need. It takes the raw JSON body (nil for none) and returns the raw
// answer.
type entityWso2CloudRequestClient interface {
	Wso2CloudRequestCall(ctx context.Context, method, path string, body []byte) ([]byte, error)
}

// Wso2CloudRequestHandler serves the WSO2 Cloud tab's requests. entity-service
// stores them and refuses what it can see is wrong (a second pending request of
// the same type for the same target, approving your own request, resolving one
// twice). This handler adds what entity-service cannot do: applying an approved
// change in WSO2 Cloud before it is recorded, and the cancel rule (the
// requester, or an admin).
type Wso2CloudRequestHandler struct {
	entity  entityWso2CloudRequestClient
	applier wso2cloud.Applier
	access  *AccessGuard
}

// NewWso2CloudRequestHandler creates a Wso2CloudRequestHandler. A nil applier
// refuses every approval (wso2cloud.NotConnected).
func NewWso2CloudRequestHandler(entity entityWso2CloudRequestClient, applier wso2cloud.Applier) *Wso2CloudRequestHandler {
	if applier == nil {
		applier = wso2cloud.NotConnected{}
	}
	return &Wso2CloudRequestHandler{entity: entity, applier: applier}
}

// WithAccessGuard lets CancelRequest recognise an admin. Without it only the
// requester can cancel.
func (h *Wso2CloudRequestHandler) WithAccessGuard(g *AccessGuard) *Wso2CloudRequestHandler {
	h.access = g
	return h
}

// storedWso2CloudRequest is the part of entity-service's request record this
// handler reads before acting on it.
type storedWso2CloudRequest struct {
	wso2cloud.Request
	Status      string `json:"status"`
	RequestedBy string `json:"requestedBy"`
}

// CreateRequest handles POST /wso2-cloud-requests. The requester is taken from
// the caller's token by entity-service, never from the body.
func (h *Wso2CloudRequestHandler) CreateRequest(w http.ResponseWriter, r *http.Request) {
	body, callerID, ok := readScheduleBody(w, r)
	if !ok {
		return
	}
	h.forward(w, r, callerID, http.MethodPost, "/wso2-cloud-requests", body, http.StatusCreated, "Failed to raise the request.")
}

// SearchRequests handles POST /wso2-cloud-requests/search.
func (h *Wso2CloudRequestHandler) SearchRequests(w http.ResponseWriter, r *http.Request) {
	body, callerID, ok := readScheduleBody(w, r)
	if !ok {
		return
	}
	h.forward(w, r, callerID, http.MethodPost, "/wso2-cloud-requests/search", body, http.StatusOK, "Failed to load requests.")
}

// GetRequest handles GET /wso2-cloud-requests/{id}.
func (h *Wso2CloudRequestHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	user, id, ok := userAndID(w, r)
	if !ok {
		return
	}
	h.forward(w, r, user.UserID, http.MethodGet, "/wso2-cloud-requests/"+id, nil, http.StatusOK, "Failed to load the request.")
}

// ResolveRequest handles POST /wso2-cloud-requests/{id}/resolve, body
// {status: approved|rejected, remark?}. A rejection is recorded straight away.
// An approval is applied in WSO2 Cloud first and recorded only once that
// worked; if it cannot be applied the request stays pending. The check that
// the request is pending and not the caller's own is made here too, before the
// change is applied: entity-service makes it again when recording, but by then
// the change would already be made.
func (h *Wso2CloudRequestHandler) ResolveRequest(w http.ResponseWriter, r *http.Request) {
	user, id, ok := userAndID(w, r)
	if !ok {
		return
	}
	body, callerID, ok := readScheduleBody(w, r)
	if !ok {
		return
	}
	var decision struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &decision); err != nil {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	path := "/wso2-cloud-requests/" + id
	const fallback = "Failed to record the decision."
	if decision.Status != "approved" {
		h.forward(w, r, callerID, http.MethodPost, path+"/resolve", body, http.StatusOK, fallback)
		return
	}

	stored, ok := h.load(w, r, callerID, path, fallback)
	if !ok {
		return
	}
	if stored.Status != "pending" {
		writeError(w, http.StatusConflict, errMsgWso2CloudNotPending)
		return
	}
	if strings.EqualFold(strings.TrimSpace(stored.RequestedBy), strings.TrimSpace(user.Email)) {
		writeError(w, http.StatusForbidden, errMsgWso2CloudOwnApproval)
		return
	}
	if err := h.applier.Apply(r.Context(), stored.Request); err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: approved change not applied", "userID", callerID, "requestId", id, "type", stored.Type, "err", err)
		if errors.Is(err, wso2cloud.ErrNotConnected) {
			writeError(w, http.StatusServiceUnavailable, errMsgWso2CloudNotConnected)
			return
		}
		writeError(w, http.StatusBadGateway, errMsgWso2CloudApplyFailed)
		return
	}
	result, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodPost, path+"/resolve", body)
	if err != nil {
		// The change is made in WSO2 Cloud but not recorded: the request still
		// reads pending. Logged at ERROR so it can be put right by hand.
		slog.ErrorContext(r.Context(), "wso2 cloud: change applied but the approval was not recorded", "userID", callerID, "requestId", id, "type", stored.Type, "err", err)
		h.writeUpstreamError(w, err, fallback)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// CancelRequest handles DELETE /wso2-cloud-requests/{id}: withdraws a pending
// request. Only its requester or an admin may.
func (h *Wso2CloudRequestHandler) CancelRequest(w http.ResponseWriter, r *http.Request) {
	user, id, ok := userAndID(w, r)
	if !ok {
		return
	}
	path := "/wso2-cloud-requests/" + id
	const fallback = "Failed to cancel the request."
	stored, ok := h.load(w, r, user.UserID, path, fallback)
	if !ok {
		return
	}
	if stored.Status != "pending" {
		writeError(w, http.StatusConflict, errMsgWso2CloudNotPending)
		return
	}
	own := strings.EqualFold(strings.TrimSpace(stored.RequestedBy), strings.TrimSpace(user.Email))
	if !own && (h.access == nil || !h.access.Permits(PermAdmin, user.Roles)) {
		writeError(w, http.StatusForbidden, errMsgWso2CloudCancelNotAllowed)
		return
	}
	if _, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodDelete, path, nil); err != nil {
		slog.ErrorContext(r.Context(), "entity wso2 cloud request call failed", "userID", user.UserID, "method", http.MethodDelete, "path", path, "err", err)
		h.writeUpstreamError(w, err, fallback)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// userAndID checks the caller is signed in and the {id} path value is a UUID.
func userAndID(w http.ResponseWriter, r *http.Request) (*middleware.UserInfo, string, bool) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return nil, "", false
	}
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return nil, "", false
	}
	return user, strings.ToLower(id), true
}

// load reads the stored request. It writes the response and returns false when
// it cannot.
func (h *Wso2CloudRequestHandler) load(w http.ResponseWriter, r *http.Request, callerID, path, fallback string) (storedWso2CloudRequest, bool) {
	var stored storedWso2CloudRequest
	raw, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodGet, path, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity wso2 cloud request call failed", "userID", callerID, "method", http.MethodGet, "path", path, "err", err)
		h.writeUpstreamError(w, err, fallback)
		return stored, false
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		slog.ErrorContext(r.Context(), "entity wso2 cloud request: unreadable record", "userID", callerID, "path", path, "err", err)
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return stored, false
	}
	return stored, true
}

// forward makes a call whose answer is passed through.
func (h *Wso2CloudRequestHandler) forward(w http.ResponseWriter, r *http.Request, callerID, method, path string, body []byte, okStatus int, fallback string) {
	result, err := h.entity.Wso2CloudRequestCall(r.Context(), method, path, body)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity wso2 cloud request call failed", "userID", callerID, "method", method, "path", path, "err", err)
		h.writeUpstreamError(w, err, fallback)
		return
	}
	writeJSON(w, okStatus, result)
}

// writeUpstreamError keeps entity-service's own reason (and errorCode) for a
// refusal (400, 403, 404, 409): its messages are written for the person
// raising or deciding a request ("a lock user request for this target is
// already pending", "a remark is required to reject"). A 5xx is the fallback.
func (h *Wso2CloudRequestHandler) writeUpstreamError(w http.ResponseWriter, err error, fallback string) {
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict:
			writeErrorCode(w, apiErr.StatusCode, upstreamErrorMessageStrict(apiErr.Body, fallback), upstreamErrorCode(apiErr.Body))
			return
		}
	}
	mapUpstreamError(w, err, fallback)
}
