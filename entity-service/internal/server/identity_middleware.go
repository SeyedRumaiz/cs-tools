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

package server

import (
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// callerIdentityMiddleware resolves the caller's identity once per request
// (via the same AccessService.ResolveScope every scoped endpoint already
// calls) and attaches it to the request context, so repository.Scoped can
// read it without every service re-resolving it.
//
// Deliberately best-effort, never rejecting: this middleware sits in front
// of EVERY route, including ones that carry no user/client token at all
// (the GitHub webhook, health checks, the Salesforce inbound webhook, and
// any other HMAC/shared-secret-authenticated endpoint) -- auth.Middleware
// itself never rejects a tokenless request either (see its own doc
// comment), so a request with nothing to resolve is expected, not an
// error. ResolveScope failing here (unauthenticated, unknown user, wrong
// user type) is not this middleware's failure to report: the concrete
// handler for a route that actually needs a resolved identity already
// calls ResolveScope itself (directly, or through a service that does) and
// produces its own typed error for that endpoint. This middleware exists
// only to make that SAME resolution available to repository.Scoped, whose
// callers never call ResolveScope directly at all.
//
// When resolution fails or never applies, ctx is passed through unchanged
// -- no identity attached, not a blank/zero one. A protected repository
// method reached this way returns repository.ErrNoCallerIdentity rather
// than silently running unscoped or as an unintended internal caller; and
// underneath that, FORCE ROW LEVEL SECURITY on the table itself fails
// closed (zero rows) regardless of what Go does. A route whose handler
// never touches a Scoped-wrapped repository (every webhook and health
// check today) is entirely unaffected either way.
func callerIdentityMiddleware(access service.AccessService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if scope, err := access.ResolveScope(ctx); err == nil {
				ctx = repository.WithCallerIdentity(ctx, scope)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
