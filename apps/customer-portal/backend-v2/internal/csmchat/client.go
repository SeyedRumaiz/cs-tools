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
// Both calls are pure machine-to-machine: neither of csm-portal/backend's
// receiving handlers (HandleEscalate, HandleCustomerMessage) ever consumes
// an end-user identity from the request. That puts them in the same class
// as integrations/csm-integration-service's inbound API, not
// csm-portal/backend's own browser-facing routes, so this client follows
// that service's established M2M pattern instead of the browser-oriented
// internal/middleware.Auth model: a plain OAuth2 client-credentials token
// attached as a standard Authorization: Bearer header, trusted entirely at
// Choreo's API Manager gateway (subscription + client-credentials app
// auth) rather than validated again in-process. See
// integrations/csm-integration-service/CLAUDE.md's "Why no Auth
// middleware" section for the rationale this mirrors, and
// csm-portal/backend's own internal/middleware.Auth exemption for these
// two routes (POST /internal/chat/escalate, POST /internal/chat/customer-message)
// for the receiving side.
//
// An earlier version of this client instead forced the client-credentials
// token into x-jwt-assertion so it would pass csm-portal/backend's
// browser-facing Auth middleware, which requires "email"/"userid" claims on
// every token. That would have meant provisioning csm-portal/backend's
// OAuth2 IdP application to emit synthetic end-user claims on a
// client-credentials grant purely to satisfy a check these two routes have
// no end-user identity to supply -- this repo already has an established,
// gateway-trust pattern for exactly this situation, so that requirement
// was removed instead of worked around.
//
// The HTTP-client plumbing itself (OAuth2 client-credentials, refuse
// redirects, bound the error body) used to be hand-rolled here and
// independently re-hand-rolled in console-chat-bridge's own twin of this
// package; both now build on the shared
// apps/live-chat-sdk/sdk-go/m2mclient package instead.
package csmchat

import (
	"context"
	"fmt"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/m2mclient"
)

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

func (c *Client) post(ctx context.Context, path string, payload []byte) error {
	body, status, err := c.m2m.Do(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		return fmt.Errorf("csmchat: %s: %w", path, err)
	}
	if !m2mclient.Success(status) {
		return apierror.NewUpstreamError(status, body)
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
