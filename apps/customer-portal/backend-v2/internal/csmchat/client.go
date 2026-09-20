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
// csm-portal/backend's live-engineer-chat endpoints
// (POST /internal/chat/escalate, POST /internal/chat/customer-message),
// both now served on that backend's main API listener alongside its
// browser-facing routes.
//
// This authenticates the same way every other inter-service client in this
// backend does: the OAuth2 client-credentials grant (see internal/entity's
// client for the canonical example this mirrors), NOT a bespoke
// shared-secret scheme. The one twist is where the resulting token is
// attached: csm-portal/backend's inbound routes are guarded by its own
// internal/middleware.Auth, which reads the access token from
// x-jwt-assertion (never Authorization), so jwtAssertionTransport below
// attaches it there instead of relying on oauth2.Transport's default
// Authorization: Bearer header. See this backend's own
// internal/middleware.Auth, which validates the reverse direction
// (csm-portal calling into POST /internal/chat-events and
// POST /internal/chat/create-case) the identical way.
//
// This does require csm-portal/backend's OAuth2 identity-provider
// application (the one issuing tokens for Config.TokenURL/ClientID) to be
// provisioned to emit "email" and "userid" claims on its client-credentials
// tokens, since middleware.Auth's extractUserInfo hard-requires both. That
// is an IdP/infra provisioning step, not something this code can satisfy on
// its own -- flagged here so it isn't mistaken for something this refactor
// forgot.
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
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// jwtAssertionHeader must match internal/middleware.jwtAssertionHeader in
// this backend and csm-portal/backend's identical constant -- that is the
// header both backends' middleware.Auth validates.
const jwtAssertionHeader = "x-jwt-assertion"

// tokenFetchTimeout is the HTTP client timeout for token-endpoint requests.
var tokenFetchTimeout = 10 * time.Second

// maxResponseBodyBytes bounds how much of a response this client reads.
const maxResponseBodyBytes = 64 << 10 // 64 KiB

// Config holds the configuration for the csm-portal/backend internal client.
type Config struct {
	// BaseURL is csm-portal/backend's main API listener base URL -- the
	// live-engineer-chat internal routes are registered there alongside
	// its browser-facing routes (see that backend's cmd/server/main.go),
	// not on a separate listener.
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// against csm-portal/backend via the OAuth2 client-credentials grant --
	// the same shared app (and usually the same
	// TokenURL/ClientID/ClientSecret values) this backend already uses for
	// entity/updates/scim/etc., just with its own Scopes.
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
		return nil, fmt.Errorf("csmchat: fetch service token: %w", err)
	}
	req = req.Clone(req.Context())
	req.Header.Set(jwtAssertionHeader, token.AccessToken)
	return t.base.RoundTrip(req)
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

func (c *Client) post(ctx context.Context, path string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("csmchat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

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
