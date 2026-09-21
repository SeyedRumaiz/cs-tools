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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"
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

// jwtClaims defines the expected JWT payload fields carried in the
// customer portal's x-jwt-assertion token: email, userid, and groups.
type jwtClaims struct {
	Email  string   `json:"email"`
	UserID string   `json:"userid"`
	Groups []string `json:"groups"`
	jwt.RegisteredClaims
}

// TokenValidator validates the customer portal's JWTs and extracts the identity
// they carry. It owns the JWKS client, so build one at startup and share it
// rather than constructing several: both the Auth middleware (which reads the
// x-jwt-assertion header) and the WebSocket listener (which cannot use a
// header at all — see handler.WebSocketHandler) validate through the same
// instance, so the JWKS is fetched and refreshed once per process.
type TokenValidator struct {
	cfg     Config
	keyFunc jwt.Keyfunc
}

// NewTokenValidator builds a TokenValidator from cfg. When
// Config.TokenValidatorEnabled is false the token is only decoded without
// signature verification — safe for local development only.
func NewTokenValidator(cfg Config) *TokenValidator {
	var keyFunc jwt.Keyfunc
	if cfg.TokenValidatorEnabled {
		jwks, err := keyfunc.NewDefault([]string{cfg.JWKSEndpoint})
		if err != nil {
			// Misconfigured auth must not silently pass — fail at startup.
			panic("auth: failed to initialise JWKS from " + cfg.JWKSEndpoint + ": " + err.Error())
		}
		keyFunc = jwks.Keyfunc
	}
	return &TokenValidator{cfg: cfg, keyFunc: keyFunc}
}

// Validate parses and validates a raw JWT, returning the identity it carries.
func (v *TokenValidator) Validate(tokenStr string) (*UserInfo, error) {
	return extractUserInfo(tokenStr, v.cfg, v.keyFunc)
}

// DecodeUnverified extracts the identity from a JWT **without** verifying its
// signature, issuer, or audience.
//
// It exists for exactly one caller: the WebSocket upgrade path, which receives
// the browser's Asgardeo-issued **ID token**, not the Choreo-injected
// x-jwt-assertion that Validate is configured for. Those are two different
// tokens — different issuer (`https://api.asgardeo.io/t/<org>/oauth2/token`),
// different audience (the SPA's client ID plus `choreo:deployment:<env>`), and
// a different signing key — so Validate rejects the ID token every time,
// which surfaced as a 401 on every WebSocket connection.
//
// The trust boundary for that route is Choreo's API Manager gateway, which has
// already validated the caller's access token (the leading
// `choreo-oauth2-token, <accessToken>` subprotocol pair) before forwarding the
// handshake — the same model CLAUDE.md documents under "Why no Auth
// middleware". The ID token is therefore used to *identify* an
// already-authenticated caller, not to authenticate one. This mirrors the
// Ballerina backend's authorization:getUserInfoFromTokens, which likewise only
// calls jwt:decode.
//
// Do NOT use this for any route that is reachable without passing through the
// gateway: it will accept a forged, unsigned, or expired token.
func (v *TokenValidator) DecodeUnverified(tokenStr string) (*UserInfo, error) {
	return extractUserInfo(tokenStr, Config{TokenValidatorEnabled: false}, nil)
}

// Auth returns an HTTP middleware that validates the x-jwt-assertion header on
// every request and stores the resulting UserInfo in the request context.
// When Config.TokenValidatorEnabled is false the token is only decoded without
// signature verification — safe for local development only.
func Auth(cfg Config) func(http.Handler) http.Handler {
	return AuthWithValidator(NewTokenValidator(cfg))
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

// AuthWithValidator is Auth over an already-built TokenValidator, so a process
// that also authenticates elsewhere (the WebSocket listener) shares one JWKS
// client instead of opening a second.
func AuthWithValidator(v *TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addSecurityHeaders(w)

			// Skip auth for the health check endpoint.
			if r.Method == http.MethodGet && r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			tokenStr := r.Header.Get(jwtAssertionHeader)
			// Local-development-only fallback: in every deployed environment,
			// Choreo's API Manager gateway sits in front of this backend and is
			// what injects x-jwt-assertion after validating the caller's real
			// access token -- there is no gateway between a browser and a
			// backend running directly on a developer's machine. Without this,
			// every request from the webapp (which only ever sends
			// "Authorization: Bearer <idToken>", per useAuthApiClient.ts) 401s
			// against a local backend even for a fully signed-in user.
			//
			// This is intentionally gated on TokenValidatorEnabled, not on the
			// header alone: when TokenValidatorEnabled is true (production),
			// x-jwt-assertion is still required exactly as before, with no
			// fallback attempted at all -- the gateway trust boundary and
			// signature/issuer/audience verification are both left completely
			// intact. When it is false, the token was already only being
			// decoded without signature verification (see extractUserInfo), so
			// reading that same unverified token from a second, equally
			// browser-controlled header does not weaken anything -- it only
			// widens which header the (still-unverified, still
			// email/userid-claim-checked) token may arrive in for local testing.
			// x-jwt-assertion always wins when both are present.
			if tokenStr == "" && !v.cfg.TokenValidatorEnabled {
				tokenStr = bearerTokenFromRequest(r)
			}
			if tokenStr == "" {
				writeAuthError(w, "You are not authorized to perform this action. Please try again.")
				return
			}

			info, err := v.Validate(tokenStr)
			if err != nil {
				slog.ErrorContext(r.Context(), "auth: token validation failed", "err", summarizeAuthErr(err))
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
		Email:  c.Email,
		UserID: c.UserID,
		Groups: c.Groups,
	}, nil
}

// summarizeAuthErr returns a short, log-safe category for a token validation
// failure — never the raw error, which for some jwt/v5 error paths can embed
// parts of the offending token or claim values.
func summarizeAuthErr(err error) string {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return "token expired"
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return "token not valid yet"
	case errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return "token used before issued"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return "signature invalid"
	case errors.Is(err, jwt.ErrTokenMalformed):
		return "malformed token"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "invalid audience"
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return "invalid issuer"
	case errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return "required claim missing"
	default:
		return "token validation failed"
	}
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

// addSecurityHeaders sets the standard security response headers required on
// every response.
func addSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "upgrade-insecure-requests")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
}
