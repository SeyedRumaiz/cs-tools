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

package servicenow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

// GoLiveStatus is a project's go-live status badge.
type GoLiveStatus struct {
	Status string `json:"status"`
	IsRisk bool   `json:"isRisk"`
	State  string `json:"state"`
}

// CaseInfo is a minimal case reference used inside health-detail dropdowns.
type CaseInfo struct {
	SysID  string `json:"sysId"`
	Number string `json:"number"`
}

// CaseGroup groups cases by priority for the health-detail dropdowns.
type CaseGroup struct {
	Priority string     `json:"priority"`
	Count    int        `json:"count"`
	Cases    []CaseInfo `json:"cases,omitempty"`
}

// Deployment is a minimal deployment reference used inside health-detail
// dropdowns.
type Deployment struct {
	SysID string `json:"sysId"`
	Name  string `json:"name"`
}

// EolProduct is an end-of-life product and the deployments using it.
type EolProduct struct {
	Name        string       `json:"name"`
	EolDate     string       `json:"eolDate"`
	Deployments []Deployment `json:"deployments"`
}

// CaseLink is a case/escalation reference used inside health-detail
// dropdowns.
type CaseLink struct {
	SysID            string `json:"sysId,omitempty"`
	Number           string `json:"number,omitempty"`
	EscalationSysID  string `json:"escalationSysId,omitempty"`
	EscalationNumber string `json:"escalationNumber,omitempty"`
}

// ProjectDetail is a single project's detailed risk breakdown within an
// account's customer-health detail view.
type ProjectDetail struct {
	SysID                   string       `json:"sysId"`
	Name                    string       `json:"name"`
	HasRecentCases          bool         `json:"hasRecentCases"`
	DetailedRecentCases     []CaseGroup  `json:"detailedRecentCases"`
	TotalRecentCases        int          `json:"totalRecentCases"`
	HasAbandonedCases       bool         `json:"hasAbandonedCases"`
	DetailedAbandonedCases  []CaseLink   `json:"detailedAbandonedCases"`
	IsUsingEolProduct       bool         `json:"isUsingEolProduct"`
	SoftwareModel           []EolProduct `json:"softwareModel"`
	Deployments             []Deployment `json:"deployments"`
	HasMigrationDelays      bool         `json:"hasMigrationDelays"`
	DetailedMigrationDelays []CaseLink   `json:"detailedMigrationDelays"`
	HasEscalatedCases       bool         `json:"hasEscalatedCases"`
	DetailedEscalatedCases  []CaseLink   `json:"detailedEscalatedCases"`
	GoLiveStatus            GoLiveStatus `json:"goLiveStatus"`
}

// AccountDetail is the full customer-health detail view for one account.
type AccountDetail struct {
	AccountName      string          `json:"accountName"`
	CustomerProjects []ProjectDetail `json:"customerProjects"`
}

type serviceNowDetailResponse struct {
	Result AccountDetail `json:"result"`
}

// AccountSummary is one account's row in the customer-health summary list.
type AccountSummary struct {
	AccountSysID           string       `json:"accountSysId"`
	AccountName            *string      `json:"accountName"`
	HasNoGoLive            GoLiveStatus `json:"hasNoGoLive"`
	HasRecentCases         bool         `json:"hasRecentCases"`
	HasEolProduct          bool         `json:"hasEolProduct"`
	HasAbandonedMigrations bool         `json:"hasAbandonedMigrations"`
	HasMigrationDelays     bool         `json:"hasMigrationDelays"`
	HasRecentEscalations   bool         `json:"hasRecentEscalations"`
	NoSupportCases6mo      bool         `json:"noSupportCases6mo"`
	HealthStatus           *string      `json:"healthStatus"`
}

// AccountSummaryResponse is a page of the customer-health summary list.
type AccountSummaryResponse struct {
	Data       []AccountSummary `json:"data"`
	TotalCount int              `json:"totalCount"`
}

type serviceNowSummaryWrapper struct {
	Result AccountSummaryResponse `json:"result"`
}

// GetCustomerHealthSummary retrieves a page of the customer-health account
// summary list from SupportPortalLite's custom scoped-app API, filtered by
// the given (all optional) criteria. region may contain multiple values;
// each is sent as a repeated "region" query parameter, matching the
// Ballerina source's handling of ServiceNow's comma-join-avoidance
// requirement for this one parameter.
//
// Every parameter here is placed into the request via url.Values.Encode(),
// which percent-encodes reserved characters (&, =, +, space, etc.)
// correctly — unlike the Ballerina source, which built this URL by plain
// string interpolation (with a hand-rolled "+"→"%2B" fix-up specifically
// for region) and so could have query parameters corrupted, or in principle
// smuggle extra parameters, if a caller-supplied value contained "&" or
// "=". This is a deliberate hardening in the port, not a functional change
// to the endpoint being called.
func (c *Client) GetCustomerHealthSummary(ctx context.Context, email, phrase, risks *string, region []string, product, abtTeam *string, offset, limit int) (*AccountSummaryResponse, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	params.Set("email", derefOrEmpty(email))
	params.Set("phrase", derefOrEmpty(phrase))
	params.Set("risks", derefOrEmpty(risks))
	params.Set("product", derefOrEmpty(product))
	params.Set("abt_team", derefOrEmpty(abtTeam))
	for _, r := range region {
		params.Add("region", r)
	}

	raw, err := c.CustomGet(ctx, "/api/wso2/customerhealthanalysis/accounts/summary", params)
	if err != nil {
		return nil, err
	}

	var wrapper serviceNowSummaryWrapper
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("servicenow: decode customer health summary response: %w", err)
	}
	return &wrapper.Result, nil
}

// GetCustomerHealthDetail retrieves the detailed customer-health breakdown
// for a single account by its ServiceNow sys_id. Returns ErrAccountNotFound
// when ServiceNow has no matching customer_account record.
func (c *Client) GetCustomerHealthDetail(ctx context.Context, accountID string) (*AccountDetail, error) {
	raw, err := c.CustomGet(ctx, "/api/wso2/customerhealthanalysis/account/"+url.PathEscape(accountID), nil)
	if err != nil {
		var apiErr *apierror.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil, ErrAccountNotFound
		}
		return nil, err
	}

	var wrapper serviceNowDetailResponse
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("servicenow: decode customer health detail response: %w", err)
	}
	return &wrapper.Result, nil
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
