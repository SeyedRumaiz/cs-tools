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

// Package chatnotify is the outbound HTTP client this backend uses to push
// live-engineer-chat events into customer-portal/backend-v2's open browser
// WebSocket for a conversation (POST /internal/chat-events).
//
// This is deliberately NOT authenticated the same way as this backend's own
// inbound endpoints (see internal/middleware.Auth): it is a server-to-server
// call between two backends that do not share a JWT audience/issuer for a
// "service identity", so it carries a shared bearer secret
// (X-Internal-Chat-Token) instead of a user JWT. See
// internal/middleware.InternalToken for the matching inbound check this
// backend applies to the reverse direction (backend-v2 calling into this
// backend's own /internal/chat/escalate).
package chatnotify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// internalTokenHeader must match internal/middleware.InternalTokenHeader and
// the header name customer-portal/backend-v2 checks on its own
// /internal/chat-events endpoint.
const internalTokenHeader = "X-Internal-Chat-Token"

// maxResponseBodyBytes bounds how much of an error response this client will
// read into memory/log.
const maxResponseBodyBytes = 64 << 10 // 64 KiB

// Config holds the configuration for the backend-v2 internal push client.
type Config struct {
	// BaseURL is customer-portal/backend-v2's internal listener base URL
	// (its WS_PORT listener — see that backend's cmd/server/main.go, which
	// registers POST /internal/chat-events on the same unauthenticated
	// listener as GET /ws, since neither can carry a user x-jwt-assertion).
	BaseURL string
	// InternalToken is the shared secret sent as X-Internal-Chat-Token. Must
	// equal the INTERNAL_CHAT_TOKEN backend-v2 is configured with.
	InternalToken string
}

// Client pushes chat events to customer-portal/backend-v2.
type Client struct {
	http    *http.Client
	baseURL string
	token   string
}

// NewClient constructs a Client. Does not validate connectivity — the first
// PushEvent call surfaces a dial failure, logged (not fatal) by the caller,
// consistent with this feature's "best effort" live-relay design: a failed
// push never blocks the case/comment write that already succeeded.
func NewClient(cfg Config) *Client {
	return &Client{
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.InternalToken,
	}
}

// PushEvent POSTs the given JSON payload to backend-v2's
// POST /internal/chat-events. Returns an error on any non-2xx response or
// transport failure; callers treat this as best-effort (see NewClient) and
// must not fail the caller-facing request over it.
func (c *Client) PushEvent(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/chat-events", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("chatnotify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("chatnotify: push event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		return fmt.Errorf("chatnotify: push event: upstream returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
