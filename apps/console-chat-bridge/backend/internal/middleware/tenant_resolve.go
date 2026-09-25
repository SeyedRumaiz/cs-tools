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
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

// v1PathPrefix is every generic tenant-scoped route's shared prefix --
// "/v1/{tenant}/...". Anything outside this prefix (the legacy
// /support/chats path, /internal/chat-events, /health) is untouched by
// ResolveTenant -- it only ever acts within /v1.
const v1PathPrefix = "/v1/"

// ResolveTenant must be the OUTERMOST middleware wrapping every /v1/... route
// -- even outside CORS -- so a request for an unknown tenant 404s before
// CORS or Auth ever run, including for a bare OPTIONS preflight (a
// preflight for a tenant that doesn't exist has nothing valid to answer
// about; letting it through to CORS would mean reflecting/declining an
// Origin on behalf of a tenant this bridge doesn't recognize at all).
//
// Parses the path segment immediately after "/v1/" as the tenant slug,
// looks it up in table, and on a match stores the resolved *tenant.Tenant
// in the request context (see tenant.WithTenant) before calling next --
// CORS (tenant-aware) and the /v1 Auth middleware both read it from there.
// A request outside "/v1/" is passed through untouched; it never resolves
// a tenant, so CORS falls back to its own default allow-list and the
// legacy Auth middleware is used instead (see cmd/server/main.go's route
// wiring).
func ResolveTenant(table tenant.Table) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, v1PathPrefix) {
				next.ServeHTTP(w, r)
				return
			}

			rest := strings.TrimPrefix(r.URL.Path, v1PathPrefix)
			slug, _, _ := strings.Cut(rest, "/")
			if slug == "" {
				writeTenantError(w, http.StatusNotFound, "Unknown tenant.")
				return
			}

			t, ok := table.Lookup(slug)
			if !ok {
				writeTenantError(w, http.StatusNotFound, "Unknown tenant.")
				return
			}

			ctx := tenant.WithTenant(r.Context(), t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeTenantError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
