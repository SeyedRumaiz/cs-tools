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

package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OnboardingStepSearchMaxLimit is the largest page size the entity service
// accepts on POST /onboarding-steps/search. A larger limit is rejected
// upstream, so callers paging through a project's ledger use this value.
const OnboardingStepSearchMaxLimit = 50

// OnboardingStep is one row of the entity service's onboarding status ledger
// (onboarding_step): the latest recorded outcome of one step (IDENTITY,
// DATABASE, EMAIL, REGISTRATION) for one Salesforce Project_Contact__c
// membership. Field names mirror the entity service's own JSON contract.
type OnboardingStep struct {
	ID               string    `json:"id"`
	MembershipSfID   string    `json:"membershipSfId"`
	ContactSfID      *string   `json:"contactSfId"`
	Email            string    `json:"email"`
	ProjectID        *string   `json:"projectId"`
	ProjectContactID *string   `json:"projectContactId"`
	Step             string    `json:"step"`
	Status           string    `json:"status"`
	AttemptCount     int       `json:"attemptCount"`
	LastError        *string   `json:"lastError"`
	EventType        string    `json:"eventType"`
	EventModifiedOn  time.Time `json:"eventModifiedOn"`
	CreatedOn        time.Time `json:"createdOn"`
	UpdatedOn        time.Time `json:"updatedOn"`
}

// OnboardingStepFilters narrows POST /onboarding-steps/search. Every field
// is optional; an empty filter set matches the whole ledger.
type OnboardingStepFilters struct {
	ProjectID       *string  `json:"projectId,omitempty"`
	MembershipSfIDs []string `json:"membershipSfIds,omitempty"`
	Statuses        []string `json:"statuses,omitempty"`
}

// OnboardingStepPagination is the offset/limit pair the search accepts.
type OnboardingStepPagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// OnboardingStepSearchRequest is the body of POST /onboarding-steps/search.
type OnboardingStepSearchRequest struct {
	Filters    OnboardingStepFilters    `json:"filters"`
	Pagination OnboardingStepPagination `json:"pagination"`
}

// OnboardingStepSearchResponse is one page of POST /onboarding-steps/search,
// newest first. There is no hasMore flag: offset+len(steps) < total is the
// continuation check.
type OnboardingStepSearchResponse struct {
	Steps  []OnboardingStep `json:"steps"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// SearchOnboardingSteps calls POST /onboarding-steps/search on the entity
// service and decodes the page it returns. Unlike most CustomerEntityClient
// methods this one is typed rather than a raw passthrough, because the
// handler regroups the rows per membership before answering the portal.
func (c *CustomerEntityClient) SearchOnboardingSteps(ctx context.Context, req OnboardingStepSearchRequest) (OnboardingStepSearchResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return OnboardingStepSearchResponse{}, fmt.Errorf("entity: encode onboarding step search request: %w", err)
	}

	raw, err := c.do(ctx, http.MethodPost, "/onboarding-steps/search", body)
	if err != nil {
		return OnboardingStepSearchResponse{}, err
	}

	var resp OnboardingStepSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OnboardingStepSearchResponse{}, fmt.Errorf("entity: decode onboarding step search response: %w", err)
	}
	return resp, nil
}
