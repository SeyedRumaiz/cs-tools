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

// Package tokenvalidator provides a per-tenant abstraction over
// internal/introspect.Validator, so the multi-tenant /v1/{tenant}/... API
// (see internal/tenant) can validate each tenant's bearer tokens against
// that tenant's own IdP. The legacy /support/chats path calls
// internal/introspect directly and never uses this package.
package tokenvalidator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
)

// Identity is a type alias for introspect.Identity: an *introspect.Identity
// value (e.g. from middleware.IdentityFromContext) and a
// *tokenvalidator.Identity value are the same type, no conversion needed.
type Identity = introspect.Identity

// TokenValidator validates a bearer token for one tenant and returns the
// Identity it carries. No field on Identity is validator-mandatory here --
// route-level policy (canonicalOwner requiring Subject on /v1, RequireUser
// requiring Subject-or-Username on /support/chats, RequireClientID requiring
// ClientID) decides what it actually needs for a given route.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (*Identity, error)
}

// IntrospectionValidator adapts an *introspect.Validator to the
// TokenValidator interface — the only validator kind implemented today.
// This is the layer that owns "the generic /v1 API requires a canonical
// Subject": the legacy /support path calls
// introspect.Validator.ValidateBearer directly and never goes through a
// TokenValidator, so this enrichment/requirement logic never runs for it.
type IntrospectionValidator struct {
	v    *introspect.Validator
	scim scimResolver
}

// scimResolver is implemented by *internal/scim.Client, declared as a
// narrow local interface so tests can supply a fake without a real SCIM2
// server.
type scimResolver interface {
	ResolveUserID(ctx context.Context, username string) (string, error)
}

// NewIntrospectionValidator wraps v. SCIM-based Subject resolution is not
// configured by default — see WithSCIMResolver.
func NewIntrospectionValidator(v *introspect.Validator) *IntrospectionValidator {
	return &IntrospectionValidator{v: v}
}

// WithSCIMResolver configures iv to resolve Subject via scim (an immutable
// WSO2 SCIM user id) instead of the UserInfo-based fallback, whenever
// introspection alone doesn't carry a Subject. Optional: a tenant that
// never calls this keeps UserInfo-only enrichment. Returns iv for chaining
// at construction time. Useful for IdPs whose access tokens are
// token-binding-bound to the browser, which makes UserInfo reject this
// bridge's bearer-only server-side call.
func (iv *IntrospectionValidator) WithSCIMResolver(scim scimResolver) *IntrospectionValidator {
	iv.scim = scim
	return iv
}

// Validate introspects token via introspect.Validator.ValidateBearer, then
// enriches the result with a canonical Subject when introspection's own
// response didn't carry one: via iv.scim.ResolveUserID when a SCIM
// resolver is configured (see WithSCIMResolver), otherwise via
// ResolveSubjectViaUserinfo. Enrichment is attempted only for a token with
// a non-empty Username and an empty Subject; a tenant whose IdP already
// returns "sub" from introspection never triggers either path.
//
// Returns an error, rather than leaving Subject empty, when enrichment was
// attempted but didn't produce a usable Subject — every /v1 caller that
// reaches this validator requires one.
func (iv *IntrospectionValidator) Validate(ctx context.Context, token string) (*Identity, error) {
	id, err := iv.v.ValidateBearer(ctx, token)
	if err != nil {
		return nil, err
	}
	if id.Subject == "" && id.Username != "" {
		var subject string
		if iv.scim != nil {
			subject, err = iv.scim.ResolveUserID(ctx, id.Username)
		} else {
			subject, err = iv.v.ResolveSubjectViaUserinfo(ctx, token)
		}
		if err != nil {
			return nil, fmt.Errorf("tokenvalidator: resolve canonical subject: %w", err)
		}
		id.Subject = subject
	}
	return &id, nil
}

// JWKSValidatorConfig configures a JWKSValidator.
type JWKSValidatorConfig struct {
	// JWKSURI is this tenant's JWKS endpoint, fetched once at construction
	// time (see NewJWKSValidator) and kept refreshed by the underlying
	// keyfunc.Keyfunc for the life of the validator.
	JWKSURI string
	// Issuer, when non-empty, must match the token's "iss" claim exactly.
	Issuer string
	// Audience, when non-empty, must appear in the token's "aud" claim.
	Audience string
	// InsecureSkipVerify disables TLS certificate verification for the
	// JWKS fetch. LOCAL DEVELOPMENT ONLY — mirrors
	// introspect.Config.InsecureSkipVerify's own doc comment.
	InsecureSkipVerify bool
}

// JWKSValidator verifies a JWT-typed access token's signature against a
// tenant's own JWKS, as the alternative to IntrospectionValidator for a
// tenant whose IdP issues JWT (rather than Opaque) access tokens.
type JWKSValidator struct {
	cfg     JWKSValidatorConfig
	keyFunc jwt.Keyfunc
}

// jwksClaims is the expected JWT access-token payload shape. username and
// client_id mirror the same-named fields introspection returns for an
// Opaque token from the same class of IdP (see introspectionResponse in
// internal/introspect) — WSO2 IS/Asgardeo's JWT access tokens carry both
// as top-level claims.
type jwksClaims struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	jwt.RegisteredClaims
}

// NewJWKSValidator constructs a JWKSValidator and starts keyfunc's own
// background JWKS fetch/refresh for cfg.JWKSURI. Only cfg.JWKSURI being
// empty fails construction outright — an unreachable or malformed JWKS
// endpoint does not: keyfunc logs the failure and keeps retrying in the
// background (so a transient fetch failure at startup can still recover),
// which means Validate will reject every token as unverifiable (no
// matching key found) until a fetch actually succeeds, rather than this
// constructor returning an error.
func NewJWKSValidator(ctx context.Context, cfg JWKSValidatorConfig) (*JWKSValidator, error) {
	if cfg.JWKSURI == "" {
		return nil, errors.New("tokenvalidator: JWKSURI is required for validationType jwks")
	}

	transport := http.RoundTripper(&x5cStrippingTransport{base: http.DefaultTransport})
	if cfg.InsecureSkipVerify {
		transport = &x5cStrippingTransport{base: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 — opt-in, local-dev-only, see JWKSValidatorConfig.InsecureSkipVerify's doc comment
	}

	jwks, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{cfg.JWKSURI}, keyfunc.Override{Client: &http.Client{Transport: transport}})
	if err != nil {
		return nil, fmt.Errorf("tokenvalidator: fetch JWKS from %s: %w", cfg.JWKSURI, err)
	}
	return &JWKSValidator{cfg: cfg, keyFunc: jwks.Keyfunc}, nil
}

// Validate verifies token's signature against jv's JWKS and, when
// configured, its issuer and audience. Returns an error for an expired,
// malformed, badly-signed, or issuer/audience-mismatched token, or one
// carrying no usable identity (no sub, username, or client_id claim) —
// every /v1 caller that reaches this validator requires at least one.
func (jv *JWKSValidator) Validate(_ context.Context, token string) (*Identity, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("tokenvalidator: empty token")
	}

	opts := []jwt.ParserOption{jwt.WithExpirationRequired()}
	if jv.cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(jv.cfg.Issuer))
	}
	if jv.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(jv.cfg.Audience))
	}

	var c jwksClaims
	parsed, err := jwt.ParseWithClaims(token, &c, jv.keyFunc, opts...)
	if err != nil {
		return nil, fmt.Errorf("tokenvalidator: validate token: %w", err)
	}
	if !parsed.Valid {
		return nil, errors.New("tokenvalidator: invalid token")
	}
	if c.Subject == "" && c.Username == "" && c.ClientID == "" {
		return nil, errors.New("tokenvalidator: token carries no usable identity")
	}

	var scopes []string
	if c.Scope != "" {
		scopes = strings.Fields(c.Scope)
	}
	// Some IdPs (e.g. Devant's STS) issue no username claim, only email.
	username := c.Username
	if username == "" {
		username = c.Email
	}
	return &Identity{
		Username: username,
		Subject:  c.Subject,
		ClientID: c.ClientID,
		Scopes:   scopes,
		Issuer:   c.Issuer,
	}, nil
}

// x5cStrippingTransport removes the "x5c" certificate chain from every key
// in a JWKS response before it reaches the jwkset parser. Verification
// only needs "n"/"e" (or the EC/OKP equivalents); jwkset unconditionally
// parses "x5c" as X.509 certificates, and some IdPs (Asgardeo included)
// publish certs with a negative serial number that Go's x509 parser
// rejects since Go 1.23, which would otherwise make the whole JWK Set
// fail to load.
type x5cStrippingTransport struct {
	base http.RoundTripper
}

func (t *x5cStrippingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read JWKS response body: %w", err)
	}

	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		// Not a JWKS document we can sanitize; hand back the original body untouched.
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}

	for _, key := range jwks.Keys {
		delete(key, "x5c")
	}
	sanitized, err := json.Marshal(jwks)
	if err != nil {
		return nil, fmt.Errorf("marshal sanitized JWKS: %w", err)
	}

	resp.Body = io.NopCloser(bytes.NewReader(sanitized))
	resp.ContentLength = int64(len(sanitized))
	resp.Header.Set("Content-Length", fmt.Sprint(len(sanitized)))
	return resp, nil
}
