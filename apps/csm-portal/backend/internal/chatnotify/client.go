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
// Both calls are pure machine-to-machine: there is no end-user identity
// behind them (CreateCase forwards the accepting engineer's own
// x-user-id-token as a plain header instead — see that method below). That
// puts them in the same class as integrations/csm-integration-service's
// inbound API, not customer-portal/backend-v2's browser-facing routes, so
// this client follows that service's established M2M pattern instead of
// backend-v2's browser-oriented internal/middleware.Auth model: a plain
// OAuth2 client-credentials token attached as a standard Authorization:
// Bearer header (via clientcredentials.Config.Client(), the same shape
// integrations/acp-closure-service/internal/entity/client.go uses against
// csm-integration-service), trusted entirely at Choreo's API Manager
// gateway (subscription + client-credentials app auth) rather than
// validated again in-process. See
// integrations/csm-integration-service/CLAUDE.md's "Why no Auth
// middleware" section for the rationale this mirrors, and
// backend-v2's own internal/middleware.Auth exemption for these two
// routes (POST /internal/chat-events, POST /internal/chat/create-case) for
// the receiving side.
//
// An earlier version of this client instead forced the client-credentials
// token into x-jwt-assertion so it would pass backend-v2's browser-facing
// Auth middleware, which requires "email"/"userid" claims on every token.
// That would have meant provisioning backend-v2's OAuth2 IdP application to
// emit synthetic end-user claims on a client-credentials grant purely to
// satisfy a check these two routes have no end-user identity to supply --
// this repo already has an established, gateway-trust pattern for exactly
// this situation, so that requirement was removed instead of worked around.
package chatnotify

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

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
	// call is unaffected by that constraint and is exempted from backend-v2's
	// Auth middleware entirely -- see the package doc comment).
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
	// InsecureSkipVerify disables TLS certificate verification for the
	// TokenURL request. LOCAL DEVELOPMENT ONLY: every other consumer of this
	// client talks to a real, CA-signed TokenURL (api.asgardeo.io), so this
	// never mattered before the "asgardeo" notifier target was added
	// (cmd/server/main.go) — that one points at a locally-installed WSO2 IS
	// serving its own self-signed certificate, which Go's default transport
	// won't trust, failing every token fetch (and therefore every push to
	// console-chat-bridge) with "x509: certificate signed by unknown
	// authority". Mirrors console-chat-bridge's own
	// INTROSPECTION_INSECURE_SKIP_VERIFY (internal/introspect.Config) for
	// the identical reason. A real deployment should instead trust that
	// instance's actual CA and leave this false.
	InsecureSkipVerify bool
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
	tokenHTTPClient := &http.Client{Timeout: tokenFetchTimeout}
	if cfg.InsecureSkipVerify {
		tokenHTTPClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- opt-in, local-dev-only, see Config.InsecureSkipVerify's doc comment
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, tokenHTTPClient)
	httpClient := cc.Client(tokenCtx)
	httpClient.Timeout = 10 * time.Second
	// oauth2.Transport reattaches the Authorization bearer token to every
	// request it processes, including a followed redirect to a different
	// host. Refuse to follow so the token can never leak to wherever
	// backend-v2 says to redirect to (mirrors
	// integrations/acp-closure-service/internal/entity/client.go's identical
	// guard against its own M2M target).
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		http:    httpClient,
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
