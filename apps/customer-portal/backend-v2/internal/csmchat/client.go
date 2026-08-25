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

// Package csmchat is the outbound HTTP client this backend uses to call
// csm-portal/backend's internal live-engineer-chat endpoints
// (POST /internal/chat/escalate, POST /internal/chat/customer-message).
//
// Unlike this backend's other upstream clients (entity, registry, SCIM,
// ...), this is NOT an OAuth2 client-credentials call: csm-portal/backend
// and this backend do not share a JWT issuer/audience for a "service"
// identity, so authentication here is a plain shared bearer secret
// (X-Internal-Chat-Token) instead. See internal/middleware's own
// InternalToken check, which guards the reverse direction
// (POST /internal/chat-events, csm-portal calling into this backend).
package csmchat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/apierror"
)

// internalTokenHeader must match csm-portal/backend's
// internal/middleware.InternalTokenHeader.
const internalTokenHeader = "X-Internal-Chat-Token"

// maxResponseBodyBytes bounds how much of a response this client reads.
const maxResponseBodyBytes = 64 << 10 // 64 KiB

// Config holds the configuration for the csm-portal/backend internal client.
type Config struct {
	// BaseURL is csm-portal/backend's internal listener base URL (its
	// INTERNAL_CHAT_PORT listener — see that backend's cmd/server/main.go).
	BaseURL string
	// InternalToken is the shared secret sent as X-Internal-Chat-Token. Must
	// equal csm-portal/backend's own INTERNAL_CHAT_TOKEN.
	InternalToken string
}

// Client calls csm-portal/backend's internal chat endpoints.
type Client struct {
	http    *http.Client
	baseURL string
	token   string
}

// NewClient constructs a Client.
func NewClient(cfg Config) *Client {
	return &Client{
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.InternalToken,
	}
}

func (c *Client) post(ctx context.Context, path string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("csmchat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("csmchat: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		return apierror.NewUpstreamError(resp.StatusCode, body)
	}
	return nil
}

// Escalate POSTs to csm-portal/backend's POST /internal/chat/escalate,
// fanning a new live-engineer-chat escalation out to connected engineers.
func (c *Client) Escalate(ctx context.Context, payload []byte) error {
	return c.post(ctx, "/internal/chat/escalate", payload)
}

// SendCustomerMessage POSTs to csm-portal/backend's
// POST /internal/chat/customer-message, relaying a customer's message to
// the engineer who accepted the session.
func (c *Client) SendCustomerMessage(ctx context.Context, payload []byte) error {
	return c.post(ctx, "/internal/chat/customer-message", payload)
}
