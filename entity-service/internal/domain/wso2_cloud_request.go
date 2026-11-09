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
	"encoding/json"
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

// WSO2CloudTier is a WSO2 Cloud organization's subscription tier.
type WSO2CloudTier string

const (
	WSO2CloudTierFree       WSO2CloudTier = "free"
	WSO2CloudTierPayg       WSO2CloudTier = "payg"
	WSO2CloudTierEnterprise WSO2CloudTier = "enterprise"
)

// WSO2CloudRequest is one request raised from the CSM portal's WSO2 Cloud tab (migration 0278).
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
	// Tier and Limits are what an upgrade asks for; nil for every other type.
	Tier   *WSO2CloudTier  `json:"tier"`
	Limits json.RawMessage `json:"limits"`
	// PreviousTier and PreviousLimits are what the organization had when the upgrade was raised.
	PreviousTier   *WSO2CloudTier         `json:"previousTier"`
	PreviousLimits json.RawMessage        `json:"previousLimits"`
	Status         WSO2CloudRequestStatus `json:"status"`
	RequestedBy    string                 `json:"requestedBy"`
	RequestedOn    time.Time              `json:"requestedOn"`
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
	Tier            *WSO2CloudTier       `json:"tier,omitempty"`
	Limits          json.RawMessage      `json:"limits,omitempty"`
	PreviousTier    *WSO2CloudTier       `json:"previousTier,omitempty"`
	PreviousLimits  json.RawMessage      `json:"previousLimits,omitempty"`
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
