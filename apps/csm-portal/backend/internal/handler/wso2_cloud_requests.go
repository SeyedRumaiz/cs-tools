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
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
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
	errMsgWso2CloudNoSubscription   = "The customer's Salesforce account no longer has an active subscription, so this upgrade cannot be approved. The request stays pending."
	errMsgWso2CloudSalesforceCheck  = "Failed to check the customer's Salesforce subscription. The request stays pending."
	errMsgWso2CloudBadAccountID     = "Enter a Salesforce Account Id: 15 or 18 letters and digits, starting with 001."
	errMsgWso2CloudRegionRequired   = "Choose the WSO2 Cloud region the organization is in."
	errMsgWso2CloudRegionNoRequest  = "This request names no WSO2 Cloud region this environment can reach, so it cannot be applied. Cancel it and raise it again with the right region."
)

// errorCodeWso2CloudRefused names a refusal by WSO2 Cloud itself (its reason is
// in the message), so a client can tell it from the portal's own refusals.
const errorCodeWso2CloudRefused = "wso2_cloud_refused"

// errorCodeWso2CloudNoSubscription is entity-service's code for a customer
// upgrade whose Salesforce account has no active opportunity; the approval
// check below uses it too.
const errorCodeWso2CloudNoSubscription = "wso2_cloud_no_subscription"

// salesforceAccountIDRe is a Salesforce Account Id: the 001 key prefix, 15 or
// 18 characters.
var salesforceAccountIDRe = regexp.MustCompile(`^001[A-Za-z0-9]{12}(?:[A-Za-z0-9]{3})?$`)

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
	// regions are the WSO2 Cloud regions this deployment can reach.
	regions []string
	plans   wso2cloud.Plans
	access  *AccessGuard
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
// subscriptions in the given WSO2 Cloud regions. Without it the route answers
// that WSO2 Cloud is not connected.
func (h *Wso2CloudRequestHandler) WithSubscriptions(l wso2cloud.SubscriptionLister, regions []string) *Wso2CloudRequestHandler {
	h.subscriptions = l
	h.regions = regions
	return h
}

// regionMessage is what a caller is told when a region cannot be used here.
func regionMessage(err error, regions []string) string {
	var unknown *wso2cloud.RegionError
	if errors.As(err, &unknown) {
		return fmt.Sprintf("The %s region is not available in this environment. Choose one of: %s.",
			strings.ToUpper(unknown.Region), strings.ToUpper(strings.Join(regions, ", ")))
	}
	return errMsgWso2CloudRegionRequired
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
	// UpgradeKind is customer or internal on an upgrade; only a customer
	// upgrade is checked and linked in Salesforce.
	UpgradeKind *string `json:"upgradeKind"`
}

func (s storedWso2CloudRequest) isCustomerUpgrade() bool {
	return s.Type == "upgrade_organization" && s.UpgradeKind != nil && *s.UpgradeKind == "customer"
}

// CreateRequest handles POST /wso2-cloud-requests. The requester is taken from
// the caller's token by entity-service, never from the body.
//
// A customer upgrade needs no approval: entity-service only stores one whose
// Salesforce account has an active subscription, and that is the proof. So
// once it is stored it is approved straight away, as the requester, through
// the same steps as an approval (the subscription is checked again, the change
// is applied in WSO2 Cloud, the approval is recorded, the organization is
// linked in Salesforce), and the answer is the approved request (201). If it
// cannot be applied the request stays pending, an approver can approve it
// later, and the answer is the apply error. Every other request waits for an
// approver.
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
		Region   *string `json:"region"`
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
	// The upgrade is applied through its region's billing API, so the region
	// must be one this deployment reaches (once WSO2 Cloud is connected at all).
	if strings.TrimSpace(upgrade.Type) == "upgrade_organization" && len(h.regions) > 0 {
		if _, err := wso2cloud.PickRegion(derefStr(upgrade.Region), h.regions); err != nil || upgrade.Region == nil {
			if err == nil {
				err = wso2cloud.ErrRegionRequired
			}
			writeError(w, http.StatusBadRequest, regionMessage(err, h.regions))
			return
		}
	}
	const fallback = "Failed to raise the request."
	created, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodPost, "/wso2-cloud-requests", body)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity wso2 cloud request call failed", "userID", callerID, "method", http.MethodPost, "path", "/wso2-cloud-requests", "err", err)
		h.writeUpstreamError(w, err, fallback)
		return
	}
	var stored storedWso2CloudRequest
	if err := json.Unmarshal(created, &stored); err != nil || !stored.isCustomerUpgrade() {
		writeJSON(w, http.StatusCreated, created)
		return
	}
	if !uuidRe.MatchString(stored.ID) {
		slog.ErrorContext(r.Context(), "wso2 cloud: created request has no usable id", "userID", callerID)
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}
	h.approve(w, r, callerID, "/wso2-cloud-requests/"+strings.ToLower(stored.ID), stored, []byte(`{"status":"approved"}`), http.StatusCreated)
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
//
// A customer upgrade is checked against Salesforce first: its account must
// still have an active subscription (409, nothing applied, otherwise). Once the
// approval is recorded its org is linked to the account in Salesforce. A link
// that fails does not undo the upgrade: the answer is still 200, and the
// request shows the failed link, which an approver can retry
// (PUT /wso2-cloud-requests/{id}/salesforce-link).
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
	// A customer upgrade needs no approval, so its requester may approve it
	// (entity-service allows the same); everything else needs someone else.
	if !stored.isCustomerUpgrade() && strings.EqualFold(strings.TrimSpace(stored.RequestedBy), strings.TrimSpace(user.Email)) {
		writeError(w, http.StatusForbidden, errMsgWso2CloudOwnApproval)
		return
	}
	h.approve(w, r, callerID, path, stored, body, http.StatusOK)
}

// approve applies a pending request in WSO2 Cloud and records the approval
// (body is the /resolve body). A customer upgrade's subscription is checked
// again first, and its organization is linked in Salesforce after. It writes
// the response: okStatus with the request, or why it could not be applied
// (the request then stays pending).
func (h *Wso2CloudRequestHandler) approve(w http.ResponseWriter, r *http.Request, callerID, path string, stored storedWso2CloudRequest, body []byte, okStatus int) {
	const fallback = "Failed to record the decision."
	if stored.isCustomerUpgrade() && !h.subscriptionStillActive(w, r, callerID, path) {
		return
	}
	rec := billingAccountRecorder{entity: h.entity, path: path + "/billing-account"}
	if err := h.applier.Apply(r.Context(), stored.Request, rec); err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: approved change not applied", "userID", callerID, "requestId", stored.ID, "type", stored.Type, "err", err)
		writeApplyError(w, err)
		return
	}
	result, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodPost, path+"/resolve", body)
	if err != nil {
		// The change is made in WSO2 Cloud but not recorded: the request still
		// reads pending. Logged at ERROR so it can be put right by hand.
		slog.ErrorContext(r.Context(), "wso2 cloud: change applied but the approval was not recorded", "userID", callerID, "requestId", stored.ID, "type", stored.Type, "err", err)
		h.writeUpstreamError(w, err, fallback)
		return
	}
	if stored.isCustomerUpgrade() {
		linked, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodPut, path+"/salesforce-link", nil)
		if err != nil {
			// The upgrade stands; the request reads approved with no link yet.
			slog.ErrorContext(r.Context(), "wso2 cloud: approved, but the salesforce link call failed", "userID", callerID, "requestId", stored.ID, "err", err)
		} else {
			result = linked
		}
	}
	writeJSON(w, okStatus, result)
}

// subscriptionStillActive re-reads, through entity-service, the Salesforce
// account a customer upgrade names, and refuses (409) when it no longer has an
// active subscription. It writes the response and returns false when the
// approval must not go ahead.
func (h *Wso2CloudRequestHandler) subscriptionStillActive(w http.ResponseWriter, r *http.Request, callerID, path string) bool {
	raw, err := h.entity.Wso2CloudRequestCall(r.Context(), http.MethodGet, path+"/salesforce-account", nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: salesforce subscription check failed", "userID", callerID, "path", path, "err", err)
		h.writeUpstreamError(w, err, errMsgWso2CloudSalesforceCheck)
		return false
	}
	var account struct {
		HasActiveSubscription bool `json:"hasActiveSubscription"`
	}
	if err := json.Unmarshal(raw, &account); err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: unreadable salesforce account", "userID", callerID, "path", path, "err", err)
		writeError(w, http.StatusBadGateway, errMsgWso2CloudSalesforceCheck)
		return false
	}
	if !account.HasActiveSubscription {
		writeErrorCode(w, http.StatusConflict, errMsgWso2CloudNoSubscription, errorCodeWso2CloudNoSubscription)
		return false
	}
	return true
}

// LinkSalesforce handles PUT /wso2-cloud-requests/{id}/salesforce-link: links
// an approved customer upgrade's org to its account in Salesforce again, after
// a link failed. entity-service decides whether the request can be linked and
// answers with the request and the link's outcome.
func (h *Wso2CloudRequestHandler) LinkSalesforce(w http.ResponseWriter, r *http.Request) {
	user, id, ok := userAndID(w, r)
	if !ok {
		return
	}
	h.forward(w, r, user.UserID, http.MethodPut, "/wso2-cloud-requests/"+id+"/salesforce-link", nil, http.StatusOK, "Failed to link the organization in Salesforce.")
}

// GetOrganizationSalesforce handles GET
// /wso2-cloud/organizations/{orgId}/salesforce: the org's Salesforce record and
// the account it is under, with that account's active subscriptions.
func (h *Wso2CloudRequestHandler) GetOrganizationSalesforce(w http.ResponseWriter, r *http.Request) {
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
	h.forward(w, r, user.UserID, http.MethodGet, "/wso2-cloud/organizations/"+strings.ToLower(orgID)+"/salesforce", nil, http.StatusOK, "Failed to load the organization's Salesforce details.")
}

// GetSalesforceAccount handles GET /wso2-cloud/salesforce-accounts/{accountId}:
// a Salesforce account's name and active subscriptions, so the person raising
// a customer upgrade can check the id first.
func (h *Wso2CloudRequestHandler) GetSalesforceAccount(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}
	accountID := r.PathValue("accountId")
	if !salesforceAccountIDRe.MatchString(accountID) {
		writeError(w, http.StatusBadRequest, errMsgWso2CloudBadAccountID)
		return
	}
	h.forward(w, r, user.UserID, http.MethodGet, "/wso2-cloud/salesforce-accounts/"+accountID, nil, http.StatusOK, "Failed to load the Salesforce account.")
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
	case errors.Is(err, wso2cloud.ErrRegionRequired), errors.As(err, new(*wso2cloud.RegionError)):
		writeError(w, http.StatusConflict, errMsgWso2CloudRegionNoRequest)
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
	region := ""
	if len(h.regions) > 0 {
		picked, err := wso2cloud.PickRegion(r.URL.Query().Get("region"), h.regions)
		if err != nil {
			writeError(w, http.StatusBadRequest, regionMessage(err, h.regions))
			return
		}
		region = picked
	}
	subs, err := lister.ListSubscriptions(r.Context(), region, strings.ToLower(orgID))
	if err != nil {
		slog.ErrorContext(r.Context(), "wso2 cloud: list subscriptions failed", "userID", user.UserID, "orgId", orgID, "region", region, "err", err)
		var refused *wso2cloud.APIError
		switch {
		case errors.Is(err, wso2cloud.ErrNotConnected):
			writeError(w, http.StatusServiceUnavailable, errMsgWso2CloudSubsNotConnected)
		case errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusBadRequest):
			// The organization is not in this region's WSO2 Cloud: most likely
			// the wrong region was chosen.
			writeError(w, http.StatusNotFound, fmt.Sprintf("This organization was not found in the %s region. Check the region.", strings.ToUpper(region)))
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

// ListRegions handles GET /wso2-cloud/regions: the WSO2 Cloud regions this
// deployment can reach, so the upgrade form offers only those.
func (h *Wso2CloudRequestHandler) ListRegions(w http.ResponseWriter, r *http.Request) {
	if middleware.UserInfoFromContext(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}
	regions := h.regions
	if regions == nil {
		regions = []string{}
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"regions": regions})
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
