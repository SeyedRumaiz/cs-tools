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
	"net/http"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

// corsAllowedHeaders: Authorization is what AsgardeoSPAClient actually sets
// on every request (see copilot-api.ts / this bridge's own README);
// Content-Type is required for JSON bodies (not CORS-safelisted, so a POST
// preflight fails without it).
const corsAllowedHeaders = "Content-Type, Authorization"

const corsAllowedMethods = "GET, POST, OPTIONS"

// CORS returns an HTTP middleware handling cross-origin browser requests --
// copied from csm-portal/backend's identically-named middleware (see that
// file's own doc comment for the full MUST-wrap-Auth-not-be-wrapped-by-it
// rationale, which applies here unchanged). Fail-closed: an empty allow-list
// allows no cross-origin browser request through at all, rather than
// reflecting any Origin back -- this bridge authenticates via a
// caller-supplied Authorization bearer header, never cookies, so there is
// no ambient credential for a browser to attach automatically, but stays
// fail-closed anyway as defense-in-depth (see the original's own note on
// why this is safer than defaulting open even so).
//
// Tenant-aware: on every request, this checks tenant.FromContext first --
// set by the tenant-resolution middleware, which must run OUTSIDE (before)
// this one for a /v1/{tenant}/... request, even for a bare OPTIONS
// preflight (see that middleware's own doc comment on why an unknown
// tenant 404s before CORS ever runs). When a Tenant is present, its own
// Config.AllowedOrigins is checked instead of defaultAllowedOrigins -- so
// each tenant gets its own origin allow-list rather than one global union
// (a global union would let tenant A's configured browser origin send
// authenticated cross-origin requests against tenant B's data, which the
// browser's own same-origin policy would otherwise have prevented). A
// legacy /support/chats request never resolves a tenant at all, so it
// always falls through to defaultAllowedOrigins -- CORS_ALLOWED_ORIGINS,
// unchanged from before multi-tenant support existed.
func CORS(defaultAllowedOrigins []string) func(http.Handler) http.Handler {
	defaultAllowed := make(map[string]bool, len(defaultAllowedOrigins))
	for _, o := range defaultAllowedOrigins {
		defaultAllowed[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed := defaultAllowed
			if t, ok := tenant.FromContext(r.Context()); ok {
				allowed = make(map[string]bool, len(t.AllowedOrigins))
				for _, o := range t.AllowedOrigins {
					allowed[o] = true
				}
			}

			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
				if allowed[origin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
				w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
				w.Header().Set("Access-Control-Max-Age", "3600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
