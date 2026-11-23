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
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/salesentity"
)

// WSO2CloudSalesforce is the part of the REST sales-entity client a WSO2 Cloud upgrade uses. Satisfied by
// *salesentity.Client.
type WSO2CloudSalesforce interface {
	FindOrganization(ctx context.Context, organizationID string) (salesentity.Organization, bool, error)
	FindCustomer(ctx context.Context, id string) (salesentity.Customer, bool, error)
	ActiveOpportunities(ctx context.Context, accountID string, limit int) ([]salesentity.Opportunity, error)
	LinkOrganization(ctx context.Context, in salesentity.LinkOrganizationInput, correlationID string) (salesentity.Organization, error)
}

// wso2CloudActiveOpportunityLimit caps the active opportunities listed for an account: one is enough to allow an
// upgrade, a few let the person see which.
const wso2CloudActiveOpportunityLimit = 10

// wso2CloudRegions are the WSO2 Cloud regions an org can be in: the values of Organization__c's region picklist.
var wso2CloudRegions = []string{"us", "eu"}

// salesforceAccountIDRe is an Account Id: the 001 key prefix, 15 or 18 characters.
var salesforceAccountIDRe = regexp.MustCompile(`^001[A-Za-z0-9]{12}(?:[A-Za-z0-9]{3})?$`)

const msgSalesforceNotConnected = "Salesforce is not connected to this service, so a customer upgrade cannot be checked or linked"

// normalizeWSO2CloudUpgradeKind checks an upgrade's kind and the Salesforce fields that go with it, and sets them on
// out: a customer upgrade names an Account Id and a region, an internal one neither.
func normalizeWSO2CloudUpgradeKind(kind *domain.WSO2CloudUpgradeKind, sfAccountID, region *string, out *domain.CreateWSO2CloudRequestRequest) error {
	if kind == nil {
		return &apierror.ValidationError{Msg: "upgradeKind is required for an upgrade_organization request (customer or internal)"}
	}
	switch *kind {
	case domain.WSO2CloudUpgradeInternal:
		if sfAccountID != nil || region != nil {
			return &apierror.ValidationError{Msg: "salesforceAccountId and region are only accepted on a customer upgrade"}
		}
	case domain.WSO2CloudUpgradeCustomer:
		if sfAccountID == nil {
			return &apierror.ValidationError{Msg: "salesforceAccountId is required for a customer upgrade"}
		}
		if !salesforceAccountIDRe.MatchString(*sfAccountID) {
			return &apierror.ValidationError{Msg: "salesforceAccountId must be a Salesforce Account Id (15 or 18 characters, starting with 001)"}
		}
		if region == nil {
			return &apierror.ValidationError{Msg: "region is required for a customer upgrade"}
		}
		r := strings.ToLower(*region)
		if !slices.Contains(wso2CloudRegions, r) {
			return apierror.InvalidValue("region", *region, "WSO2 Cloud region", wso2CloudRegions)
		}
		region = &r
	default:
		return apierror.InvalidValue("upgradeKind", string(*kind), "upgrade kind",
			[]string{string(domain.WSO2CloudUpgradeCustomer), string(domain.WSO2CloudUpgradeInternal)})
	}
	out.UpgradeKind, out.SalesforceAccountID, out.Region = kind, sfAccountID, region
	return nil
}

func (s *wso2CloudRequestService) salesforceClient() (WSO2CloudSalesforce, error) {
	if s.salesforce == nil {
		return nil, &apierror.ServiceUnavailableError{Msg: msgSalesforceNotConnected}
	}
	return s.salesforce, nil
}

// salesforceAccount reads an Account and its active opportunities; false when there is no Account with that id.
func (s *wso2CloudRequestService) salesforceAccount(ctx context.Context, accountID string) (domain.WSO2CloudSalesforceAccount, bool, error) {
	sf, err := s.salesforceClient()
	if err != nil {
		return domain.WSO2CloudSalesforceAccount{}, false, err
	}
	cust, ok, err := sf.FindCustomer(ctx, accountID)
	if err != nil || !ok {
		return domain.WSO2CloudSalesforceAccount{}, false, err
	}
	opps, err := sf.ActiveOpportunities(ctx, cust.ID, wso2CloudActiveOpportunityLimit)
	if err != nil {
		return domain.WSO2CloudSalesforceAccount{}, false, err
	}
	out := domain.WSO2CloudSalesforceAccount{
		ID: cust.ID, Name: cust.Name, HasActiveSubscription: len(opps) > 0,
		ActiveOpportunities: make([]domain.WSO2CloudSalesforceOpportunity, 0, len(opps)),
	}
	for _, o := range opps {
		out.ActiveOpportunities = append(out.ActiveOpportunities, domain.WSO2CloudSalesforceOpportunity{
			ID: o.ID, Name: o.Name, EndDate: o.SupportAccountEndDateRollUp,
		})
	}
	return out, true, nil
}

// customerAccount is the Account a customer upgrade names, which must exist and have an active subscription.
func (s *wso2CloudRequestService) customerAccount(ctx context.Context, accountID string) (domain.WSO2CloudSalesforceAccount, error) {
	account, ok, err := s.salesforceAccount(ctx, accountID)
	if err != nil {
		return account, err
	}
	if !ok {
		return account, &apierror.ValidationError{Msg: "salesforceAccountId: Salesforce has no account with this id"}
	}
	if !account.HasActiveSubscription {
		return account, &apierror.ConflictError{
			Msg:  "this Salesforce account has no active subscription (an active opportunity), which a customer upgrade needs",
			Code: apierror.CodeWSO2CloudNoSubscription,
		}
	}
	return account, nil
}

// SalesforceOrganization says what Salesforce knows about a WSO2 Cloud org: its Organization__c record and the
// account it is under (with that account's subscription), or neither when it has no record yet.
func (s *wso2CloudRequestService) SalesforceOrganization(ctx context.Context, orgID string) (domain.WSO2CloudSalesforceStatus, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudSalesforceStatus{}, err
	}
	if err := validateUUIDs("orgId", []string{orgID}); err != nil {
		return domain.WSO2CloudSalesforceStatus{}, err
	}
	sf, err := s.salesforceClient()
	if err != nil {
		return domain.WSO2CloudSalesforceStatus{}, err
	}
	rec, ok, err := sf.FindOrganization(ctx, strings.ToLower(orgID))
	if err != nil || !ok {
		return domain.WSO2CloudSalesforceStatus{}, err
	}
	out := domain.WSO2CloudSalesforceStatus{Record: &domain.WSO2CloudSalesforceRecord{
		ID: rec.ID, Handle: rec.Handle, Region: rec.Region, ProjectID: rec.ProjectID,
	}}
	if rec.CustomerID == nil || *rec.CustomerID == "" {
		return out, nil
	}
	account, found, err := s.salesforceAccount(ctx, *rec.CustomerID)
	if err != nil {
		return domain.WSO2CloudSalesforceStatus{}, err
	}
	if found {
		out.Account = &account
	}
	return out, nil
}

// SalesforceAccount reads a Salesforce Account and whether it has an active subscription, so the person raising a
// customer upgrade can check the id before submitting it. An unknown id is a NotFoundError.
func (s *wso2CloudRequestService) SalesforceAccount(ctx context.Context, accountID string) (domain.WSO2CloudSalesforceAccount, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudSalesforceAccount{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if !salesforceAccountIDRe.MatchString(accountID) {
		return domain.WSO2CloudSalesforceAccount{}, &apierror.ValidationError{Msg: "accountId must be a Salesforce Account Id (15 or 18 characters, starting with 001)"}
	}
	account, ok, err := s.salesforceAccount(ctx, accountID)
	if err != nil {
		return account, err
	}
	if !ok {
		return account, &apierror.NotFoundError{Msg: "Salesforce has no account with this id"}
	}
	return account, nil
}

// RequestSalesforceAccount reads, again, the Account a customer upgrade names, so the CSM backend can refuse to
// approve one whose subscription has lapsed since it was raised. Only a customer upgrade has one.
func (s *wso2CloudRequestService) RequestSalesforceAccount(ctx context.Context, id string) (domain.WSO2CloudSalesforceAccount, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudSalesforceAccount{}, err
	}
	r, err := s.customerUpgrade(ctx, id)
	if err != nil {
		return domain.WSO2CloudSalesforceAccount{}, err
	}
	account, ok, err := s.salesforceAccount(ctx, *r.SalesforceAccountID)
	if err != nil {
		return account, err
	}
	if !ok {
		return account, &apierror.NotFoundError{Msg: "Salesforce no longer has the account this request names"}
	}
	return account, nil
}

// LinkSalesforce records an approved customer upgrade's org in Salesforce: its Organization__c record is created or
// updated under the request's account, with the org's handle and region (Sales Entity's link, safe to repeat). The
// outcome is stored on the request and returned with it; a refusal or an unreachable Salesforce is a failed link with
// the reason, not an error, so it can be retried. A request already linked is returned as it is.
func (s *wso2CloudRequestService) LinkSalesforce(ctx context.Context, id string) (domain.WSO2CloudRequest, error) {
	if err := RequireInternalCaller(ctx, s.access, wso2CloudRequestForbiddenMsg); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if _, err := wso2CloudCallerEmail(ctx); err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	r, err := s.customerUpgrade(ctx, id)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if r.Status != domain.WSO2CloudRequestApproved {
		return domain.WSO2CloudRequest{}, &apierror.ConflictError{Msg: "only an approved request is linked in Salesforce; this one is " + string(r.Status)}
	}
	if r.SalesforceLinkStatus != nil && *r.SalesforceLinkStatus == domain.WSO2CloudSalesforceLinked {
		return r, nil
	}
	sf, err := s.salesforceClient()
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	status, reason := domain.WSO2CloudSalesforceLinked, (*string)(nil)
	_, linkErr := sf.LinkOrganization(ctx, salesentity.LinkOrganizationInput{
		OrganizationID: strings.ToLower(r.TargetID),
		Handle:         r.TargetLabel,
		Region:         *r.Region,
		CustomerID:     *r.SalesforceAccountID,
	}, r.Number)
	if linkErr != nil {
		if errors.Is(linkErr, context.Canceled) {
			return domain.WSO2CloudRequest{}, linkErr
		}
		msg := salesforceLinkFailure(linkErr)
		status, reason = domain.WSO2CloudSalesforceFailed, &msg
		slog.WarnContext(ctx, "wso2 cloud request: salesforce link failed", "requestId", r.ID, "err", linkErr)
	}
	updated, err := s.repo.SetSalesforceLink(ctx, r.ID, status, reason)
	if err != nil {
		return domain.WSO2CloudRequest{}, err
	}
	if updated == nil {
		return domain.WSO2CloudRequest{}, &apierror.ConflictError{Msg: "this request is no longer an approved customer upgrade"}
	}
	return *updated, nil
}

// customerUpgrade reads a request that must be a customer upgrade.
func (s *wso2CloudRequestService) customerUpgrade(ctx context.Context, id string) (domain.WSO2CloudRequest, error) {
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
	if r.UpgradeKind == nil || *r.UpgradeKind != domain.WSO2CloudUpgradeCustomer || r.SalesforceAccountID == nil || r.Region == nil {
		return domain.WSO2CloudRequest{}, &apierror.ValidationError{Msg: "only a customer upgrade has a Salesforce account"}
	}
	return *r, nil
}

// salesforceLinkCodeRe is the shape of Sales Entity's refusal codes; anything else is not quoted.
var salesforceLinkCodeRe = regexp.MustCompile(`^[A-Z][A-Z_]{0,39}$`)

// salesforceLinkFailure is the reason a link failed, written for the person who will put it right.
func salesforceLinkFailure(err error) string {
	var refused *salesentity.LinkRefusedError
	var unavailable *apierror.ServiceUnavailableError
	var unauthorized *apierror.UnauthorizedError
	switch {
	case errors.As(err, &refused):
		switch refused.Code {
		case "ORG_OTHER_ACCOUNT":
			return "The organization's Salesforce record is under another account. Move it to this account in Salesforce, then retry."
		case "ACCOUNT_NOT_FOUND":
			return "Salesforce has no account with this request's account id."
		case "HANDLE_TAKEN":
			return "Another Salesforce organization record already has this handle."
		case "ORG_HAS_PROJECT", "PROJECT_HAS_ORG", "PROJECT_OTHER_ACCOUNT":
			return "The organization's Salesforce record is linked to a project that does not fit this account. Check it in Salesforce, then retry."
		case "INVALID_INPUT":
			return "Salesforce refused the organization's details (its id, handle or region)."
		}
		if salesforceLinkCodeRe.MatchString(refused.Code) {
			return "Salesforce refused the link (" + refused.Code + ")."
		}
		return "Salesforce refused the link."
	case errors.As(err, &unauthorized):
		return "This service is not allowed to write to Salesforce. Ask for its access to be checked, then retry."
	case errors.As(err, &unavailable), errors.Is(err, context.DeadlineExceeded):
		return "Salesforce could not be reached. Retry later."
	default:
		return "The link could not be completed. Retry later."
	}
}
