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

// corsAllowedHeaders: Authorization is what every caller sets; Content-Type
// is required for JSON bodies (not CORS-safelisted, so a POST preflight
// fails without it).
const corsAllowedHeaders = "Content-Type, Authorization"

const corsAllowedMethods = "GET, POST, OPTIONS"

// CORS returns middleware handling cross-origin browser requests. Must wrap
// Auth (run outside it), never the reverse. Fail-closed: an empty
// allow-list allows no cross-origin request through, rather than
// reflecting the Origin back.
//
// Tenant-aware: if tenant.FromContext finds a resolved Tenant (set by
// ResolveTenant, which must run before this), its own AllowedOrigins is
// checked instead of defaultAllowedOrigins, so each tenant gets its own
// origin allow-list. A legacy /support/chats request never resolves a
// tenant, so it always falls back to defaultAllowedOrigins
// (CORS_ALLOWED_ORIGINS).
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
