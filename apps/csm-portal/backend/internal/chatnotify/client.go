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
// WebSocket for a conversation (POST /internal/chat-events) and to create a
// case on the engineer's behalf when a chat is converted
// (POST /internal/chat/create-case).
//
// This authenticates the same way every other inter-service client in this
// repo does: the OAuth2 client-credentials grant (see
// internal/entity.NewCustomerEntityClient for the canonical example this
// mirrors), NOT a bespoke shared-secret scheme. The one twist is where the
// resulting token is attached: backend-v2's inbound routes are guarded by
// its own internal/middleware.Auth, which reads the access token from
// x-jwt-assertion (never Authorization), so jwtAssertionTransport below
// attaches it there instead of relying on oauth2.Transport's default
// Authorization: Bearer header. See internal/middleware.Auth in this
// backend for the identical inbound check this backend applies to the
// reverse direction (backend-v2 calling into this backend's own
// /internal/chat/escalate and /internal/chat/customer-message).
//
// This does require backend-v2's OAuth2 identity-provider application (the
// one issuing tokens for Config.TokenURL/ClientID) to be provisioned to
// emit "email" and "userid" claims on its client-credentials tokens, since
// middleware.Auth's extractUserInfo hard-requires both. That is an
// IdP/infra provisioning step, not something this code can satisfy on its
// own -- flagged here so it isn't mistaken for something this refactor
// forgot.
package chatnotify

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

// jwtAssertionHeader must match internal/middleware.jwtAssertionHeader in
// this backend and customer-portal/backend-v2's identical constant --
// that is the header both backends' middleware.Auth validates.
const jwtAssertionHeader = "x-jwt-assertion"

// tokenFetchTimeout is the HTTP client timeout for token-endpoint requests.
// Overridden in tests to keep them fast.
var tokenFetchTimeout = 10 * time.Second

// maxResponseBodyBytes bounds how much of an error response this client will
// read into memory/log.
const maxResponseBodyBytes = 64 << 10 // 64 KiB

// Config holds the configuration for the backend-v2 internal push client.
type Config struct {
	// BaseURL is customer-portal/backend-v2's internal listener base URL
	// (its WS_PORT listener -- see that backend's cmd/server/main.go, which
	// registers POST /internal/chat-events and POST /internal/chat/create-case
	// on the same listener as GET /ws, since a browser cannot carry an
	// x-jwt-assertion header on a WebSocket handshake; this service-to-service
	// call is unaffected by that constraint, and authenticates via the
	// backend's normal Auth middleware like any other route).
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// against backend-v2 via the OAuth2 client-credentials grant -- the
	// same shared app (and usually the same TokenURL/ClientID/ClientSecret
	// values) this backend already uses for entity/updates/scim, just with
	// its own Scopes.
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// jwtAssertionTransport attaches an OAuth2 client-credentials token to every
// outgoing request as x-jwt-assertion. source (an oauth2.TokenSource)
// already caches and refreshes the token exactly as oauth2.Transport would;
// the only difference from that default transport is the header the token
// is attached under, since the receiving side (middleware.Auth) reads
// x-jwt-assertion, never Authorization.
type jwtAssertionTransport struct {
	base   http.RoundTripper
	source oauth2.TokenSource
}

func (t *jwtAssertionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.source.Token()
	if err != nil {
		return nil, fmt.Errorf("chatnotify: fetch service token: %w", err)
	}
	req = req.Clone(req.Context())
	req.Header.Set(jwtAssertionHeader, token.AccessToken)
	return t.base.RoundTrip(req)
}

// Client pushes chat events to customer-portal/backend-v2.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient constructs a Client. Does not validate connectivity — the first
// PushEvent call surfaces a dial failure, logged (not fatal) by the caller,
// consistent with this feature's "best effort" live-relay design: a failed
// push never blocks the case/comment write that already succeeded.
func NewClient(cfg Config) *Client {
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient,
		&http.Client{Timeout: tokenFetchTimeout})

	return &Client{
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &jwtAssertionTransport{
				base:   http.DefaultTransport,
				source: cc.TokenSource(tokenCtx),
			},
		},
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
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

// CreateCase POSTs payload to backend-v2's POST /internal/chat/create-case
// and returns the raw response body. Unlike PushEvent, this is NOT
// best-effort -- the caller needs the new case's ID back synchronously.
// userIDToken is forwarded as an x-user-id-token header since backend-v2
// has no end-user session to derive one from on this internal route.
func (c *Client) CreateCase(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/chat/create-case", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("chatnotify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Forwarded on to entity-service by backend-v2's HandleCreateCase (see
	// that handler's doc comment) -- entity-service's CreateCase requires
	// this header, and this internal route has no end-user session of its
	// own to derive one from otherwise.
	if userIDToken != "" {
		req.Header.Set("x-user-id-token", userIDToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatnotify: create case: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("chatnotify: create case: read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("chatnotify: create case: upstream returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
