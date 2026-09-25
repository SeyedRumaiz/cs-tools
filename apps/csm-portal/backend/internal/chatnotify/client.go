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
// live-engineer-chat events into a case's origin (customer-portal/
// backend-v2's open browser WebSocket, or console-chat-bridge's per-case
// SSE stream -- see ChatHandler.notifiers/notifierFor) via
// POST /internal/chat-events, and to create a case on the engineer's behalf
// when a chat is converted (POST /internal/chat/create-case).
//
// Both calls are pure machine-to-machine: there is no end-user identity
// behind them (CreateCase forwards the accepting engineer's own
// x-user-id-token as a plain header instead -- see that method below). That
// puts them in the same class as integrations/csm-integration-service's
// inbound API, not the receiving service's own browser-facing routes, so
// this client follows that service's established M2M pattern instead of a
// browser-oriented Auth model: a plain OAuth2 client-credentials token
// attached as a standard Authorization: Bearer header, trusted entirely at
// Choreo's API Manager gateway (subscription + client-credentials app
// auth) rather than validated again in-process. See
// integrations/csm-integration-service/CLAUDE.md's "Why no Auth
// middleware" section for the rationale this mirrors, and backend-v2's own
// internal/middleware.Auth exemption for these two routes for the
// receiving side.
//
// An earlier version of this client instead forced the client-credentials
// token into x-jwt-assertion so it would pass backend-v2's browser-facing
// Auth middleware, which requires "email"/"userid" claims on every token.
// That would have meant provisioning backend-v2's OAuth2 IdP application to
// emit synthetic end-user claims on a client-credentials grant purely to
// satisfy a check these two routes have no end-user identity to supply --
// this repo already has an established, gateway-trust pattern for exactly
// this situation, so that requirement was removed instead of worked
// around.
//
// The HTTP-client plumbing itself (OAuth2 client-credentials, refuse
// redirects, bound the error body) used to be hand-rolled here and
// independently re-hand-rolled in backend-v2's and console-chat-bridge's
// own twin packages; all three now build on the shared
// apps/live-chat-sdk/sdk-go/m2mclient package instead.
package chatnotify

import (
	"context"
	"fmt"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/m2mclient"
)

// Config holds the configuration for an origin's internal push client.
type Config struct {
	// BaseURL is the target origin's internal listener base URL (e.g.
	// backend-v2's WS_PORT listener, or console-chat-bridge's main
	// listener).
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// against the target via the OAuth2 client-credentials grant.
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// InsecureSkipVerify disables TLS certificate verification for the
	// TokenURL request. LOCAL DEVELOPMENT ONLY: every consumer that talks
	// to a real, CA-signed TokenURL (api.asgardeo.io) leaves this false --
	// it only matters for a target whose TokenURL points at a
	// locally-installed WSO2 IS serving its own self-signed certificate,
	// which Go's default transport won't trust, failing every token fetch
	// (and therefore every push) with "x509: certificate signed by unknown
	// authority". Mirrors console-chat-bridge's own
	// INTROSPECTION_INSECURE_SKIP_VERIFY for the identical reason. A real
	// deployment should instead trust that instance's actual CA and leave
	// this false.
	InsecureSkipVerify bool
}

// Client pushes chat events to a case's origin.
type Client struct {
	m2m *m2mclient.Client
}

// NewClient constructs a Client. Does not validate connectivity -- the
// first PushEvent call surfaces a dial failure, logged (not fatal) by the
// caller, consistent with this feature's "best effort" live-relay design: a
// failed push never blocks the case/comment write that already succeeded.
func NewClient(cfg Config) *Client {
	return &Client{m2m: m2mclient.NewClient(m2mclient.Config{
		BaseURL:            cfg.BaseURL,
		TokenURL:           cfg.TokenURL,
		ClientID:           cfg.ClientID,
		ClientSecret:       cfg.ClientSecret,
		Scopes:             cfg.Scopes,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	})}
}

// PushEvent POSTs the given JSON payload (a marshaled
// live-chat-sdk/sdk-go/pushevents.ChatEvent) to the target's
// POST /internal/chat-events. Returns an error on any non-2xx response or
// transport failure; callers treat this as best-effort (see NewClient) and
// must not fail the caller-facing request over it.
func (c *Client) PushEvent(ctx context.Context, payload []byte) error {
	body, status, err := c.m2m.Do(ctx, http.MethodPost, "/internal/chat-events", payload, nil)
	if err != nil {
		return fmt.Errorf("chatnotify: push event: %w", err)
	}
	if !m2mclient.Success(status) {
		return fmt.Errorf("chatnotify: push event: upstream returned %d: %s", status, body)
	}
	return nil
}

// CreateCase POSTs payload to the target's POST /internal/chat/create-case
// and returns the raw response body. Unlike PushEvent, this is NOT
// best-effort -- the caller needs the new case's ID back synchronously.
// userIDToken is forwarded as an x-user-id-token header since the target
// has no end-user session to derive one from on this internal route.
func (c *Client) CreateCase(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
	var headers map[string]string
	if userIDToken != "" {
		// Forwarded on to entity-service by backend-v2's HandleCreateCase
		// (see that handler's doc comment) -- entity-service's CreateCase
		// requires this header, and this internal route has no end-user
		// session of its own to derive one from otherwise.
		headers = map[string]string{"x-user-id-token": userIDToken}
	}
	body, status, err := c.m2m.Do(ctx, http.MethodPost, "/internal/chat/create-case", payload, headers)
	if err != nil {
		return nil, fmt.Errorf("chatnotify: create case: %w", err)
	}
	if !m2mclient.Success(status) {
		return nil, fmt.Errorf("chatnotify: create case: upstream returned %d: %s", status, body)
	}
	return body, nil
}
