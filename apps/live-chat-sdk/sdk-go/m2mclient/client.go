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

// Package m2mclient is the shared outbound-HTTP-client plumbing behind
// every service-to-service call in the live-engineer-chat feature:
// customer-portal/backend-v2's internal/csmchat, console-chat-bridge's
// internal/csmchat, and csm-portal/backend's internal/chatnotify were three
// independently hand-rolled copies of the exact same "OAuth2
// client-credentials token, refuse redirects, bound the error body" shape
// (console-chat-bridge's own copy even said so in its own doc comment:
// "deliberately copied ... rather than shared"). This package is that
// shared plumbing; each of the three keeps its own typed methods
// (Escalate/PushEvent/CreateCase/etc.) and package identity, now built on
// top of one Client instead of three copies of the same boilerplate.
//
// Every call here is pure machine-to-machine, trusted entirely at Choreo's
// API Manager gateway (subscription + client-credentials app auth) rather
// than validated again in-process by the receiving service -- see any of
// the three wrapping packages' own doc comments for the full rationale this
// mirrors.
package m2mclient

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

// TokenFetchTimeout is the HTTP client timeout for token-endpoint requests.
// A package-level var (not const) so a caller's own tests can shorten it,
// matching the convention each of the three original clients already had.
var TokenFetchTimeout = 10 * time.Second

// requestTimeout bounds the whole outbound call, token fetch included.
const requestTimeout = 10 * time.Second

// MaxResponseBodyBytes bounds how much of a response this client reads into
// memory -- matches all three original clients' own identical constant.
const MaxResponseBodyBytes = 64 << 10 // 64 KiB

// Config configures a Client's OAuth2 client-credentials grant and target.
type Config struct {
	// BaseURL is the target service's base URL; every call's path is
	// resolved against it.
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// via the OAuth2 client-credentials grant.
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// InsecureSkipVerify disables TLS certificate verification for the
	// TokenURL request. LOCAL DEVELOPMENT ONLY -- e.g. a target IdP that is
	// a locally-installed instance serving its own self-signed certificate,
	// which Go's default transport won't trust. Mirrors
	// console-chat-bridge's own introspect.Config.InsecureSkipVerify for
	// the identical reason. A real deployment should instead trust that
	// instance's actual CA and leave this false.
	InsecureSkipVerify bool
}

// Client is the shared HTTP client every M2M wrapper package builds on.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient constructs a Client. Does not validate connectivity -- the
// first Do call surfaces a dial failure; every current caller of this
// package treats that as best-effort (logged, not fatal to the request
// that triggered it).
func NewClient(cfg Config) *Client {
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	tokenHTTPClient := &http.Client{Timeout: TokenFetchTimeout}
	if cfg.InsecureSkipVerify {
		tokenHTTPClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- opt-in, local-dev-only, see Config.InsecureSkipVerify's doc comment
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, tokenHTTPClient)
	httpClient := cc.Client(tokenCtx)
	httpClient.Timeout = requestTimeout
	// oauth2.Transport reattaches the Authorization bearer token to every
	// request it processes, including a followed redirect to a different
	// host. Refuse to follow so the token can never leak to wherever the
	// target service says to redirect to.
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		http:    httpClient,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
	}
}

// Do issues method to path (resolved against Config.BaseURL) with payload
// as the JSON request body (nil for none) and any extra headers set after
// Content-Type (so a caller can override it, though none currently do).
// Returns the response body, status code, and a non-nil error only for a
// transport-level failure (dial/timeout/etc.) or a failure to read the
// response body -- an HTTP-level non-2xx status is returned as a normal
// (body, status, nil) result, since what counts as "failure" differs per
// caller (e.g. one path passes a specific 409 straight through instead of
// treating it as an error).
func (c *Client) Do(ctx context.Context, method, path string, payload []byte, headers map[string]string) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, fmt.Errorf("m2mclient: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("m2mclient: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("m2mclient: %s %s: read response: %w", method, path, err)
	}
	return respBody, resp.StatusCode, nil
}

// Success reports whether status is a 2xx -- a small shared helper so every
// wrapper package's own "is this an error" check reads the same way.
func Success(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}
