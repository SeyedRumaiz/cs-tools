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

// Package tokenvalidator generalizes this bridge's original single-pinned-
// instance internal/introspect.Validator into a per-tenant abstraction, so
// the multi-tenant /v1/{tenant}/... API (see internal/tenant) can validate
// each tenant's bearer tokens against that tenant's own IdP, while the
// legacy /support/chats path keeps calling internal/introspect directly,
// completely unchanged.
package tokenvalidator

import (
	"context"
	"errors"
	"fmt"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
)

// Identity is a type alias for introspect.Identity, not a separate struct --
// every field a caller needs (Subject/Username/ClientID/Scopes/Issuer) is
// already there, and aliasing means an *introspect.Identity value (e.g. from
// middleware.IdentityFromContext on the legacy path) and a *tokenvalidator.
// Identity value (from a TokenValidator on the /v1 path) are the exact same
// type -- no conversion, no risk of the two drifting apart field-by-field.
type Identity = introspect.Identity

// TokenValidator validates a bearer token for one tenant and returns the
// Identity it carries. No field on Identity is validator-mandatory here --
// route-level policy (canonicalOwner requiring Subject on /v1, RequireUser
// requiring Subject-or-Username on /support/chats, RequireClientID requiring
// ClientID) decides what it actually needs for a given route.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (*Identity, error)
}

// IntrospectionValidator adapts an *introspect.Validator (RFC 7662 token
// introspection against one pinned IdP instance) to the TokenValidator
// interface -- the only validator kind actually implemented today. Every
// tenant configured with validationType "introspection" (see
// internal/tenant.Config) gets its own *introspect.Validator instance,
// constructed once at startup against that tenant's own Issuer/
// IntrospectionURL/ClientID/secret, and wrapped here.
//
// This is deliberately the layer that owns "the generic /v1 API requires a
// canonical Subject" -- not introspect.Validator.ValidateBearer itself (see
// that method's own doc comment on why), and not a route-name check
// anywhere in chat handler code. TokenValidator is only ever constructed
// for a /v1 tenant (see internal/tenant.buildValidator) -- the legacy
// /support path's middleware.Auth calls introspect.Validator.ValidateBearer
// directly and never goes through a TokenValidator at all, so this
// enrichment/requirement logic simply never runs for it.
type IntrospectionValidator struct {
	v    *introspect.Validator
	scim scimResolver
}

// scimResolver is implemented by *internal/scim.Client. Declared as a
// narrow local interface (rather than importing internal/scim's concrete
// type) so this package stays generic and tests can supply a fake without a
// real SCIM2 server -- the same reason introspect.Validator itself never
// imports anything WSO2-specific beyond RFC 7662/OIDC UserInfo.
type scimResolver interface {
	ResolveUserID(ctx context.Context, username string) (string, error)
}

// NewIntrospectionValidator wraps v. SCIM-based Subject resolution is not
// configured by default -- see WithSCIMResolver.
func NewIntrospectionValidator(v *introspect.Validator) *IntrospectionValidator {
	return &IntrospectionValidator{v: v}
}

// WithSCIMResolver configures iv to resolve Subject via scim (an immutable
// WSO2 SCIM user id) instead of introspect.Validator's own UserInfo-based
// fallback, whenever introspection alone doesn't carry a Subject. Returns
// iv for chaining at construction time (see internal/tenant.buildLegacyTenant).
//
// Optional and WSO2-specific: a tenant/IdP that never calls this keeps
// today's UserInfo-only enrichment unchanged -- SCIM is never required, and
// nothing here assumes every IdP has a SCIM2 endpoint to call.
//
// Exists specifically for IdPs whose access tokens are token-binding-bound
// to the browser (see internal/scim's own package doc comment for the full
// mechanics) -- UserInfo then rejects this bridge's bearer-only
// server-side call (no binding cookie to present), while a SCIM lookup
// authenticated with this bridge's own separate client-credentials grant is
// unaffected by that restriction entirely.
func (iv *IntrospectionValidator) WithSCIMResolver(scim scimResolver) *IntrospectionValidator {
	iv.scim = scim
	return iv
}

// Validate introspects token against this validator's pinned IdP instance
// (base validation, via introspect.Validator.ValidateBearer -- identical to
// what the legacy /support path itself gets), then enriches the result with
// a canonical Subject when introspection's own response didn't carry one:
// via iv.scim.ResolveUserID (username -> immutable SCIM user id) when a
// SCIM resolver is configured (see WithSCIMResolver), otherwise via
// introspect.Validator.ResolveSubjectViaUserinfo exactly as before. Either
// way, username is used ONLY as a lookup key -- the resolved value becomes
// Subject, username itself never does. Enrichment is attempted only when
// the token looks like a genuine end-user token (a non-empty Username -- a
// pure client-credentials token has none, see introspect.Identity's own
// doc comment, and has no Subject to resolve in the first place) and
// introspection's own Subject came back empty; a tenant whose IdP already
// returns "sub" from introspection (e.g. one using JWT-typed access
// tokens) never triggers either path.
//
// Returns an error -- failing this call, not just leaving Subject empty --
// when enrichment was attempted but didn't produce a usable Subject, since
// every /v1 caller that reaches this validator requires one (see
// internal/handler's canonicalOwner/RequireSubject). This keeps the
// "Subject is mandatory for /v1" decision inside the auth/validator layer,
// consistent with introspect.ErrUserinfoInsufficientScope/
// ErrUserinfoNoSubject/ErrUserinfoUnavailable's own classification, and
// with internal/scim.ErrNoMatch/ErrAmbiguous/ErrUnavailable's.
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

// ErrJWKSNotImplemented is returned by JWKSValidator.Validate -- the
// validationType "jwks" tenant config shape exists (see internal/tenant.
// Config's ValidationType/JWKSURI/Audience fields and the TENANT_REGISTRY
// row syntax) so a JWT-issuing tenant can be *configured* today, but actual
// JWT/JWKS verification is deferred follow-up work, not something to fake
// here -- see this package's own doc comment and the project's live-chat-
// SDK extraction plan (Stage 1, TokenValidator section) for why
// introspection-only ships first.
var ErrJWKSNotImplemented = errors.New("tokenvalidator: JWKS validation is not implemented yet")

// JWKSValidatorConfig configures a JWKSValidator -- accepted and stored now
// so a tenant row can already declare validationType "jwks" without a
// TENANT_REGISTRY parse error, even though Validate itself always fails
// until real JWT/JWKS verification lands.
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
// validate connectivity -- there is nothing to verify against yet.
func NewJWKSValidator(cfg JWKSValidatorConfig) *JWKSValidator {
	return &JWKSValidator{cfg: cfg}
}

// Validate always fails -- see ErrJWKSNotImplemented.
func (jv *JWKSValidator) Validate(context.Context, string) (*Identity, error) {
	return nil, ErrJWKSNotImplemented
}
