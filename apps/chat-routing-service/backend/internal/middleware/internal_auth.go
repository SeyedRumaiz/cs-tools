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

// InternalTokenHeader carries the shared secret csm-portal/backend
// authenticates with when calling this service (see that backend's
// internal/routingclient package). This service has no user-facing routes
// at all — every route is server-to-server, gated by this single check —
// so, unlike csm-portal/backend's own InternalToken (which coexists with a
// separate user-JWT Auth middleware on other routes), there is no second
// auth mechanism in this codebase to keep this one distinct from.
const InternalTokenHeader = "X-Routing-Service-Token"

// authErrorBody is the JSON error payload for an auth failure.
type authErrorBody struct {
	Message string `json:"message"`
}

// InternalToken returns HTTP middleware that requires InternalTokenHeader to
// equal expected, using a constant-time comparison to avoid a timing side
// channel. An empty expected value always rejects — this must never be read
// as "no check configured"; the caller (main.go) exits at startup instead if
// ROUTING_SERVICE_TOKEN is unset, so expected is never actually empty in
// practice.
func InternalToken(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
