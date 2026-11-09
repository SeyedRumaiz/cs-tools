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
	"bytes"
	"context"
	"encoding/json"
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

var validWSO2CloudTier = map[domain.WSO2CloudTier]bool{
	domain.WSO2CloudTierFree:       true,
	domain.WSO2CloudTierPayg:       true,
	domain.WSO2CloudTierEnterprise: true,
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
}

// NewWSO2CloudRequestService constructs a WSO2CloudRequestService.
func NewWSO2CloudRequestService(repo repository.WSO2CloudRequestRepository, access AccessService) WSO2CloudRequestService {
	return &wso2CloudRequestService{repo: repo, access: access}
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
	// requested_by never changes, so this check needs no lock.
	if strings.EqualFold(current.RequestedBy, resolvedBy) {
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

// normalizeWSO2CloudRequest trims every text field, turns blank optional text into nil, and checks the request is
// complete for its type: an upgrade carries a tier (and may carry limits and what the organization had before), no
// other type carries any of those, and lock or disable needs a reason.
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

	if req.Type != domain.WSO2CloudRequestUpgradeOrganization {
		if req.Tier != nil || len(req.Limits) > 0 || req.PreviousTier != nil || len(req.PreviousLimits) > 0 {
			return req, &apierror.ValidationError{Msg: "tier, limits, previousTier and previousLimits are only accepted on an upgrade_organization request"}
		}
		return out, nil
	}
	if req.Tier == nil {
		return req, &apierror.ValidationError{Msg: "tier is required for an upgrade_organization request"}
	}
	if !validWSO2CloudTier[*req.Tier] {
		return req, apierror.InvalidValue("tier", string(*req.Tier), "WSO2 Cloud tier", wso2CloudTierNames())
	}
	if req.PreviousTier != nil && !validWSO2CloudTier[*req.PreviousTier] {
		return req, apierror.InvalidValue("previousTier", string(*req.PreviousTier), "WSO2 Cloud tier", wso2CloudTierNames())
	}
	limits, err := jsonObjectOrNil("limits", req.Limits)
	if err != nil {
		return req, err
	}
	previousLimits, err := jsonObjectOrNil("previousLimits", req.PreviousLimits)
	if err != nil {
		return req, err
	}
	out.Tier, out.Limits, out.PreviousTier, out.PreviousLimits = req.Tier, limits, req.PreviousTier, previousLimits
	return out, nil
}

// jsonObjectOrNil accepts a JSON object; absent or null is nil, anything else (an array, a string, a number) is a
// ValidationError.
func jsonObjectOrNil(field string, raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if trimmed[0] != '{' || json.Unmarshal(trimmed, &obj) != nil {
		return nil, &apierror.ValidationError{Msg: field + " must be a JSON object"}
	}
	return json.RawMessage(trimmed), nil
}

func wso2CloudRequestTypeNames() []string {
	return []string{
		string(domain.WSO2CloudRequestUpgradeOrganization), string(domain.WSO2CloudRequestDeleteOrganization),
		string(domain.WSO2CloudRequestLockUser), string(domain.WSO2CloudRequestUnlockUser),
		string(domain.WSO2CloudRequestDisableUser), string(domain.WSO2CloudRequestEnableUser),
		string(domain.WSO2CloudRequestDeleteUser),
	}
}

func wso2CloudTierNames() []string {
	return []string{string(domain.WSO2CloudTierFree), string(domain.WSO2CloudTierPayg), string(domain.WSO2CloudTierEnterprise)}
}
