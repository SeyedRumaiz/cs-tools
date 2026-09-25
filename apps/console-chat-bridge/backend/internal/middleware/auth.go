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
	"log/slog"
	"net/http"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

type contextKey string

const identityKey contextKey = "identity"

// Auth returns middleware that introspects the request's bearer token
// against v's pinned known IS instance (see package introspect) and stores
// the resulting Identity in the request context. Unlike
// customer-portal/backend-v2's own Auth middleware, there is no
// "local dev, decode without verifying" fallback here -- introspection
// always calls the real instance, and for this POC that instance IS the
// known local one, so there is nothing to skip.
//
// Applied to both this bridge's browser-facing routes (where the caller
// must resolve to a real end-user identity, see RequireUser) and its one
// internal route, /internal/chat-events (where the caller is
// csm-portal/backend's own client-credentials token, see
// RequireClientID) -- introspection against the same pinned instance
// validates either kind of token; only the check applied *after* it
// differs.
func Auth(v *introspect.Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				writeUnauthorized(w, "Missing bearer token.")
				return
			}
			identity, err := v.ValidateBearer(r.Context(), token)
			if err != nil {
				// The response to the browser stays generic on purpose (RFC
				// 7662 introspection failures should never leak detail to
				// an untrusted caller), but that left this bridge's own
				// terminal with no way to tell "IS rejected our
				// introspection client's own credentials", "TLS trust
				// failure against a self-signed local IS" and "token
				// genuinely inactive/expired" apart -- log the real reason
				// here, server-side only.
				slog.ErrorContext(r.Context(), "auth: token introspection failed", "err", err)
				writeUnauthorized(w, "Your session could not be verified. Please sign in again.")
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TenantAuth is Auth's counterpart for the generic /v1/{tenant}/... API:
// instead of one pinned *introspect.Validator, it validates against
// whichever tenant ResolveTenant already resolved into the request context
// (see tenant.FromContext) -- must run after ResolveTenant, same ordering
// requirement Auth itself has none of (it only ever used the one validator
// it closed over). If no tenant is in context at all (ResolveTenant was
// skipped or misordered -- a bug, not a normal request outcome, since every
// /v1 route is always registered behind ResolveTenant), this fails closed
// with 401 rather than a nil-pointer panic.
func TenantAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t, ok := tenant.FromContext(r.Context())
			if !ok {
				slog.ErrorContext(r.Context(), "auth: TenantAuth ran with no tenant resolved in context -- ResolveTenant must run first")
				writeUnauthorized(w, "Your session could not be verified. Please sign in again.")
				return
			}

			token := bearerToken(r)
			if token == "" {
				writeUnauthorized(w, "Missing bearer token.")
				return
			}
			identity, err := t.Validator.Validate(r.Context(), token)
			if err != nil {
				// Same fail-closed, log-detail-server-side-only shape as
				// Auth above.
				slog.ErrorContext(r.Context(), "auth: tenant token validation failed", "tenant", t.Slug, "err", err)
				writeUnauthorized(w, "Your session could not be verified. Please sign in again.")
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, *identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUser rejects a request whose validated Identity has no end-user
// claim (Username/Subject both empty) -- used on the browser-facing routes
// so a client-credentials-only token (which introspects successfully but
// carries no user identity) cannot be used to open a chat as no one in
// particular. Must run after Auth.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := IdentityFromContext(r.Context())
		if id.Username == "" && id.Subject == "" {
			writeUnauthorized(w, "A user session is required for this action.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSubject rejects a request whose validated Identity has no Subject
// claim -- the /v1/{tenant}/... API's own, stricter counterpart to
// RequireUser. Every /v1 user route uses this instead of RequireUser: the
// generic API's canonicalOwner() (see internal/handler's v1 routes) trusts
// Subject only, never falling back to Username the way the legacy
// /support/chats path's ownerIdentity() does (see that function's own doc
// comment for why -- Subject is the one claim every TokenValidator kind
// normalizes consistently, Username is optional/IdP-dependent). A token
// that introspects successfully but carries no Subject (e.g. malformed, or
// a client-credentials-only token hitting a user-only route) is rejected
// here the same way RequireUser rejects a Subject-and-Username-both-empty
// identity. Must run after TenantAuth.
func RequireSubject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := IdentityFromContext(r.Context())
		if id.Subject == "" {
			writeUnauthorized(w, "A user session is required for this action.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireClientID rejects a request whose validated Identity's ClientID
// does not match expected -- used on /internal/chat-events so only
// csm-portal/backend's own registered M2M client can push events into this
// bridge, even though the token is introspected against the same pinned
// instance as every browser-facing call. Must run after Auth.
func RequireClientID(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := IdentityFromContext(r.Context())
			if expected == "" || id.ClientID != expected {
				writeUnauthorized(w, "Not authorized to call this endpoint.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

// IdentityFromContext retrieves the validated Identity Auth stored, or the
// zero value if Auth was not applied.
func IdentityFromContext(ctx context.Context) introspect.Identity {
	id, _ := ctx.Value(identityKey).(introspect.Identity)
	return id
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
