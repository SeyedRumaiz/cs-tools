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

package salesentity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
)

const (
	organizationSearchPath = "/organizations/search"
	// correlationIDHeader names, on a link, the CSM request that asked for it,
	// so Sales Entity's audit log can say which approved request it was.
	correlationIDHeader = "correlationId"
)

// Organization is a Salesforce Organization__c record: one WSO2 Cloud org,
// under the customer's Account and optionally linked to one support Project.
type Organization struct {
	ID string `json:"id"`
	// OrganizationID is the WSO2 Cloud org's UUID, lower-case.
	OrganizationID *string               `json:"organizationId"`
	Handle         *string               `json:"handle"`
	Region         *string               `json:"region"`
	CustomerID     *string               `json:"customerId"`
	Customer       *OrganizationCustomer `json:"customer"`
	ProjectID      *string               `json:"projectId"`
	Project        *OrganizationProject  `json:"project"`
}

// OrganizationCustomer is the Account an Organization__c is under.
type OrganizationCustomer struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// OrganizationProject is the Project__c an Organization__c is linked to.
type OrganizationProject struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type organizationSearchRequest struct {
	OrganizationID string `json:"organizationId"`
	Limit          int    `json:"limit"`
}

// LinkOrganizationInput is a PUT /organizations/{organizationId}/link: the org
// UUID goes in the path, the rest in the body. AllowMove is never sent: a
// record under another account is refused, not moved.
type LinkOrganizationInput struct {
	OrganizationID string `json:"-"`
	Handle         string `json:"handle"`
	Region         string `json:"region"`
	CustomerID     string `json:"customerId"`
}

// LinkRefusedError is Sales Entity refusing a link (a 4xx). Code is its stable
// reason (ORG_OTHER_ACCOUNT, ACCOUNT_NOT_FOUND, ...), empty when the body has
// none; Status is the HTTP status.
type LinkRefusedError struct {
	Status int
	Code   string
}

func (e *LinkRefusedError) Error() string {
	return fmt.Sprintf("salesentity: organization link refused (status %d, code %q)", e.Status, e.Code)
}

// FindOrganization returns the Organization__c record of a WSO2 Cloud org, by
// its UUID; false when there is none.
func (c *Client) FindOrganization(ctx context.Context, organizationID string) (Organization, bool, error) {
	var rows []Organization
	body := organizationSearchRequest{OrganizationID: strings.ToLower(organizationID), Limit: 1}
	if err := c.searchWithRetry(ctx, organizationSearchPath, body, "organization", &rows); err != nil {
		return Organization{}, false, err
	}
	for _, org := range rows {
		if org.OrganizationID != nil && strings.EqualFold(*org.OrganizationID, organizationID) {
			return org, true, nil
		}
	}
	return Organization{}, false, nil
}

// FindCustomer returns the Account with that Salesforce Id; false when there
// is none. Unlike GetCustomer, an empty result is an answer, not a retry: the
// id was typed by a person, not delivered by a Salesforce event.
func (c *Client) FindCustomer(ctx context.Context, id string) (Customer, bool, error) {
	var rows []Customer
	body := customerSearchRequest{IDs: []string{id}, IsRealTime: true, Limit: 1}
	if err := c.searchWithRetry(ctx, customerSearchPath, body, "customer", &rows); err != nil {
		return Customer{}, false, err
	}
	cust, ok := matchingCustomer(rows, id)
	return cust, ok, nil
}

// ActiveOpportunities lists an account's active opportunities, as Sales
// Entity's isOnlyActiveOpportunities defines them (won and not yet ended, or
// with allocations ready).
func (c *Client) ActiveOpportunities(ctx context.Context, accountID string, limit int) ([]Opportunity, error) {
	active := true
	return c.SearchOpportunities(ctx, SearchFilter{CustomerID: accountID, IsOnlyActiveOpportunities: &active, Limit: limit})
}

// LinkOrganization creates or updates the org's Organization__c record under
// the account (PUT /organizations/{organizationId}/link). It is safe to repeat. correlationID
// names the CSM request that asked for it. A 4xx is a *LinkRefusedError
// carrying Sales Entity's code; the token is refreshed once on a 401.
func (c *Client) LinkOrganization(ctx context.Context, in LinkOrganizationInput, correlationID string) (Organization, error) {
	out, err := c.linkOrganization(ctx, in, correlationID)
	if errors.Is(err, errCustomerUnauthorized) {
		c.invalidateToken()
		out, err = c.linkOrganization(ctx, in, correlationID)
		if errors.Is(err, errCustomerUnauthorized) {
			return Organization{}, &apierror.UnauthorizedError{Msg: "salesentity: organization link unauthorized"}
		}
	}
	return out, err
}

func (c *Client) linkOrganization(ctx context.Context, in LinkOrganizationInput, correlationID string) (Organization, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return Organization{}, err
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return Organization{}, fmt.Errorf("salesentity: marshal organization link request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+organizationLinkPath(in.OrganizationID), bytes.NewReader(payload))
	if err != nil {
		return Organization{}, fmt.Errorf("salesentity: build organization link request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if correlationID != "" {
		req.Header.Set(correlationIDHeader, correlationID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return Organization{}, err
		}
		return Organization{}, &apierror.ServiceUnavailableError{Msg: fmt.Sprintf("salesentity: organization link request: %v", err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Organization{}, fmt.Errorf("salesentity: read organization link response: %w", err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		var org Organization
		if err := json.Unmarshal(raw, &org); err != nil {
			return Organization{}, fmt.Errorf("salesentity: parse organization link response: %w", err)
		}
		return org, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return Organization{}, errCustomerUnauthorized
	case resp.StatusCode >= 500:
		return Organization{}, &apierror.ServiceUnavailableError{Msg: fmt.Sprintf("salesentity: organization link returned %d", resp.StatusCode)}
	default:
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &body)
		slog.Warn("salesentity refused the organization link", "status", resp.StatusCode, "code", body.Code)
		return Organization{}, &LinkRefusedError{Status: resp.StatusCode, Code: body.Code}
	}
}

// organizationLinkPath is the route that creates or updates the Organization__c
// record of a WSO2 Cloud org and links it to an account. UUIDs are stored
// lower-case, so the path carries the lower-cased one.
func organizationLinkPath(organizationID string) string {
	return "/organizations/" + url.PathEscape(strings.ToLower(organizationID)) + "/link"
}
