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

// Package introspect validates access tokens presented to this bridge, via
// RFC 7662 token introspection against a single, pinned WSO2 IS instance
// (Config.IssuerBaseURL). Introspection, not JWKS/JWT validation, since
// WSO2 IS's default access token type is Opaque; see the README to swap in
// JWKS validation if a Console application's Access Token Type is JWT.
// Validating an arbitrary customer's own IS/Asgardeo tenant is out of
// scope here — see internal/tenant for the multi-tenant equivalent.
package introspect

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a Validator against exactly one known WSO2 IS instance.
type Config struct {
	// IssuerBaseURL is the pinned IS instance this bridge trusts, e.g.
	// "https://localhost:9444". The introspection endpoint is derived from
	// it (IssuerBaseURL + "/oauth2/introspect") unless IntrospectionURL
	// overrides it.
	IssuerBaseURL string
	// IntrospectionURL, when set, is used verbatim as the introspection
	// endpoint instead of deriving one from IssuerBaseURL — needed for an
	// IdP (e.g. Asgardeo) whose introspection endpoint lives elsewhere.
	IntrospectionURL string
	// UserinfoURL, when set, is used verbatim as the OIDC UserInfo endpoint
	// instead of deriving one from IssuerBaseURL. Only consulted by
	// ResolveSubjectViaUserinfo — ValidateBearer itself never calls
	// userinfo.
	UserinfoURL string
	// IntrospectionClientID/Secret authenticate this bridge to the
	// instance's introspection endpoint via HTTP Basic auth (RFC 7662
	// §2.1). Never sent to, or reachable from, the browser.
	IntrospectionClientID     string
	IntrospectionClientSecret string
	// InsecureSkipVerify disables TLS certificate verification for calls to
	// IssuerBaseURL's introspection endpoint. LOCAL DEVELOPMENT ONLY: a
	// self-signed local WSO2 IS instance otherwise fails every
	// introspection call with a TLS handshake error. A real deployment
	// should trust that instance's actual CA instead and leave this false.
	InsecureSkipVerify bool
	// HTTPClient defaults to a 5s-timeout client (honouring
	// InsecureSkipVerify) when nil.
	HTTPClient *http.Client
}

// Identity is what this bridge trusts about a caller, derived only from the
// introspection response, never from any browser-supplied field. Subject
// may be empty even for a genuine end-user token — base validation never
// enriches it; a caller that requires a non-empty canonical Subject must
// call ResolveSubjectViaUserinfo itself.
type Identity struct {
	// Username/Subject identify an end user. Empty on a pure
	// client-credentials token.
	Username string
	Subject  string
	// ClientID identifies the OAuth2 client the token was issued to --
	// always set, including on a client-credentials token. Checked by
	// middleware.RequireClientID instead of requiring an end-user identity.
	ClientID string
	Scopes   []string
	// Issuer is the introspection response's "iss" claim, already verified
	// (see ValidateBearer) to match this Validator's pinned IssuerBaseURL.
	Issuer string
}

// Validator introspects opaque access tokens against one pinned IS
// instance.
type Validator struct {
	cfg Config
	hc  *http.Client
}

// NewValidator builds a Validator. Panics if IssuerBaseURL is empty --
// misconfiguration here must fail loudly at startup rather than silently
// accepting every token.
func NewValidator(cfg Config) *Validator {
	if cfg.IssuerBaseURL == "" {
		panic("introspect: IssuerBaseURL must be set to a known WSO2 IS instance")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
		if cfg.InsecureSkipVerify {
			hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 — opt-in, local-dev-only, see Config.InsecureSkipVerify's doc comment
		}
	}
	return &Validator{cfg: cfg, hc: hc}
}

// introspectionResponse is RFC 7662's response shape, trimmed to the fields
// this bridge actually reads.
type introspectionResponse struct {
	Active   bool   `json:"active"`
	Username string `json:"username"`
	Sub      string `json:"sub"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	Iss      string `json:"iss"`
}

// userinfoResponse is the OIDC UserInfo endpoint's response shape, trimmed
// to the one claim ValidateBearer's fallback actually needs.
type userinfoResponse struct {
	Sub string `json:"sub"`
}

// The three ways ResolveSubjectViaUserinfo can fail, kept distinguishable
// via errors.Is: ErrUserinfoInsufficientScope/ErrUserinfoNoSubject mean the
// token itself can't produce a Subject, while ErrUserinfoUnavailable means
// this bridge's connectivity to the IdP failed — operationally different
// even though every caller today surfaces both as the same generic 401.
var (
	// ErrUserinfoInsufficientScope means introspection reported the token
	// active, but UserInfo rejected it (RFC 6750 401/403, typically
	// "insufficient_scope").
	ErrUserinfoInsufficientScope = errors.New("introspect: userinfo endpoint rejected the token")
	// ErrUserinfoNoSubject means UserInfo answered 200 but its response
	// carried no usable "sub" claim.
	ErrUserinfoNoSubject = errors.New("introspect: userinfo response has no usable sub claim")
	// ErrUserinfoUnavailable means calling UserInfo failed for a reason
	// unrelated to the caller's token (network failure, unexpected status,
	// malformed body).
	ErrUserinfoUnavailable = errors.New("introspect: userinfo endpoint unavailable")
)

// ResolveSubjectViaUserinfo calls the configured OIDC UserInfo endpoint
// with the same bearer token and returns its "sub" claim. This is a
// separate, opt-in enrichment step — ValidateBearer never calls it itself.
// Today the only caller is tokenvalidator.IntrospectionValidator.Validate,
// which needs a canonical Subject for the generic /v1 API and calls this
// whenever introspection's own Subject came back empty for what looks like
// a genuine end-user token.
func (v *Validator) ResolveSubjectViaUserinfo(ctx context.Context, tokenStr string) (string, error) {
	userinfoURL := v.cfg.UserinfoURL
	if userinfoURL == "" {
		userinfoURL = strings.TrimRight(v.cfg.IssuerBaseURL, "/") + "/oauth2/userinfo"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build request: %w", ErrUserinfoUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenStr)

	resp, err := v.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: call userinfo endpoint: %w", ErrUserinfoUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// RFC 6750 — a scope-insufficient or otherwise-rejected token
		// reaches userinfo as 401/403, not a 5xx.
		return "", fmt.Errorf("%w: status %d", ErrUserinfoInsufficientScope, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("%w: unexpected status %d", ErrUserinfoUnavailable, resp.StatusCode)
	}

	var out userinfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("%w: decode response: %w", ErrUserinfoUnavailable, err)
	}
	if out.Sub == "" {
		return "", ErrUserinfoNoSubject
	}
	return out.Sub, nil
}

// ValidateBearer introspects tokenStr against the pinned known instance and
// returns the Identity it carries. Returns an error for anything other
// than a token the instance reports active, including a well-formed
// response whose "iss" doesn't match this bridge's pinned instance.
//
// Base validation only — RFC 7662 introspection and nothing else. It
// deliberately never calls UserInfo, even when the response carries no
// "sub": that would make the legacy /support path (which tolerates an
// empty Subject) fail whenever UserInfo is slow or down, though it never
// needed Subject. A caller that requires a canonical Subject calls
// ResolveSubjectViaUserinfo itself.
func (v *Validator) ValidateBearer(ctx context.Context, tokenStr string) (Identity, error) {
	if strings.TrimSpace(tokenStr) == "" {
		return Identity{}, fmt.Errorf("introspect: empty token")
	}

	introspectionURL := v.cfg.IntrospectionURL
	if introspectionURL == "" {
		introspectionURL = strings.TrimRight(v.cfg.IssuerBaseURL, "/") + "/oauth2/introspect"
	}

	form := url.Values{"token": {tokenStr}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		introspectionURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("introspect: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(v.cfg.IntrospectionClientID, v.cfg.IntrospectionClientSecret)

	resp, err := v.hc.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("introspect: call introspection endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("introspect: introspection endpoint returned %d", resp.StatusCode)
	}

	var out introspectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Identity{}, fmt.Errorf("introspect: decode response: %w", err)
	}

	if !out.Active {
		return Identity{}, fmt.Errorf("introspect: token is not active")
	}
	if out.Iss != "" && !strings.HasPrefix(out.Iss, v.cfg.IssuerBaseURL) {
		return Identity{}, fmt.Errorf("introspect: unexpected issuer %q", out.Iss)
	}
	if out.Username == "" && out.Sub == "" && out.ClientID == "" {
		return Identity{}, fmt.Errorf("introspect: token carries no usable identity")
	}

	var scopes []string
	if out.Scope != "" {
		scopes = strings.Fields(out.Scope)
	}

	// Subject is whatever introspection itself returned — possibly empty
	// for an Opaque access token. Enriching it is deliberately not this
	// method's job; see the doc comment above.
	return Identity{
		Username: out.Username,
		Subject:  out.Sub,
		ClientID: out.ClientID,
		Scopes:   scopes,
		Issuer:   out.Iss,
	}, nil
}
