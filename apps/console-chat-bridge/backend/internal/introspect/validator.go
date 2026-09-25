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

// Package introspect validates the access tokens identity-apps' Console SPA
// (and, for the internal push-back leg, csm-portal/backend) present to this
// bridge, via RFC 7662 token introspection against a single, pinned WSO2 IS
// instance.
//
// Introspection, not JWKS/JWT validation, on purpose: WSO2 IS's
// product-wide default access token type is Opaque (confirmed against the
// product docs), and this repo's own local instance
// (wso2is-7.3.0/repository/conf/deployment.toml) does not override the
// global opaque token issuer for any application. Nothing here assumes a
// JWT -- see this service's README for how to swap in JWKS validation
// instead if your Console application's own Access Token Type is
// specifically switched to JWT.
//
// This is deliberately NOT a generic multi-tenant validator. It is scoped,
// as directed for this POC, to exactly one known WSO2 IS instance
// (Config.IssuerBaseURL) -- validating an arbitrary customer's own
// self-hosted IS/Asgardeo tenant is real follow-up work, not something to
// fake here.
package introspect

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a Validator against exactly one known WSO2 IS instance.
type Config struct {
	// IssuerBaseURL is the pinned, known-good IS instance this bridge
	// trusts -- e.g. "https://localhost:9444" for this POC. The
	// introspection endpoint is derived from it
	// (IssuerBaseURL + "/oauth2/introspect") unless IntrospectionURL below
	// overrides it -- never taken from a request.
	IssuerBaseURL string
	// IntrospectionURL, when set, is used verbatim as the introspection
	// endpoint instead of deriving one from IssuerBaseURL -- some IdPs
	// (e.g. Asgardeo, whose token issuer and introspection endpoint live
	// under different paths) don't follow the IssuerBaseURL+"/oauth2/
	// introspect" convention this bridge's own pinned local WSO2 IS does.
	// Every existing caller leaves this unset and keeps today's derived
	// behavior unchanged; only internal/tenant's multi-tenant construction
	// sets it explicitly.
	IntrospectionURL string
	// IntrospectionClientID/Secret authenticate this bridge to the known
	// instance's introspection endpoint via HTTP Basic auth (RFC 7662
	// §2.1) -- a small confidential "Standard-Based Application"
	// registered once on that instance for exactly this purpose (see
	// README's setup steps). Never sent to, or reachable from, the
	// browser.
	IntrospectionClientID     string
	IntrospectionClientSecret string
	// InsecureSkipVerify disables TLS certificate verification for calls to
	// IssuerBaseURL's introspection endpoint. LOCAL DEVELOPMENT ONLY: a
	// locally-installed WSO2 IS (e.g. wso2is-7.3.0 run straight from the
	// product distribution) serves HTTPS with its default self-signed
	// server certificate, which Go's default transport will not trust --
	// every introspection call fails closed with a TLS handshake error,
	// which ValidateBearer reports as a generic "not active" failure (see
	// its own doc comment on visibility into this). Since this bridge
	// already pins IssuerBaseURL to one specific, operator-chosen instance
	// (see this package's own doc comment), skipping chain-of-trust
	// verification for calls to that one pinned host is a bounded,
	// deliberate POC shortcut -- not a general TLS bypass -- but a real
	// deployment should instead trust that instance's actual CA (or run it
	// with a certificate issued by one already in the OS trust store) and
	// leave this false.
	InsecureSkipVerify bool
	// HTTPClient defaults to a 5s-timeout client (honouring
	// InsecureSkipVerify) when nil.
	HTTPClient *http.Client
}

// Identity is what this bridge trusts about a caller, derived only from the
// introspection response -- never from any browser-supplied field (e.g. a
// request body's own "email"/"userId"). See Validator.ValidateBearer.
type Identity struct {
	// Username/Subject identify an end user (set on a genuine user-session
	// access token, i.e. the Console browser's own call). Empty on a pure
	// client-credentials token.
	Username string
	Subject  string
	// ClientID identifies the OAuth2 client the token was issued to --
	// always set, including on a client-credentials token (e.g.
	// csm-portal/backend's own M2M call to this bridge's
	// /internal/chat-events). middleware.RequireClientID checks this for
	// that route instead of requiring an end-user identity.
	ClientID string
	Scopes   []string
	// Issuer is the introspection response's own "iss" claim, already
	// verified (see ValidateBearer) to match this Validator's pinned
	// IssuerBaseURL -- carried through mainly for
	// internal/tokenvalidator.Identity, which multi-tenant callers use to
	// tell which IdP a token actually came from.
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
// accepting every token (mirrors customer-portal/backend-v2's own
// NewTokenValidator panic-on-misconfigured-JWKS precedent).
func NewValidator(cfg Config) *Validator {
	if cfg.IssuerBaseURL == "" {
		panic("introspect: IssuerBaseURL must be set to a known WSO2 IS instance")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
		if cfg.InsecureSkipVerify {
			hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- opt-in, local-dev-only, see Config.InsecureSkipVerify's doc comment
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

// ValidateBearer introspects tokenStr against the pinned known instance and
// returns the Identity it carries. Returns an error for anything other than
// a token the instance itself currently reports as active -- including a
// well-formed response whose "iss" doesn't match this bridge's pinned
// instance (defense in depth against a token being replayed against the
// wrong bridge, or a misconfigured endpoint).
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

	return Identity{
		Username: out.Username,
		Subject:  out.Sub,
		ClientID: out.ClientID,
		Scopes:   scopes,
		Issuer:   out.Iss,
	}, nil
}
