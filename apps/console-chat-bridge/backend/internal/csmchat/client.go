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

// Package csmchat is the outbound HTTP client this bridge uses to call
// csm-portal/backend's live-engineer-chat endpoints: escalate,
// customer-message, and complete under /internal/chat/..., and case
// ownership/transcript lookups under /internal/chat/cases/{caseId}. Every
// call is machine-to-machine, authenticated separately from the caller
// (see internal/introspect).
//
// It builds on the shared apps/live-chat-sdk/sdk-go/m2mclient package for
// OAuth2 client-credentials transport, keeping its own identity and typed
// methods (Escalate/SendCustomerMessage) on top.
package csmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/m2mclient"
)

// Config holds the configuration for the csm-portal/backend internal
// client.
type Config struct {
	// BaseURL is csm-portal/backend's main API listener base URL.
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// against csm-portal/backend via the OAuth2 client-credentials grant.
	// ClientID/Secret should be a dedicated registration carrying only the
	// least-privilege "internal_console_chat_escalate" scope.
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// Client calls csm-portal/backend's internal chat endpoints.
type Client struct {
	m2m *m2mclient.Client
}

// NewClient constructs a Client.
func NewClient(cfg Config) *Client {
	return &Client{m2m: m2mclient.NewClient(m2mclient.Config{
		BaseURL:      cfg.BaseURL,
		TokenURL:     cfg.TokenURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       cfg.Scopes,
	})}
}

// Escalate POSTs to csm-portal/backend's POST /internal/chat/escalate.
// Returns the response body and status so the caller (chats.go) can pass
// csm-portal/backend's own duplicate-open-chat 409 straight through to the
// browser instead of masking it as a generic failure.
func (c *Client) Escalate(ctx context.Context, payload []byte) ([]byte, int, error) {
	body, status, err := c.m2m.Do(ctx, http.MethodPost, "/internal/chat/escalate", payload, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("csmchat: escalate: %w", err)
	}
	if !m2mclient.Success(status) {
		return body, status, fmt.Errorf("csmchat: escalate: upstream returned %d", status)
	}
	return body, status, nil
}

// SendCustomerMessage POSTs to csm-portal/backend's
// POST /internal/chat/customer-message.
func (c *Client) SendCustomerMessage(ctx context.Context, payload []byte) error {
	body, status, err := c.m2m.Do(ctx, http.MethodPost, "/internal/chat/customer-message", payload, nil)
	if err != nil {
		return fmt.Errorf("csmchat: customer-message: %w", err)
	}
	if !m2mclient.Success(status) {
		return fmt.Errorf("csmchat: customer-message: upstream returned %d: %s", status, body)
	}
	return nil
}

// CaseOwnership mirrors csm-portal/backend's caseOwnershipResponse
// (GET /internal/chat/cases/{caseId}).
type CaseOwnership struct {
	TenantSlug string `json:"tenantSlug,omitempty"`
	Source     string `json:"source,omitempty"`
	Channel    string `json:"channel,omitempty"`
}

// GetCaseOwnership calls csm-portal/backend's
// GET /internal/chat/cases/{caseId} — the source of truth behind
// requireTenantCase's authorization check. Returns an error for any
// non-2xx response, including a 404 for an unrecognized case.
func (c *Client) GetCaseOwnership(ctx context.Context, caseID string) (CaseOwnership, error) {
	body, status, err := c.m2m.Do(ctx, http.MethodGet, "/internal/chat/cases/"+url.PathEscape(caseID), nil, nil)
	if err != nil {
		return CaseOwnership{}, fmt.Errorf("csmchat: get case ownership: %w", err)
	}
	if !m2mclient.Success(status) {
		return CaseOwnership{}, fmt.Errorf("csmchat: get case ownership: upstream returned %d: %s", status, body)
	}
	var out CaseOwnership
	if err := json.Unmarshal(body, &out); err != nil {
		return CaseOwnership{}, fmt.Errorf("csmchat: get case ownership: decode response: %w", err)
	}
	return out, nil
}

// HistoryMessage mirrors one entry of csm-portal/backend's
// caseHistoryResponse.PriorMessages (itself routingclient.PriorMessage) --
// Role is "customer", "assistant", or "engineer".
type HistoryMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// GetCaseHistory calls csm-portal/backend's
// GET /internal/chat/cases/{caseId}/history, returning caseID's full
// transcript to date. Callers must confirm the requester owns caseID (see
// requireOwner/requireTenantCase) before calling this.
func (c *Client) GetCaseHistory(ctx context.Context, caseID string) ([]HistoryMessage, error) {
	body, status, err := c.m2m.Do(ctx, http.MethodGet, "/internal/chat/cases/"+url.PathEscape(caseID)+"/history", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("csmchat: get case history: %w", err)
	}
	if !m2mclient.Success(status) {
		return nil, fmt.Errorf("csmchat: get case history: upstream returned %d: %s", status, body)
	}
	var out struct {
		PriorMessages []HistoryMessage `json:"priorMessages"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("csmchat: get case history: decode response: %w", err)
	}
	return out.PriorMessages, nil
}

// CompleteByTenant POSTs to csm-portal/backend's
// POST /internal/chat/complete — the tenant-initiated (customer-side)
// session-completion counterpart to the engineer-initiated
// POST /chat/sessions/{id}/complete (which this bridge never calls).
func (c *Client) CompleteByTenant(ctx context.Context, payload []byte) error {
	body, status, err := c.m2m.Do(ctx, http.MethodPost, "/internal/chat/complete", payload, nil)
	if err != nil {
		return fmt.Errorf("csmchat: complete: %w", err)
	}
	if !m2mclient.Success(status) {
		return fmt.Errorf("csmchat: complete: upstream returned %d: %s", status, body)
	}
	return nil
}
