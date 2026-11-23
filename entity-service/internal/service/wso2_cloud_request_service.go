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

package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/auth"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	maxWSO2CloudTargetIDLength    = 255
	maxWSO2CloudTargetLabelLength = 320
	maxWSO2CloudReasonLength      = 2000
	maxWSO2CloudConsentLength     = 1000
	maxWSO2CloudRemarkLength      = 2000
	maxWSO2CloudProductLength     = 100
	maxWSO2CloudPlanCodeLength    = 100
	maxWSO2CloudSubscriptionIDLen = 255
	maxWSO2CloudBillingAccountLen = 255
)

var validWSO2CloudRequestType = map[domain.WSO2CloudRequestType]bool{
	domain.WSO2CloudRequestUpgradeOrganization: true,
	domain.WSO2CloudRequestDeleteOrganization:  true,
	domain.WSO2CloudRequestLockUser:            true,
	domain.WSO2CloudRequestUnlockUser:          true,
	domain.WSO2CloudRequestDisableUser:         true,
	domain.WSO2CloudRequestEnableUser:          true,
	domain.WSO2CloudRequestDeleteUser:          true,
}

var validWSO2CloudRequestStatus = map[domain.WSO2CloudRequestStatus]bool{
	domain.WSO2CloudRequestPending:  true,
	domain.WSO2CloudRequestApproved: true,
	domain.WSO2CloudRequestRejected: true,
}

var validWSO2CloudBillingPeriod = map[domain.WSO2CloudBillingPeriod]bool{
	domain.WSO2CloudBillingMonthly: true,
	domain.WSO2CloudBillingAnnual:  true,
}

// wso2CloudRequestNeedsReason lists the types whose requester must say why.
var wso2CloudRequestNeedsReason = map[domain.WSO2CloudRequestType]bool{
	domain.WSO2CloudRequestLockUser:    true,
	domain.WSO2CloudRequestDisableUser: true,
}

const wso2CloudRequestForbiddenMsg = "WSO2 Cloud requests are available to internal callers only"

type wso2CloudRequestService struct {
	repo   repository.WSO2CloudRequestRepository
	access AccessService
	// salesforce is nil when Sales Entity is not configured: then a customer upgrade cannot be raised or linked.
	salesforce WSO2CloudSalesforce
}

// NewWSO2CloudRequestService constructs a WSO2CloudRequestService. salesforce may be nil (Sales Entity not
// configured); every call that needs it is then a ServiceUnavailableError, and internal upgrades still work.
func NewWSO2CloudRequestService(repo repository.WSO2CloudRequestRepository, access AccessService, salesforce WSO2CloudSalesforce) WSO2CloudRequestService {
	return &wso2CloudRequestService{repo: repo, access: access, salesforce: salesforce}
}

// wso2CloudCallerEmail is the person acting: requests are raised and resolved by people, so a call with no user token
// (machine-to-machine only) cannot do either.
func wso2CloudCallerEmail(ctx context.Context) (string, error) {
	email := strings.TrimSpace(auth.IdentityFromContext(ctx).UserEmail)
	if email == "" {
		return "", &apierror.UnauthorizedError{Msg: "a user token (x-user-id-token) is required: WSO2 Cloud requests are raised and resolved by people"}
	}
	return email, nil
}

func (s *wso2CloudRequestService) Create(ctx context.Context, req domain.CreateWSO2CloudRequestRequest) (domain.WSO2CloudRequest, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	requestedBy, err := wso2CloudCallerEmail(ctx)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	clean, err := normalizeWSO2CloudRequest(req)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if clean.UpgradeKind != nil && *clean.UpgradeKind == domain.WSO2CloudUpgradeCustomer {
		account, err := s.customerAccount(ctx, *clean.SalesforceAccountID)
		if err != nil {
			return domain.WSO2CloudRequest{}, err
		}
		// Store the Id as Salesforce spells it (18 characters), whatever form was typed.
		clean.SalesforceAccountID = &account.ID
	}
	return s.repo.Create(ctx, requestedBy, clean)
}

func (s *wso2CloudRequestService) Get(ctx context.Context, id string) (domain.WSO2CloudRequest, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if err := validateUUIDs("id", []string{id}); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	r, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if r == nil {
		return domain.WSO2CloudRequest{}, &apierror.NotFoundError{Msg: "WSO2 Cloud request not found"}
	}
	return *r, nil
}

func (s *wso2CloudRequestService) Search(ctx context.Context, req domain.SearchWSO2CloudRequestsRequest) (domain.SearchWSO2CloudRequestsResponse, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.SearchWSO2CloudRequestsResponse{}, err
	}
	for _, st := range req.Filters.Statuses {
		if !validWSO2CloudRequestStatus[st] {
			return domain.SearchWSO2CloudRequestsResponse{}, &apierror.ValidationError{Msg: fmt.Sprintf("statuses contains invalid value: %q", st)}
		}
	}
	for _, t := range req.Filters.Types {
		if !validWSO2CloudRequestType[t] {
			return domain.SearchWSO2CloudRequestsResponse{}, &apierror.ValidationError{Msg: fmt.Sprintf("types contains invalid value: %q", t)}
		}
	}
	req.Filters.TargetID = trimmedOrNil(req.Filters.TargetID)
	req.Filters.RequestedBy = trimmedOrNil(req.Filters.RequestedBy)
	if err := normalizePagination(&req.Pagination); err != nil {
		return domain.SearchWSO2CloudRequestsResponse{}, err
	}
	requests, total, err := s.repo.Search(ctx, req)
	if err != nil {
		return domain.SearchWSO2CloudRequestsResponse{}, err
	}
	return domain.SearchWSO2CloudRequestsResponse{
		Requests: requests,
		Total:    total,
		Limit:    req.Pagination.Limit,
		Offset:   req.Pagination.Offset,
	}, nil
}

func (s *wso2CloudRequestService) Resolve(ctx context.Context, id string, req domain.ResolveWSO2CloudRequestRequest) (domain.WSO2CloudRequest, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	resolvedBy, err := wso2CloudCallerEmail(ctx)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if err := validateUUIDs("id", []string{id}); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if req.Status != domain.WSO2CloudRequestApproved && req.Status != domain.WSO2CloudRequestRejected {
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: `status must be "approved" or "rejected"`}
	}
	remark := trimmedOrNil(req.Remark)
	if remark != nil && utf8.RuneCountInString(*remark) > maxWSO2CloudRemarkLength {
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: fmt.Sprintf("remark must be at most %d characters", maxWSO2CloudRemarkLength)}
	}
	if req.Status == domain.WSO2CloudRequestRejected && remark == nil {
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: "remark is required to reject a request"}
	}

	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if current == nil {
		return domain.WSO2CloudRequest{}, &apierror.NotFoundError{Msg: "WSO2 Cloud request not found"}
	}
	if current.Status != domain.WSO2CloudRequestPending {
		return domain.WSO2CloudRequest{}, &apierror.ConflictError{Msg: "this request has already been " + string(current.Status)}
	}
	// requested_by never changes, so this check needs no lock. A customer upgrade needs no approval (its active
	// subscription, checked when it was raised, is the proof), so its requester may approve it: the CSM backend does
	// that as soon as it is raised. They still may not reject it (that is a cancel).
	if strings.EqualFold(current.RequestedBy, resolvedBy) &&
		(req.Status != domain.WSO2CloudRequestApproved || !isCustomerUpgrade(*current)) {
		return domain.WSO2CloudRequest{}, &apierror.ForbiddenError{Msg: "a requester cannot approve or reject their own request"}
	}

	resolved, err := s.repo.Resolve(ctx, id, req.Status, resolvedBy, remark)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if resolved == nil {
		// Someone else resolved or cancelled it between the read and the write.
		return domain.WSO2CloudRequest{}, &apierror.ConflictError{Msg: "this request is no longer pending"}
	}
	return *resolved, nil
}

func isCustomerUpgrade(r domain.WSO2CloudRequest) bool {
	return r.Type == domain.WSO2CloudRequestUpgradeOrganization && r.UpgradeKind != nil && *r.UpgradeKind == domain.WSO2CloudUpgradeCustomer
}

func (s *wso2CloudRequestService) Delete(ctx context.Context, id string) error {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return err
	}
	if err := validateUUIDs("id", []string{id}); err != nil {
		return err
	}
	deleted, err := s.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if deleted {
		return nil
	}
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if current == nil {
		return &apierror.NotFoundError{Msg: "WSO2 Cloud request not found"}
	}
	return &apierror.ConflictError{Msg: "only a pending request can be cancelled; this one has been " + string(current.Status)}
}

// SetBillingAccount records the wire billing account the CSM backend created in WSO2 Cloud while approving an
// upgrade, so an approval that is retried reuses it. Only a pending upgrade takes one, and only once: the same id
// again is accepted unchanged, a different one is a ConflictError.
func (s *wso2CloudRequestService) SetBillingAccount(ctx context.Context, id string, req domain.SetWSO2CloudBillingAccountRequest) (domain.WSO2CloudRequest, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if _, err := wso2CloudCallerEmail(ctx); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if err := validateUUIDs("id", []string{id}); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	accountID := strings.TrimSpace(req.BillingAccountID)
	if accountID == "" {
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: "billingAccountId is required"}
	}
	if utf8.RuneCountInString(accountID) > maxWSO2CloudBillingAccountLen {
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: fmt.Sprintf("billingAccountId must be at most %d characters", maxWSO2CloudBillingAccountLen)}
	}
	updated, err := s.repo.SetBillingAccount(ctx, id, accountID)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if updated != nil {
		return *updated, nil
	}
	// Nothing was written: say why.
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	switch {
	case current == nil:
		return domain.WSO2CloudRequest{}, &apierror.NotFoundError{Msg: "WSO2 Cloud request not found"}
	case current.Type != domain.WSO2CloudRequestUpgradeOrganization:
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: "only an upgrade_organization request has a billing account"}
	case current.BillingAccountID != nil && *current.BillingAccountID == accountID:
		return *current, nil
	case current.BillingAccountID != nil:
		return domain.WSO2CloudRequest{}, &apierror.ConflictError{Msg: "this request already has a different billing account"}
	default:
		return domain.WSO2CloudRequest{}, &apierror.ConflictError{Msg: "this request has already been " + string(current.Status)}
	}
}

// normalizeWSO2CloudRequest trims every text field, turns blank optional text into nil, and checks the request is
// complete for its type: an upgrade names a subscription and an Enterprise plan (and may name the product, the plan it
// had, a billing period and whether to tell the customer), no other type carries any of those, and lock or disable
// needs a reason.
func normalizeWSO2CloudRequest(req domain.CreateWSO2CloudRequestRequest) (domain.CreateWSO2CloudRequestRequest, error) {
	if !validWSO2CloudRequestType[req.Type] {
		return req, apierror.InvalidValue("type", string(req.Type), "WSO2 Cloud request type", wso2CloudRequestTypeNames())
	}
	out := domain.CreateWSO2CloudRequestRequest{
		Type:            req.Type,
		TargetID:        strings.TrimSpace(req.TargetID),
		TargetLabel:     strings.TrimSpace(req.TargetLabel),
		Reason:          trimmedOrNil(req.Reason),
		CustomerConsent: trimmedOrNil(req.CustomerConsent),
	}
	if out.TargetID == "" {
		return req, &apierror.ValidationError{Msg: "targetId is required"}
	}
	if utf8.RuneCountInString(out.TargetID) > maxWSO2CloudTargetIDLength {
		return req, &apierror.ValidationError{Msg: fmt.Sprintf("targetId must be at most %d characters", maxWSO2CloudTargetIDLength)}
	}
	if out.TargetLabel == "" {
		return req, &apierror.ValidationError{Msg: "targetLabel is required"}
	}
	if utf8.RuneCountInString(out.TargetLabel) > maxWSO2CloudTargetLabelLength {
		return req, &apierror.ValidationError{Msg: fmt.Sprintf("targetLabel must be at most %d characters", maxWSO2CloudTargetLabelLength)}
	}
	if out.Reason != nil && utf8.RuneCountInString(*out.Reason) > maxWSO2CloudReasonLength {
		return req, &apierror.ValidationError{Msg: fmt.Sprintf("reason must be at most %d characters", maxWSO2CloudReasonLength)}
	}
	if wso2CloudRequestNeedsReason[req.Type] && out.Reason == nil {
		return req, &apierror.ValidationError{Msg: "reason is required for a " + strings.ReplaceAll(string(req.Type), "_", " ") + " request"}
	}
	if out.CustomerConsent != nil && utf8.RuneCountInString(*out.CustomerConsent) > maxWSO2CloudConsentLength {
		return req, &apierror.ValidationError{Msg: fmt.Sprintf("customerConsent must be at most %d characters", maxWSO2CloudConsentLength)}
	}

	product := trimmedOrNil(req.Product)
	subscriptionID := trimmedOrNil(req.SubscriptionID)
	planCode := trimmedOrNil(req.PlanCode)
	previousPlanCode := trimmedOrNil(req.PreviousPlanCode)
	sfAccountID := trimmedOrNil(req.SalesforceAccountID)
	region := trimmedOrNil(req.Region)
	if req.Type != domain.WSO2CloudRequestUpgradeOrganization {
		if product != nil || subscriptionID != nil || planCode != nil || previousPlanCode != nil || req.BillingPeriod != nil || req.NotifyCustomer ||
			req.UpgradeKind != nil || sfAccountID != nil || region != nil {
			return req, &apierror.ValidationError{Msg: "product, subscriptionId, planCode, previousPlanCode, billingPeriod, notifyCustomer, upgradeKind, salesforceAccountId and region are only accepted on an upgrade_organization request"}
		}
		return out, nil
	}
	if err := normalizeWSO2CloudUpgradeKind(req.UpgradeKind, sfAccountID, region, &out); err != nil {
		return req, err
	}
	if subscriptionID == nil {
		return req, &apierror.ValidationError{Msg: "subscriptionId is required for an upgrade_organization request"}
	}
	if planCode == nil {
		return req, &apierror.ValidationError{Msg: "planCode is required for an upgrade_organization request"}
	}
	for _, f := range []struct {
		name  string
		value *string
		max   int
	}{
		{"product", product, maxWSO2CloudProductLength},
		{"subscriptionId", subscriptionID, maxWSO2CloudSubscriptionIDLen},
		{"planCode", planCode, maxWSO2CloudPlanCodeLength},
		{"previousPlanCode", previousPlanCode, maxWSO2CloudPlanCodeLength},
	} {
		if f.value != nil && utf8.RuneCountInString(*f.value) > f.max {
			return req, &apierror.ValidationError{Msg: fmt.Sprintf("%s must be at most %d characters", f.name, f.max)}
		}
	}
	if previousPlanCode != nil && *previousPlanCode == *planCode {
		return req, &apierror.ValidationError{Msg: "planCode is the plan the subscription is already on"}
	}
	if req.BillingPeriod != nil && !validWSO2CloudBillingPeriod[*req.BillingPeriod] {
		return req, apierror.InvalidValue("billingPeriod", string(*req.BillingPeriod), "billing period",
			[]string{string(domain.WSO2CloudBillingMonthly), string(domain.WSO2CloudBillingAnnual)})
	}
	out.Product, out.SubscriptionID, out.PlanCode, out.PreviousPlanCode = product, subscriptionID, planCode, previousPlanCode
	out.BillingPeriod, out.NotifyCustomer = req.BillingPeriod, req.NotifyCustomer
	return out, nil
}

func wso2CloudRequestTypeNames() []string {
	return []string{
		string(domain.WSO2CloudRequestUpgradeOrganization), string(domain.WSO2CloudRequestDeleteOrganization),
		string(domain.WSO2CloudRequestLockUser), string(domain.WSO2CloudRequestUnlockUser),
		string(domain.WSO2CloudRequestDisableUser), string(domain.WSO2CloudRequestEnableUser),
		string(domain.WSO2CloudRequestDeleteUser),
	}
}
