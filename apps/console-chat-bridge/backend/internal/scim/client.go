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

// Package scim resolves a WSO2 IS username (as introspection's own
// "username" claim carries it) to that user's immutable SCIM id, via a
// trusted, server-to-server call to WSO2 IS's native SCIM2 Users API
// (GET /scim2/Users) -- authenticated with this bridge's own OAuth2
// client-credentials grant, never the caller's token.
//
// Why this exists: some IdP applications (WSO2 IS's own built-in "Console"
// app among them) issue access tokens with cookie-based token binding.
// WSO2 IS's OIDC UserInfo endpoint (internal/introspect.Validator's own
// ResolveSubjectViaUserinfo) then requires the browser's binding cookie to
// be presented alongside the bearer token -- something a server-side
// backend, calling UserInfo with only the forwarded Authorization header,
// structurally can never provide (the cookie is scoped to the browser's
// origin and never reaches a third-party API). Confirmed live against this
// bridge's own pinned local WSO2 IS instance: UserInfo returns
// 400 "Valid token binding value not present in the request" for exactly
// this token shape, even though RFC 7662 introspection of the same token
// succeeds fine (introspection is a server-to-server RP endpoint and is
// never binding-gated).
//
// This package sidesteps that entirely: it never touches the caller's own
// token or the browser's binding cookie. It authenticates as ITSELF (a
// separate, least-privilege client-credentials application, scoped only to
// SCIM2's read/view capability) and looks the already-introspected
// username up directly -- a mechanism token binding has no bearing on,
// since it's not the end user's session being used to call a protected
// resource.
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
	// {BaseURL}/scim2/Users on every WSO2 IS instance) -- typically the
	// same value as internal/introspect.Config.IssuerBaseURL.
	BaseURL string
	// TokenURL, ClientID, ClientSecret, and Scopes authenticate this client
	// via the OAuth2 client-credentials grant. This must be a SEPARATE,
	// least-privilege registration from whatever authenticates
	// introspection calls (that one only ever does RFC 7662 Basic auth) --
	// Scopes should carry only WSO2 IS's SCIM2-view scope
	// ("internal_user_mgt_list"), nothing else.
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// InsecureSkipVerify disables TLS certificate verification for calls to
	// BaseURL/TokenURL. LOCAL DEVELOPMENT ONLY -- mirrors
	// introspect.Config.InsecureSkipVerify's own doc comment; a real
	// deployment should instead trust that instance's actual CA.
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

// The three ways ResolveUserID can fail, kept distinguishable so a caller
// can tell "this IdP genuinely has no such user" (ErrNoMatch), "this
// username isn't uniquely resolvable" (ErrAmbiguous) apart from "the
// lookup itself didn't work" (ErrUnavailable) -- every one of them means
// "do not trust a Subject from this call", the caller just may want to log
// or report them differently.
var (
	// ErrNoMatch means SCIM2 returned zero matching users.
	ErrNoMatch = errors.New("scim: no user matches the given username")
	// ErrAmbiguous means SCIM2 returned more than one matching user (e.g.
	// the same local username exists in more than one user store) --
	// treated as untrustworthy rather than guessing which one is right.
	ErrAmbiguous = errors.New("scim: multiple users match the given username")
	// ErrUnavailable means the lookup itself failed: a transport/auth
	// failure, a non-2xx response, or a response that didn't parse as the
	// expected SCIM2 ListResponse shape.
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

// ResolveUserID resolves username -- exactly as introspection's own
// "username" claim carries it, e.g. "admin@carbon.super" -- to WSO2 IS's
// immutable per-user SCIM id (the same stable UUID a JWT-typed access
// token's own "sub" claim carries). username is used ONLY as a search key;
// the returned id, never username itself, is what a caller should treat as
// a canonical Subject.
//
// Strips WSO2's own tenant-domain suffix (the segment after the last "@")
// before searching, mirroring org.wso2.carbon.utils.multitenancy.
// MultitenantUtils.getTenantAwareUsername's own default-configuration
// behavior (confirmed by decompiling org.wso2.carbon.utils_4.12.34.jar's
// actual bytecode, not just its Javadoc): with tenant email-as-username
// disabled -- this deployment's own configuration, and the common default
// -- that method unconditionally does
// username.substring(0, username.lastIndexOf('@')), regardless of which
// tenant domain follows (not hardcoded to "carbon.super"), so this covers
// any tenant, not only the super tenant.
//
// Known, deliberate gap: when a tenant instead has email-as-username
// *enabled*, the real getTenantAwareUsername applies a different,
// context-dependent heuristic for a username with exactly one "@" (i.e.
// one that is itself shaped like an email address) that this function
// cannot replicate, because whether that mode is even enabled for a given
// tenant is a server-side WSO2 IS configuration this service has no API to
// query. This bridge is POC-scoped to one known, pinned instance (see this
// package's own doc comment) whose email-as-username setting is already
// known to be disabled -- live-verified end to end against it -- so this
// is a documented scope limitation for a future email-as-username tenant,
// not an unnoticed one.
//
// Fails closed in every case that isn't a single, unambiguous match: zero
// results (ErrNoMatch), more than one result (ErrAmbiguous), and any
// transport/auth/non-2xx/malformed-response failure (ErrUnavailable). Never
// falls back to returning username itself.
func (c *Client) ResolveUserID(ctx context.Context, username string) (string, error) {
	local := username
	if i := strings.LastIndex(username, "@"); i >= 0 {
		local = username[:i]
	}
	if local == "" {
		return "", ErrNoMatch
	}

	// The filter's string literal follows SCIM2's own value syntax (RFC
	// 7644 §3.4.2.2), which is JSON string syntax (RFC 8259) -- NOT Go
	// string-literal syntax (e.g. fmt's %q), and distinct from this
	// request's own URL-encoding (q.Encode() below, which only protects
	// the query string transport, not the quoted literal's own content).
	// encoding/json.Marshal on a string produces exactly that: a
	// correctly-escaped, double-quoted JSON string, so a username
	// containing a literal '"' or '\' can never break out of the filter's
	// quoted value and inject additional filter syntax.
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
