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

// Package scim resolves a WSO2 IS username (as introspection's "username"
// claim carries it) to that user's immutable SCIM id, via a server-to-
// server call to WSO2 IS's native SCIM2 Users API (GET /scim2/Users)
// authenticated with this bridge's own OAuth2 client-credentials grant,
// never the caller's token.
//
// This exists because some IdP applications (including WSO2 IS's built-in
// "Console" app) issue access tokens with cookie-based token binding, which
// makes the OIDC UserInfo endpoint reject a server-side call that can't
// present the browser's binding cookie. SCIM2 lookup sidesteps this: it
// never touches the caller's token, authenticating instead as a separate,
// least-privilege client scoped only to SCIM2 read access.
package scim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/m2mclient"
)

// Config configures a Client.
type Config struct {
	// BaseURL is the WSO2 IS instance's own origin (SCIM2 lives at
	// {BaseURL}/scim2/Users on every WSO2 IS instance) — typically the
	// same value as internal/introspect.Config.IssuerBaseURL.
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// via the OAuth2 client-credentials grant. Must be a separate,
	// least-privilege registration from whatever authenticates
	// introspection, with Scopes carrying only WSO2 IS's SCIM2-view scope
	// ("internal_user_mgt_list").
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// InsecureSkipVerify disables TLS certificate verification for calls to
	// BaseURL/TokenURL. LOCAL DEVELOPMENT ONLY.
	InsecureSkipVerify bool
}

// Client looks up WSO2 IS user ids via SCIM2.
type Client struct {
	m2m *m2mclient.Client
}

// NewClient constructs a Client.
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

// The three ways ResolveUserID can fail; all mean "do not trust a Subject
// from this call", but stay distinguishable via errors.Is for logging.
var (
	// ErrNoMatch means SCIM2 returned zero matching users.
	ErrNoMatch = errors.New("scim: no user matches the given username")
	// ErrAmbiguous means SCIM2 returned more than one matching user.
	ErrAmbiguous = errors.New("scim: multiple users match the given username")
	// ErrUnavailable means the lookup itself failed: a transport/auth
	// failure, a non-2xx response, or an unparseable response.
	ErrUnavailable = errors.New("scim: user lookup failed")
)

// listResponse is the subset of a SCIM2 ListResponse (RFC 7644 §3.4.2) this
// package needs.
type listResponse struct {
	TotalResults int `json:"totalResults"`
	Resources    []struct {
		ID string `json:"id"`
	} `json:"Resources"`
}

// ResolveUserID resolves username (as introspection's "username" claim
// carries it, e.g. "admin@carbon.super") to WSO2 IS's immutable per-user
// SCIM id. username is used only as a search key; the returned id, never
// username itself, is what a caller should treat as a canonical Subject.
//
// Strips WSO2's tenant-domain suffix (the segment after the last "@")
// before searching, matching MultitenantUtils.getTenantAwareUsername's
// default behavior. Known gap: a tenant with email-as-username enabled
// uses a different heuristic this function can't replicate — this bridge
// is scoped to a known instance where that mode is disabled.
//
// Fails closed on anything but a single, unambiguous match — see
// ErrNoMatch, ErrAmbiguous, ErrUnavailable. Never falls back to returning
// username itself.
func (c *Client) ResolveUserID(ctx context.Context, username string) (string, error) {
	local := username
	if i := strings.LastIndex(username, "@"); i >= 0 {
		local = username[:i]
	}
	if local == "" {
		return "", ErrNoMatch
	}

	// SCIM2 filter values use JSON string syntax (RFC 7644 §3.4.2.2), not
	// Go's %q — json.Marshal escapes '"' and '\' so local can't break out
	// of the quoted value and inject filter syntax.
	filterValue, err := json.Marshal(local)
	if err != nil {
		return "", fmt.Errorf("%w: encode filter value: %w", ErrUnavailable, err)
	}

	q := url.Values{}
	q.Set("filter", "userName eq "+string(filterValue))
	q.Set("attributes", "id,userName")

	body, status, err := c.m2m.Do(ctx, http.MethodGet, "/scim2/Users?"+q.Encode(), nil, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if !m2mclient.Success(status) {
		return "", fmt.Errorf("%w: status %d", ErrUnavailable, status)
	}

	var resp listResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("%w: decode response: %w", ErrUnavailable, err)
	}

	switch {
	case resp.TotalResults == 0 || len(resp.Resources) == 0:
		return "", ErrNoMatch
	case resp.TotalResults > 1 || len(resp.Resources) > 1:
		return "", ErrAmbiguous
	case resp.Resources[0].ID == "":
		return "", fmt.Errorf("%w: matched user has no id", ErrUnavailable)
	default:
		return resp.Resources[0].ID, nil
	}
}
