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

package domain

import (
	"time"
)

// WSO2CloudRequestType is what a WSO2 Cloud request asks for.
type WSO2CloudRequestType string

const (
	WSO2CloudRequestUpgradeOrganization WSO2CloudRequestType = "upgrade_organization"
	WSO2CloudRequestDeleteOrganization  WSO2CloudRequestType = "delete_organization"
	WSO2CloudRequestLockUser            WSO2CloudRequestType = "lock_user"
	WSO2CloudRequestUnlockUser          WSO2CloudRequestType = "unlock_user"
	WSO2CloudRequestDisableUser         WSO2CloudRequestType = "disable_user"
	WSO2CloudRequestEnableUser          WSO2CloudRequestType = "enable_user"
	WSO2CloudRequestDeleteUser          WSO2CloudRequestType = "delete_user"
)

// WSO2CloudRequestStatus is where a WSO2 Cloud request stands. A cancelled request is deleted, so there is no
// cancelled status.
type WSO2CloudRequestStatus string

const (
	WSO2CloudRequestPending  WSO2CloudRequestStatus = "pending"
	WSO2CloudRequestApproved WSO2CloudRequestStatus = "approved"
	WSO2CloudRequestRejected WSO2CloudRequestStatus = "rejected"
)

// WSO2CloudBillingPeriod is how often an upgraded subscription is invoiced (WSO2 Cloud's billing_period).
type WSO2CloudBillingPeriod string

const (
	WSO2CloudBillingMonthly WSO2CloudBillingPeriod = "monthly"
	WSO2CloudBillingAnnual  WSO2CloudBillingPeriod = "annual"
)

// WSO2CloudUpgradeKind says whether an upgrade is for a paying customer or internal. A customer upgrade names the
// customer's Salesforce Account, which must have an active opportunity; an internal one never touches Salesforce.
type WSO2CloudUpgradeKind string

const (
	WSO2CloudUpgradeCustomer WSO2CloudUpgradeKind = "customer"
	WSO2CloudUpgradeInternal WSO2CloudUpgradeKind = "internal"
)

// WSO2CloudSalesforceLinkStatus is the outcome of linking an approved customer upgrade's org to its Salesforce
// account. A request whose link has not been tried has none.
type WSO2CloudSalesforceLinkStatus string

const (
	WSO2CloudSalesforceLinked WSO2CloudSalesforceLinkStatus = "linked"
	WSO2CloudSalesforceFailed WSO2CloudSalesforceLinkStatus = "failed"
)

// WSO2CloudRequest is one request raised from the CSM portal's WSO2 Cloud tab (migration 0287).
type WSO2CloudRequest struct {
	ID string `json:"id"`
	// Number is the request's own number (WCR0000001), given when it is raised and never reused.
	Number string               `json:"number"`
	Type   WSO2CloudRequestType `json:"type"`
	// TargetID is the organization's UUID or the user's SCIM id in WSO2 Cloud.
	TargetID string `json:"targetId"`
	// TargetLabel is the organization's handle or the user's email, as it was when the request was raised.
	TargetLabel string `json:"targetLabel"`
	// Reason is the requester's reason; always present for lock_user and disable_user.
	Reason *string `json:"reason"`
	// CustomerConsent is the subject of the customer's consent email, when one was given (deletions).
	CustomerConsent *string `json:"customerConsent"`
	// The upgrade fields are nil (NotifyCustomer false) for every other type. An upgrade moves one subscription (one
	// product of the organization) to an Enterprise plan billed by wire transfer.
	Product        *string `json:"product"`
	SubscriptionID *string `json:"subscriptionId"`
	PlanCode       *string `json:"planCode"`
	// PreviousPlanCode is the plan the subscription had when the upgrade was raised.
	PreviousPlanCode *string                 `json:"previousPlanCode"`
	BillingPeriod    *WSO2CloudBillingPeriod `json:"billingPeriod"`
	NotifyCustomer   bool                    `json:"notifyCustomer"`
	// BillingAccountID is the wire billing account created in WSO2 Cloud while the upgrade was being approved.
	BillingAccountID *string `json:"billingAccountId"`
	// UpgradeKind is set on upgrades only; nil on an upgrade raised before it existed (treated as internal).
	UpgradeKind *WSO2CloudUpgradeKind `json:"upgradeKind"`
	// SalesforceAccountID and Region are set on a customer upgrade only: the customer's Account (18 characters) and
	// the WSO2 Cloud region the org is in.
	SalesforceAccountID *string `json:"salesforceAccountId"`
	Region              *string `json:"region"`
	// The Salesforce link of an approved customer upgrade: nil until tried, then linked or failed (with the reason).
	SalesforceLinkStatus *WSO2CloudSalesforceLinkStatus `json:"salesforceLinkStatus"`
	SalesforceLinkError  *string                        `json:"salesforceLinkError"`
	SalesforceLinkedOn   *time.Time                     `json:"salesforceLinkedOn"`
	Status               WSO2CloudRequestStatus         `json:"status"`
	RequestedBy          string                         `json:"requestedBy"`
	RequestedOn          time.Time                      `json:"requestedOn"`
	// ResolvedBy and ResolvedOn are nil while the request is pending.
	ResolvedBy *string    `json:"resolvedBy"`
	ResolvedOn *time.Time `json:"resolvedOn"`
	// Remark is the approver's comment; always present on a rejected request.
	Remark *string `json:"remark"`
}

// CreateWSO2CloudRequestRequest is the body of POST /wso2-cloud-requests. The requester is the caller, read from their
// token.
type CreateWSO2CloudRequestRequest struct {
	Type            WSO2CloudRequestType `json:"type"`
	TargetID        string               `json:"targetId"`
	TargetLabel     string               `json:"targetLabel"`
	Reason          *string              `json:"reason,omitempty"`
	CustomerConsent *string              `json:"customerConsent,omitempty"`
	// Upgrade only: subscriptionId and planCode are required, the rest optional.
	Product          *string                 `json:"product,omitempty"`
	SubscriptionID   *string                 `json:"subscriptionId,omitempty"`
	PlanCode         *string                 `json:"planCode,omitempty"`
	PreviousPlanCode *string                 `json:"previousPlanCode,omitempty"`
	BillingPeriod    *WSO2CloudBillingPeriod `json:"billingPeriod,omitempty"`
	NotifyCustomer   bool                    `json:"notifyCustomer,omitempty"`
	// Upgrade only, required: customer or internal. A customer upgrade also needs salesforceAccountId (an Account with
	// an active opportunity) and region; an internal one takes neither.
	UpgradeKind         *WSO2CloudUpgradeKind `json:"upgradeKind,omitempty"`
	SalesforceAccountID *string               `json:"salesforceAccountId,omitempty"`
	Region              *string               `json:"region,omitempty"`
}

// SetWSO2CloudBillingAccountRequest is the body of POST /wso2-cloud-requests/{id}/billing-account: the wire billing
// account the CSM backend created in WSO2 Cloud while approving an upgrade.
type SetWSO2CloudBillingAccountRequest struct {
	BillingAccountID string `json:"billingAccountId"`
}

// ResolveWSO2CloudRequestRequest is the body of POST /wso2-cloud-requests/{id}/resolve. The approver is the caller,
// read from their token.
type ResolveWSO2CloudRequestRequest struct {
	// Status is approved or rejected.
	Status WSO2CloudRequestStatus `json:"status"`
	// Remark is required to reject.
	Remark *string `json:"remark,omitempty"`
}

// SearchWSO2CloudRequestsFilters narrows POST /wso2-cloud-requests/search; every field is optional and they are ANDed.
type SearchWSO2CloudRequestsFilters struct {
	Statuses    []WSO2CloudRequestStatus `json:"statuses,omitempty"`
	Types       []WSO2CloudRequestType   `json:"types,omitempty"`
	TargetID    *string                  `json:"targetId,omitempty"`
	RequestedBy *string                  `json:"requestedBy,omitempty"`
}

// SearchWSO2CloudRequestsRequest is the body of POST /wso2-cloud-requests/search.
type SearchWSO2CloudRequestsRequest struct {
	Filters    SearchWSO2CloudRequestsFilters `json:"filters"`
	Pagination Pagination                     `json:"pagination"`
}

// SearchWSO2CloudRequestsResponse is a page of requests, newest first.
type SearchWSO2CloudRequestsResponse struct {
	Requests []WSO2CloudRequest `json:"requests"`
	Total    int                `json:"total"`
	Limit    int                `json:"limit"`
	Offset   int                `json:"offset"`
}

// WSO2CloudSalesforceOpportunity is one active opportunity of a customer's Salesforce Account.
type WSO2CloudSalesforceOpportunity struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
	// EndDate is when the support it sold ends (YYYY-MM-DD), when Salesforce has one.
	EndDate *string `json:"endDate"`
}

// WSO2CloudSalesforceAccount is a customer's Salesforce Account and whether it has an active subscription (an active
// opportunity), which a customer upgrade needs.
type WSO2CloudSalesforceAccount struct {
	// ID is the Account Id, 18 characters.
	ID                    string                           `json:"id"`
	Name                  *string                          `json:"name"`
	HasActiveSubscription bool                             `json:"hasActiveSubscription"`
	ActiveOpportunities   []WSO2CloudSalesforceOpportunity `json:"activeOpportunities"`
}

// WSO2CloudSalesforceRecord is the Organization__c record of a WSO2 Cloud org.
type WSO2CloudSalesforceRecord struct {
	ID        string  `json:"id"`
	Handle    *string `json:"handle"`
	Region    *string `json:"region"`
	ProjectID *string `json:"projectId"`
}

// WSO2CloudSalesforceStatus is what Salesforce knows about a WSO2 Cloud org (GET
// /wso2-cloud/organizations/{orgId}/salesforce): its Organization__c record and the account it is under, both nil
// when the org has no record yet.
type WSO2CloudSalesforceStatus struct {
	Record  *WSO2CloudSalesforceRecord  `json:"record"`
	Account *WSO2CloudSalesforceAccount `json:"account"`
}
