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

// This is the shared HTTP client that handles OAuth authentication for backend-to-backend calls.

// Package m2mclient provides the shared HTTP client plumbing for
// service-to-service calls in the live-engineer-chat feature: an OAuth2
// client-credentials token, no followed redirects, and a bounded response
// body. Wrapper packages build their own typed methods on top of one
// Client.
//
// Calls are authenticated at Choreo's API Manager gateway (subscription +
// client-credentials app auth); the receiving service does not re-validate
// them.
// Authentication is enforced by Choreo’s gateway.
// This client supplies credentials; it does not configure or enforce gateway security.
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
// It is a var, not a const, so tests can shorten it.
var TokenFetchTimeout = 10 * time.Second

// requestTimeout bounds the whole outbound call, token fetch included.
const requestTimeout = 10 * time.Second

// MaxResponseBodyBytes bounds how much of a response this client reads
// into memory.
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
	// TokenURL request. LOCAL DEVELOPMENT ONLY, e.g. a locally-installed
	// IdP serving a self-signed certificate. A real deployment should
	// trust that instance's actual CA and leave this false.
	InsecureSkipVerify bool
}

// Client is the shared HTTP client every M2M wrapper package builds on.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient constructs a Client. It does not validate connectivity; a
// dial failure only surfaces on the first Do call.
func NewClient(cfg Config) *Client {
	// Configure clietn credentials grant
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	// This client is used for token acquisition only.
	tokenHTTPClient := &http.Client{Timeout: TokenFetchTimeout}
	if cfg.InsecureSkipVerify {
		tokenHTTPClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 — opt-in, local-dev-only, see Config.InsecureSkipVerify's doc comment
	}
	// Give the OAuth library that HTTP client
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, tokenHTTPClient)
	// Create an authenticated HTTP client
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
// as the JSON request body (nil for none), plus any extra headers set
// after Content-Type so a caller can override it.
//
// It returns a non-nil error only for a transport-level failure or a
// failure reading the response body; a non-2xx status is returned as a
// normal (body, status, nil) result for the caller to interpret.
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

// Success reports whether status is a 2xx status code.
func Success(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}
