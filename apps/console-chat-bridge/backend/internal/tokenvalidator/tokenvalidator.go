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
type IntrospectionValidator struct {
	v *introspect.Validator
}

// NewIntrospectionValidator wraps v.
func NewIntrospectionValidator(v *introspect.Validator) *IntrospectionValidator {
	return &IntrospectionValidator{v: v}
}

// Validate introspects token against this validator's pinned IdP instance.
func (iv *IntrospectionValidator) Validate(ctx context.Context, token string) (*Identity, error) {
	id, err := iv.v.ValidateBearer(ctx, token)
	if err != nil {
		return nil, err
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
