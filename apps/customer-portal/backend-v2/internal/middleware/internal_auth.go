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
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

// InternalTokenHeader carries the shared secret used to authenticate
// server-to-server calls between this backend and csm-portal/backend for
// the live-engineer-chat feature (see internal/csmchat and this backend's
// POST /internal/chat-events). Neither backend shares a JWT issuer/audience
// for a "service" identity, so this is a plain shared bearer secret rather
// than a token Auth (above, in this same package) could validate. Must
// match csm-portal/backend's own middleware.InternalTokenHeader value.
const InternalTokenHeader = "X-Internal-Chat-Token"

// InternalToken returns HTTP middleware that requires InternalTokenHeader to
// equal expected, using a constant-time comparison to avoid a timing side
// channel. An empty expected value always rejects — this must never be used
// to mean "no check configured"; leave the route unregistered instead if the
// feature is disabled.
//
// This is intentionally a distinct, narrower gate from Auth/AuthWithValidator
// above: it has no notion of a user identity, and must only ever guard
// routes meant for direct service-to-service calls, never anything a
// browser calls directly.
func InternalToken(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addSecurityHeaders(w)

			got := r.Header.Get(InternalTokenHeader)
			if expected == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(authErrorBody{Message: "You are not authorized to perform this action. Please try again."})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
