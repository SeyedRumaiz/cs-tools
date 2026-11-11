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

package wso2cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// DefaultBillingAudience is the billing resource server's audience.
const DefaultBillingAudience = "urn:wso2:cloud:billing"

// billingTimeout bounds one call to the billing admin API (token included).
const billingTimeout = 20 * time.Second

// maxErrorMessage caps how much of WSO2 Cloud's error text is kept.
const maxErrorMessage = 500

// BillingConfig is how to reach WSO2 Cloud's billing admin API
// (base path /admin/api/v1). WSO2 Cloud is still changing how this API is
// authorized; everything about the token is in this struct and
// NewBillingClient, so that change stays here.
type BillingConfig struct {
	// BaseURL ends at /admin/api/v1, e.g. https://<host>/admin/api/v1.
	BaseURL string
	// TokenURL, ClientID and ClientSecret are the client_credentials client
	// WSO2 Cloud issued for the CSM backend.
	TokenURL     string
	ClientID     string
	ClientSecret string
	// Scopes requested with the token; empty for the client's defaults.
	Scopes []string
	// Audience is sent as the token request's audience; empty for
	// DefaultBillingAudience.
	Audience string
	// HTTPClient makes the API calls; nil for a default one. Tests set it.
	HTTPClient *http.Client
}

// BillingClient calls WSO2 Cloud's billing admin API with a client_credentials
// token. Every call it makes is recorded in WSO2 Cloud's billing audit log.
type BillingClient struct {
	baseURL string
	http    *http.Client
}

// NewBillingClient checks cfg and returns a client. The token is fetched on the
// first call and refreshed when it expires.
func NewBillingClient(cfg BillingConfig) (*BillingClient, error) {
	var missing []string
	for name, v := range map[string]string{
		"WSO2_CLOUD_BILLING_BASE_URL": cfg.BaseURL, "WSO2_CLOUD_TOKEN_URL": cfg.TokenURL,
		"WSO2_CLOUD_CLIENT_ID": cfg.ClientID, "WSO2_CLOUD_CLIENT_SECRET": cfg.ClientSecret,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("WSO2_CLOUD_APPLY_MODE=%s needs %s", ModeBillingAPI, strings.Join(sortedStrings(missing), ", "))
	}
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, fmt.Errorf("WSO2_CLOUD_BILLING_BASE_URL must be an http(s) URL")
	}
	audience := cfg.Audience
	if audience == "" {
		audience = DefaultBillingAudience
	}
	plain := cfg.HTTPClient
	if plain == nil {
		plain = &http.Client{Timeout: billingTimeout}
	}
	cc := clientcredentials.Config{
		ClientID:       cfg.ClientID,
		ClientSecret:   cfg.ClientSecret,
		TokenURL:       cfg.TokenURL,
		Scopes:         cfg.Scopes,
		EndpointParams: url.Values{"audience": {audience}},
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, plain)
	authed := oauth2.NewClient(tokenCtx, cc.TokenSource(tokenCtx))
	authed.Timeout = billingTimeout
	return &BillingClient{baseURL: base.String(), http: authed}, nil
}

// APIError is a 4xx from WSO2 Cloud: it refused the call. Message is WSO2
// Cloud's own reason, cut to a readable length.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("WSO2 Cloud refused the call (%d): %s", e.Status, e.Message)
}

// ErrNotAuthorized is returned when WSO2 Cloud rejects the CSM backend's token
// (401) or the token lacks the operation's scope (403).
var ErrNotAuthorized = errors.New("WSO2 Cloud did not authorize the CSM backend for this call")

// Subscription is one product subscription of an organization, as the CSM
// portal shows it.
type Subscription struct {
	ID       string `json:"id"`
	Product  string `json:"product"`
	PlanCode string `json:"planCode"`
	Status   string `json:"status"`
	TrialEnd string `json:"trialEnd,omitempty"`
}

// ListSubscriptions returns the organization's subscriptions
// (GET /organizations/{org_id}/subscriptions, scope billing:organizations:read).
func (c *BillingClient) ListSubscriptions(ctx context.Context, orgID string) ([]Subscription, error) {
	var raw json.RawMessage
	if err := c.call(ctx, http.MethodGet, "/organizations/"+url.PathEscape(orgID)+"/subscriptions", nil, &raw); err != nil {
		return nil, err
	}
	return parseSubscriptions(raw)
}

// CreateBillingAccount creates a wire-transfer billing account for the
// organization and returns its id (POST /organizations/{org_id}/billing-accounts,
// scope billing:billing-accounts:create). csmRequest is the CSM request
// number, kept in the account's metadata so it can be traced back.
func (c *BillingClient) CreateBillingAccount(ctx context.Context, orgID, csmRequest string) (string, error) {
	body := map[string]any{"channel": "wire"}
	if csmRequest != "" {
		body["metadata"] = map[string]string{"csm_request": csmRequest}
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/organizations/"+url.PathEscape(orgID)+"/billing-accounts", body, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", errors.New("WSO2 Cloud created a billing account but returned no id")
	}
	return out.ID, nil
}

// PlanChange is the body of a plan change.
type PlanChange struct {
	PlanCode         string `json:"plan_code"`
	BillingAccountID string `json:"billing_account_id"`
	BillingPeriod    string `json:"billing_period,omitempty"`
	NotifyCustomer   bool   `json:"notify_customer"`
	Notes            string `json:"notes,omitempty"`
}

// ChangePlan moves a subscription to an Enterprise plan billed by the given
// billing account, activates it and drafts the first invoice
// (POST /organizations/{org_id}/subscriptions/{subscription_id}/plan-change,
// scope billing:subscription-plan:change). Safe to retry: the same plan and
// account again is a 200 that changes nothing. WSO2 Cloud answers 409 when the
// subscription or organization cannot be changed (suspended, cancelled, being
// deleted, usage over the plan's limits, not allowed mid-period, a linked
// product).
func (c *BillingClient) ChangePlan(ctx context.Context, orgID, subscriptionID string, change PlanChange) error {
	path := "/organizations/" + url.PathEscape(orgID) + "/subscriptions/" + url.PathEscape(subscriptionID) + "/plan-change"
	return c.call(ctx, http.MethodPost, path, change, nil)
}

func (c *BillingClient) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The token request's own failure ends up here too; its error never
		// carries the secret.
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s %s: %w", method, path, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s %s: %d: %w", method, path, resp.StatusCode, ErrNotAuthorized)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return &APIError{Status: resp.StatusCode, Message: errorMessage(data, resp.StatusCode)}
	case resp.StatusCode >= 300:
		return fmt.Errorf("%s %s: WSO2 Cloud answered %d", method, path, resp.StatusCode)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// errorMessage picks WSO2 Cloud's reason out of an error body: the first of
// message, detail, description, error_description or error that is text, else
// the status text.
func errorMessage(data []byte, status int) string {
	var body map[string]any
	if json.Unmarshal(data, &body) == nil {
		for _, key := range []string{"message", "detail", "description", "error_description", "error"} {
			if s, ok := body[key].(string); ok && strings.TrimSpace(s) != "" {
				return truncate(strings.TrimSpace(s))
			}
		}
	}
	return http.StatusText(status)
}

func truncate(s string) string {
	if r := []rune(s); len(r) > maxErrorMessage {
		return string(r[:maxErrorMessage]) + "…"
	}
	return s
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
