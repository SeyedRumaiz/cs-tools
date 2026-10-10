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

package repository

import "context"

// viewerProjectHint returns an extra " AND <alias>.project_id = ANY(...)"
// predicate for an external (non-Unrestricted) caller, or "" for an internal
// one.
//
// This is a planner hint, NOT an authorization check. Row-level security is
// still what decides visibility; this predicate is redundant with
// work_item's policy (is_project_member reads the same app.viewer_project_ids
// array), so it can never widen what a caller sees. Its only purpose is
// performance: RLS puts "internal OR project_id = ANY(...)" in an OR, which
// Postgres cannot turn into an index scan, so an external caller's list and
// count queries walked and filtered the whole work_item table (~400K rows).
// The same array condition sitting at the top level of the WHERE clause is
// indexable on work_item.project_id, which cut the customer case list and
// global search from ~200-450ms to ~10-60ms on a real-data copy.
//
// Fails closed in both directions: if the setting were ever empty, NULLIF
// makes the predicate NULL and the query returns zero rows, never extra rows;
// and if a caller forgets to add it, RLS still enforces visibility, the query
// is only slower. It must NOT be added for Unrestricted callers, who get '{}'
// in that setting and would match nothing.
func viewerProjectHint(alias string, scope SearchScope) string {
	if scope.Unrestricted {
		return ""
	}
	return " AND " + alias + ".project_id = ANY(NULLIF(current_setting('app.viewer_project_ids', true), '')::uuid[])"
}

// viewerProjectHintFor is viewerProjectHint for repositories that only have
// the request context. A ctx with no identity yields the zero scope, which is
// not Unrestricted, so the hint is added: it can only narrow, never widen, and
// Scoped refuses such a ctx anyway.
func viewerProjectHintFor(ctx context.Context, alias string) string {
	scope, _ := CallerIdentityFromContext(ctx)
	return viewerProjectHint(alias, scope)
}
