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
// csm-portal/backend's live-engineer-chat endpoints
// (POST /internal/chat/escalate, POST /internal/chat/customer-message) --
// deliberately copied from customer-portal/backend-v2's own
// internal/csmchat/client.go rather than shared, matching this repo's
// existing convention of duplicating small M2M clients per caller (see that
// file's own package doc comment for the M2M/gateway-trust rationale this
// mirrors exactly). Both calls are pure machine-to-machine: this bridge
// itself already validated the Console admin's identity (see
// internal/introspect) before ever reaching here, and forwards only what
// csm-portal/backend's existing escalateRequest/customerMessageRequest
// shapes already accept -- no new fields, no new route.
package csmchat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const maxResponseBodyBytes = 64 << 10 // 64 KiB

// Config holds the configuration for the csm-portal/backend internal
// client.
type Config struct {
	// BaseURL is csm-portal/backend's main API listener base URL.
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// against csm-portal/backend via the OAuth2 client-credentials grant.
	// ClientID/Secret should be a dedicated registration distinct from
	// backend-v2's own, carrying only the least-privilege
	// "internal_console_chat_escalate" scope (see this bridge's README) --
	// least-privilege per consumer, same principle backend-v2's own
	// csmchat client already follows for its own scopes.
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// Client calls csm-portal/backend's internal chat endpoints.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient constructs a Client.
func NewClient(cfg Config) *Client {
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient,
		&http.Client{Timeout: 10 * time.Second})
	httpClient := cc.Client(tokenCtx)
	httpClient.Timeout = 10 * time.Second
	// Never follow a redirect: refuse rather than let oauth2.Transport
	// reattach this bearer token to a different host (mirrors
	// backend-v2's internal/csmchat.Client identical guard).
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		http:    httpClient,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
	}
}

func (c *Client) post(ctx context.Context, path string, payload []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("csmchat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("csmchat: %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return body, resp.StatusCode, fmt.Errorf("csmchat: %s: upstream returned %d", path, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

// Escalate POSTs to csm-portal/backend's POST /internal/chat/escalate.
// Returns the response body and status so the caller (chats.go) can pass
// csm-portal/backend's own duplicate-open-chat 409 straight through to the
// Console browser instead of masking it as a generic failure.
func (c *Client) Escalate(ctx context.Context, payload []byte) ([]byte, int, error) {
	return c.post(ctx, "/internal/chat/escalate", payload)
}

// SendCustomerMessage POSTs to csm-portal/backend's
// POST /internal/chat/customer-message.
func (c *Client) SendCustomerMessage(ctx context.Context, payload []byte) error {
	_, _, err := c.post(ctx, "/internal/chat/customer-message", payload)
	return err
}
