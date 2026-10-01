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
	"context"
	"errors"
	"fmt"

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

// ErrJWKSNotImplemented is returned by JWKSValidator.Validate. A
// validationType "jwks" tenant can be configured today (see
// internal/tenant.Config), but actual JWT/JWKS verification is deferred
// follow-up work.
var ErrJWKSNotImplemented = errors.New("tokenvalidator: JWKS validation is not implemented yet")

// JWKSValidatorConfig configures a JWKSValidator — accepted and stored now
// so a tenant row can declare validationType "jwks" without a
// TENANT_REGISTRY parse error, even though Validate always fails until
// JWT/JWKS verification lands.
type JWKSValidatorConfig struct {
	JWKSURI  string
	Issuer   string
	Audience string
}

// JWKSValidator is a stub TokenValidator for a JWT-issuing tenant. See
// ErrJWKSNotImplemented.
type JWKSValidator struct {
	cfg JWKSValidatorConfig
}

// NewJWKSValidator constructs a JWKSValidator. Does not fetch cfg.JWKSURI or
// validate connectivity — there is nothing to verify against yet.
func NewJWKSValidator(cfg JWKSValidatorConfig) *JWKSValidator {
	return &JWKSValidator{cfg: cfg}
}

// Validate always fails — see ErrJWKSNotImplemented.
func (jv *JWKSValidator) Validate(context.Context, string) (*Identity, error) {
	return nil, ErrJWKSNotImplemented
}
