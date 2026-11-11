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
	errMsgWso2CloudNotAvailable     = "WSO2 Cloud does not offer this kind of change to the CSM portal yet. The request stays pending."
	errMsgWso2CloudNotAuthorized    = "WSO2 Cloud did not allow the CSM portal to make this change. The request stays pending."
	errMsgWso2CloudRefusedPrefix    = "WSO2 Cloud refused the change: "
	errMsgWso2CloudRefusedSuffix    = " The request stays pending."
	errMsgWso2CloudUnknownPlan      = "planCode is not one of the Enterprise plans configured for this product."
	errMsgWso2CloudSubscriptions    = "Failed to load the organization's subscriptions."
	errMsgWso2CloudSubsNotConnected = "WSO2 Cloud is not connected yet, so the organization's subscriptions cannot be loaded."
)

// errorCodeWso2CloudRefused names a refusal by WSO2 Cloud itself (its reason is
// in the message), so a client can tell it from the portal's own refusals.
const errorCodeWso2CloudRefused = "wso2_cloud_refused"

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
	entity        entityWso2CloudRequestClient
	applier       wso2cloud.Applier
	subscriptions wso2cloud.SubscriptionLister
	plans         wso2cloud.Plans
	access        *AccessGuard
}

// NewWso2CloudRequestHandler creates a Wso2CloudRequestHandler. A nil applier
// refuses every approval (wso2cloud.NotConnected).
func NewWso2CloudRequestHandler(entity entityWso2CloudRequestClient, applier wso2cloud.Applier) *Wso2CloudRequestHandler {
	if applier == nil {
		applier = wso2cloud.NotConnected{}
	}
	return &Wso2CloudRequestHandler{entity: entity, applier: applier, plans: wso2cloud.Plans{}}
}

// WithSubscriptions lets ListSubscriptions read an organization's
// subscriptions. Without it the route answers that WSO2 Cloud is not connected.
func (h *Wso2CloudRequestHandler) WithSubscriptions(l wso2cloud.SubscriptionLister) *Wso2CloudRequestHandler {
	h.subscriptions = l
	return h
}

// WithPlans sets the Enterprise plans an upgrade may ask for
// (WSO2_CLOUD_ENTERPRISE_PLANS). Without any, no upgrade can be raised.
func (h *Wso2CloudRequestHandler) WithPlans(p wso2cloud.Plans) *Wso2CloudRequestHandler {
	if p == nil {
		p = wso2cloud.Plans{}
	}
	h.plans = p
	return h
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
	// An upgrade must ask for a configured Enterprise plan of its product:
	// WSO2 Cloud would refuse any other plan code, but only at approval.
	// encoding/json matches keys without regard to case, as entity-service's
	// decoder does, so a respelt key cannot slip a plan past this check.
	var upgrade struct {
		Type     string  `json:"type"`
		Product  *string `json:"product"`
		PlanCode *string `json:"planCode"`
	}
	if err := json.Unmarshal(body, &upgrade); err != nil {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}
	if strings.TrimSpace(upgrade.Type) == "upgrade_organization" && upgrade.PlanCode != nil &&
		(upgrade.Product == nil || !h.plans.Has(*upgrade.Product, *upgrade.PlanCode)) {
		writeError(w, http.StatusBadRequest, errMsgWso2CloudUnknownPlan)
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
	rec := billingAccountRecorder{entity: h.entity, path: path + "/billing-account"}
	if err := h.applier.Apply(r.Context(), stored.Request, rec); err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: approved change not applied", "userID", callerID, "requestId", id, "type", stored.Type, "err", err)
		writeApplyError(w, err)
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

// writeApplyError says why an approved change could not be made. In every case
// the request stays pending, so it can be approved again once the cause is
// fixed (a plan change is safe to repeat and the billing account is reused).
func writeApplyError(w http.ResponseWriter, err error) {
	var refused *wso2cloud.APIError
	switch {
	case errors.Is(err, wso2cloud.ErrNotConnected):
		writeError(w, http.StatusServiceUnavailable, errMsgWso2CloudNotConnected)
	case errors.Is(err, wso2cloud.ErrNotAvailable):
		writeError(w, http.StatusServiceUnavailable, errMsgWso2CloudNotAvailable)
	case errors.Is(err, wso2cloud.ErrNotAuthorized):
		// The CSM backend's client lacks a scope, or its token was refused:
		// a configuration problem, not the approver's.
		writeError(w, http.StatusBadGateway, errMsgWso2CloudNotAuthorized)
	case errors.As(err, &refused):
		// WSO2 Cloud's own reason (a suspended subscription, usage over the
		// plan's limits, ...) is what the approver needs to act on.
		writeErrorCode(w, http.StatusConflict, errMsgWso2CloudRefusedPrefix+refused.Message+errMsgWso2CloudRefusedSuffix, errorCodeWso2CloudRefused)
	default:
		writeError(w, http.StatusBadGateway, errMsgWso2CloudApplyFailed)
	}
}

// billingAccountRecorder stores, on the request being approved, the billing
// account the applier created, through entity-service's
// POST /wso2-cloud-requests/{id}/billing-account.
type billingAccountRecorder struct {
	entity entityWso2CloudRequestClient
	path   string
}

func (b billingAccountRecorder) RecordBillingAccount(ctx context.Context, billingAccountID string) error {
	body, err := json.Marshal(map[string]string{"billingAccountId": billingAccountID})
	if err != nil {
		return err
	}
	_, err = b.entity.Wso2CloudRequestCall(ctx, http.MethodPost, b.path, body)
	return err
}

// ListSubscriptions handles GET /wso2-cloud/organizations/{orgId}/subscriptions:
// the organization's product subscriptions, so an upgrade can name the one it
// moves. 503 when WSO2 Cloud is not connected.
func (h *Wso2CloudRequestHandler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}
	orgID := r.PathValue("orgId")
	if !uuidRe.MatchString(orgID) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}
	lister := h.subscriptions
	if lister == nil {
		writeError(w, http.StatusServiceUnavailable, errMsgWso2CloudSubsNotConnected)
		return
	}
	subs, err := lister.ListSubscriptions(r.Context(), strings.ToLower(orgID))
	if err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: list subscriptions failed", "userID", user.UserID, "orgId", orgID, "err", err)
		var refused *wso2cloud.APIError
		switch {
		case errors.Is(err, wso2cloud.ErrNotConnected):
			writeError(w, http.StatusServiceUnavailable, errMsgWso2CloudSubsNotConnected)
		case errors.As(err, &refused) && refused.Status == http.StatusNotFound:
			writeError(w, http.StatusNotFound, ErrMsgNotFound)
		default:
			writeError(w, http.StatusBadGateway, errMsgWso2CloudSubscriptions)
		}
		return
	}
	if subs == nil {
		subs = []wso2cloud.Subscription{}
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

// ListPlans handles GET /wso2-cloud/plans: the Enterprise plans an upgrade may
// ask for, per product (WSO2_CLOUD_ENTERPRISE_PLANS).
func (h *Wso2CloudRequestHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	if middleware.UserInfoFromContext(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"plans": h.plans})
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
