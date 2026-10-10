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

package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
)

// authErrorBody is the JSON error payload for auth failures.
type authErrorBody struct {
	Message string `json:"message"`
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(authErrorBody{Message: message})
}

const jwtAssertionHeader = "x-jwt-assertion"

type contextKey string

const userInfoKey contextKey = "user-info"

// UserInfo holds the authenticated user's identity extracted from the JWT.
type UserInfo struct {
	Email  string
	UserID string
	// FirstName/LastName are the token's "given_name"/"family_name" claims.
	// Optional -- never required for a token to validate, unlike Email/UserID
	// below -- and so far only consumed by ensureUserProvisioned (see
	// handler/ensure_user.go), which needs a display name to provision a
	// worknote_creator-/escalator-only caller's entity-service "user" row.
	// Empty when the token doesn't carry either claim.
	FirstName string
	LastName  string
	// Roles is the token's "roles" claim, which portal authorisation checks
	// (see handler.AccessGuard).
	Roles []string
	// Groups is the token's "groups" claim — restored here for the /spl/*
	// (SupportPortalLite) routes only, which port the original Ballerina
	// backend's raw Asgardeo-group-based authorization model
	// (SPL_ALLOWED_GROUPS etc, see internal/splauth) rather than this app's
	// newer roles-based one. Deliberate, not a leftover from before the
	// roles migration — do not remove without checking internal/splauth's
	// callers first. If/when SPL's authorization moves onto the same
	// roles-based model as the rest of this app, this field (and the
	// "groups" claim decode below) can go.
	Groups []string
}

// Config holds JWT validation configuration.
type Config struct {
	JWKSEndpoint          string
	Issuer                string
	Audiences             []string
	ClockSkew             time.Duration
	TokenValidatorEnabled bool
}

// jwtClaims defines the expected JWT payload fields, mirroring the Ballerina
// CustomJwtPayload in the authorization module.
type jwtClaims struct {
	Email  string     `json:"email"`
	UserID string     `json:"userid"`
	Roles  stringList `json:"roles"`
	// Groups — see UserInfo.Groups's doc comment for why this is still read.
	Groups stringList `json:"groups"`
	// FirstName/LastName — see UserInfo.FirstName's own doc comment. Neither
	// is required: a token missing one or both still validates, same as
	// Groups above.
	FirstName string `json:"given_name"`
	LastName  string `json:"family_name"`
	jwt.RegisteredClaims
}

// m2mExemptRoutes lists the routes that skip Auth entirely because they are
// pure machine-to-machine calls with no end-user identity behind them --
// customer-portal/backend-v2's internal/csmchat.Client calling
// POST /internal/chat/escalate and POST /internal/chat/customer-message,
// and console-chat-bridge's own csmchat.Client calling
// POST /internal/chat/complete (ChatHandler.HandleCompleteByTenant). Neither
// receiving handler for any of these ever reads UserInfoFromContext or an
// entity.WithUserIDToken value, so there is no end-user identity these
// routes need Auth to establish.
//
// GET /internal/chat/cases/{caseId} (ChatHandler.HandleGetCaseOwnership,
// also called by console-chat-bridge) is exempted separately below, via
// m2mExemptPathPrefixes rather than this exact-match map -- its path
// carries a caller-supplied caseId segment, so there is no single literal
// string to match against r.URL.Path the way every other exempt route's
// fixed path allows.
//
// These are trusted the same way integrations/csm-integration-service
// trusts its own M2M callers: entirely at Choreo's API Manager gateway
// (subscription + client-credentials app auth), not validated again here.
// See that service's CLAUDE.md ("Why no Auth middleware") for the
// established precedent this follows, and openapi.yaml's
// oauth2ClientCredentials securityScheme on these paths for the
// corresponding API contract change. This intentionally does NOT reuse the
// browser-facing x-jwt-assertion/email/userid check: this repo already has
// a dedicated pattern for pure M2M routes, and requiring an IdP to emit
// synthetic end-user claims on a client-credentials token merely to satisfy
// that check would be inventing a second, weaker way to do the same thing.
var m2mExemptRoutes = map[string]bool{
	http.MethodPost + " /internal/chat/escalate":         true,
	http.MethodPost + " /internal/chat/customer-message": true,
	http.MethodPost + " /internal/chat/complete":         true,
	http.MethodGet + " /internal/chat/open-chat":         true,
}

// m2mExemptPathPrefixes lists method+path-prefix pairs that skip Auth the
// same way m2mExemptRoutes does, for a route whose path carries a variable
// segment (so no single literal string belongs in that exact-match map).
// Currently just GET /internal/chat/cases/{caseId} -- see
// m2mExemptRoutes's own doc comment.
var m2mExemptPathPrefixes = []string{
	http.MethodGet + " /internal/chat/cases/",
}

// isM2MExempt reports whether r's method+path matches m2mExemptRoutes
// exactly or falls under one of m2mExemptPathPrefixes.
func isM2MExempt(r *http.Request) bool {
	// path.Clean before matching -- this runs outermost, ahead of net/http.
	// ServeMux's own path-cleaning redirect (Mux sits behind Auth in the
	// chain: SecurityHeaders -> CorrelationID -> Auth -> Logger -> Mux), so
	// comparing against the raw, unnormalized r.URL.Path let a dirty path
	// like "/internal/chat/cases/../../../users/me" satisfy
	// m2mExemptPathPrefixes' literal string prefix even though it
	// canonicalizes to an unrelated route ("/users/me") -- confirmed via a
	// live probe: the exempt check passed on the dirty path, and only the
	// ServeMux redirect this Auth bypass exposed the request to caught it
	// before anything unauthorized actually ran. Cleaning first makes this
	// check match on the same path ServeMux will ultimately route on.
	cleanPath := path.Clean(r.URL.Path)
	key := r.Method + " " + cleanPath
	if m2mExemptRoutes[key] {
		return true
	}
	for _, prefix := range m2mExemptPathPrefixes {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			return true
		}
	}
	return false
}

// bearerAuthPrefix is the standard "Authorization: Bearer <token>" scheme
// prefix (RFC 6750), matched case-insensitively.
const bearerAuthPrefix = "Bearer "

// bearerTokenFromRequest extracts the raw token from a standard
// "Authorization: Bearer <token>" header. Returns "" if the header is
// missing, uses a different scheme, or carries an empty/whitespace-only
// token -- any of which leaves the caller unauthenticated exactly as if no
// fallback existed at all.
func bearerTokenFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(bearerAuthPrefix) || !strings.EqualFold(auth[:len(bearerAuthPrefix)], bearerAuthPrefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(bearerAuthPrefix):])
}

// stringList decodes a claim that Asgardeo emits as a bare string when it holds
// one value and as an array when it holds several (its "roles" claim does
// this). A plain []string would reject a single-role user's whole token, so
// both shapes are accepted; anything else fails the token.
//
// SCIM's own "roles" attribute was assumed to follow this same convention but
// does not -- observed in practice as an array of {value, ...} objects, a
// different-enough shape (see scim.scimRoles) that it isn't reused here.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one == "" {
			*l = nil
		} else {
			*l = []string{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("claim must be a string or an array of strings: %w", err)
	}
	*l = many
	return nil
}

// Auth returns an HTTP middleware that validates the x-jwt-assertion header on
// every request and stores the resulting UserInfo in the request context.
// When Config.TokenValidatorEnabled is false the token is only decoded without
// signature verification — safe for local development only. Requests to
// m2mExemptRoutes skip validation entirely — see that map's doc comment.
//
// Local-development-only Authorization fallback: in every deployed
// environment, Choreo's API Manager gateway sits in front of this backend
// and is what injects x-jwt-assertion after validating the caller's real
// access token -- there is no gateway between a browser and a backend
// running directly on a developer's machine. When TokenValidatorEnabled is
// false and x-jwt-assertion is absent, the (still-unverified,
// still-claim-checked) token is instead read from a standard
// "Authorization: Bearer <token>" header, matching what the webapp's
// useBackendApi() actually sends. x-jwt-assertion always wins when present,
// and when TokenValidatorEnabled is true (production) no fallback is
// attempted at all -- the gateway trust boundary and signature/issuer/
// audience verification are both left completely intact.
func Auth(cfg Config) func(http.Handler) http.Handler {
	var keyFunc jwt.Keyfunc
	if cfg.TokenValidatorEnabled {
		client := &http.Client{Transport: &x5cStrippingTransport{base: http.DefaultTransport}}
		jwks, err := keyfunc.NewDefaultOverrideCtx(context.Background(), []string{cfg.JWKSEndpoint}, keyfunc.Override{Client: client})
		if err != nil {
			// Misconfigured auth must not silently pass — fail at startup.
			panic("auth: failed to initialise JWKS from " + cfg.JWKSEndpoint + ": " + err.Error())
		}
		keyFunc = jwks.Keyfunc
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addSecurityHeaders(w)

			// Skip auth for both health check endpoints.
			if r.Method == http.MethodGet && (r.URL.Path == "/health" || r.URL.Path == "/health/dependencies") {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for the M2M-only internal chat routes -- see
			// m2mExemptRoutes's doc comment.
			if isM2MExempt(r) {
				next.ServeHTTP(w, r)
				return
			}

			tokenStr := r.Header.Get(jwtAssertionHeader)
			if tokenStr == "" && !cfg.TokenValidatorEnabled {
				tokenStr = bearerTokenFromRequest(r)
			}
			if tokenStr == "" {
				writeAuthError(w, "You are not authorized to perform this action. Please try again.")
				return
			}

			info, err := extractUserInfo(tokenStr, cfg, keyFunc)
			if err != nil {
				slog.ErrorContext(r.Context(), "auth: token validation failed", "err", err)
				writeAuthError(w, "You are not authorized to perform this action. Please try again.")
				return
			}

			ctx := context.WithValue(r.Context(), userInfoKey, info)
			userIDToken := r.Header.Get("x-user-id-token")
			if userIDToken == "" {
				userIDToken = tokenStr
			}
			ctx = entity.WithUserIDToken(ctx, userIDToken)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// x5cStrippingTransport removes the "x5c" certificate chain from every key in
// a JWKS response before it reaches the jwkset parser. Verification only
// needs "n"/"e" (or the EC/OKP equivalents); jwkset unconditionally parses
// "x5c" as X.509 certificates, and some IdPs (e.g. Asgardeo) publish certs
// with a negative serial number that Go's x509 parser rejects since Go 1.23,
// which would otherwise make the whole JWK Set fail to load.
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

// UserInfoFromContext retrieves the authenticated user's info from the context.
// Returns nil if the auth middleware was not applied.
func UserInfoFromContext(ctx context.Context) *UserInfo {
	v, _ := ctx.Value(userInfoKey).(*UserInfo)
	return v
}

// WithUserInfo returns a copy of ctx carrying the given UserInfo.
// Call this in tests to bypass JWT parsing and inject a fake authenticated user.
func WithUserInfo(ctx context.Context, user *UserInfo) context.Context {
	return context.WithValue(ctx, userInfoKey, user)
}

func extractUserInfo(tokenStr string, cfg Config, keyFunc jwt.Keyfunc) (*UserInfo, error) {
	var c jwtClaims

	if !cfg.TokenValidatorEnabled {
		// Local mode: decode without signature verification.
		_, _, err := new(jwt.Parser).ParseUnverified(tokenStr, &c)
		if err != nil {
			return nil, fmt.Errorf("decode token: %w", err)
		}
	} else {
		token, err := jwt.ParseWithClaims(tokenStr, &c, keyFunc,
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithLeeway(cfg.ClockSkew),
			jwt.WithExpirationRequired(),
		)
		if err != nil {
			return nil, fmt.Errorf("validate token: %w", err)
		}
		if !token.Valid {
			return nil, fmt.Errorf("invalid token")
		}
		if len(cfg.Audiences) > 0 {
			tokenAuds, _ := token.Claims.GetAudience()
			if !hasAnyAudience(tokenAuds, cfg.Audiences) {
				return nil, fmt.Errorf("token audience not accepted")
			}
		}
	}

	if c.Email == "" {
		return nil, fmt.Errorf("token missing email claim")
	}
	if c.UserID == "" {
		return nil, fmt.Errorf("token missing userid claim")
	}

	return &UserInfo{
		Email:     c.Email,
		UserID:    c.UserID,
		FirstName: c.FirstName,
		LastName:  c.LastName,
		Roles:     []string(c.Roles),
		Groups:    []string(c.Groups),
	}, nil
}

func hasAnyAudience(tokenAuds jwt.ClaimStrings, expected []string) bool {
	for _, want := range expected {
		for _, got := range tokenAuds {
			if got == want {
				return true
			}
		}
	}
	return false
}

// addSecurityHeaders mirrors the Ballerina ResponseInterceptor security headers.
func addSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "upgrade-insecure-requests")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
}
