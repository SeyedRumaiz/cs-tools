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

	csm "github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/plg/entityclient"
)

// ForwardCorrelationID carries the request's correlation ID across the hop into
// entity-service.
//
// WHY THIS EXISTS AT ALL. csm-portal's CorrelationID middleware puts the id in
// two places: its own context key, which the slog handler reads so every log
// record carries it, and csm's `entity` package key, which csm's entity client
// reads so its outgoing requests carry it. PLG's entity client is a different
// package with a key of its own, and nothing populated it — so
// entityclient.WithCorrelationID was dead code, the header was never set, and
// entity-service generated a fresh id for every PLG call.
//
// The effect was that a PLG request's id in the portal's logs and the id in
// entity-service's logs were unrelated values, and the two could not be joined.
// Worse, one user action usually makes two entity calls — the identity
// resolution and the query itself — so it produced two unrelated ids for what a
// reader would reasonably expect to be one traceable action.
//
// MOUNTED OUTSIDE ResolveIdentity, deliberately. That middleware makes an
// entity-service call of its own to resolve the caller, and it is the first
// thing to run on a PLG request. Wrapping it means that call is correlated too;
// wrapping the other way round would leave the single most frequent PLG request
// to entity-service as the one hop still untraceable.
//
// A request with no correlation ID passes through untouched rather than being
// given a fabricated one. csm-portal's own middleware has already generated one
// by this point, so an empty value here means the chain was assembled wrongly,
// and inventing an id would hide that rather than surface it.
func ForwardCorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := csm.CorrelationIDFromContext(r.Context())
		if id == "" {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(entityclient.WithCorrelationID(r.Context(), id)))
	})
}
