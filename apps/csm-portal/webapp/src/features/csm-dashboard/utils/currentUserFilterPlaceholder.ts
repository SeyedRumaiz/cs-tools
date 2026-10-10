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

import { hasFieldFilterEntries, isFieldFilterEntry } from "./widgetPreviewUrl";

/**
 * Placeholder value a dashboard widget's own filters may carry wherever a
 * per-user value belongs — e.g. `assignedUserId`/`assignedUserIds` — meaning
 * "the signed-in user's own id". Until 2026-08-06 this was resolved
 * server-side (`CurrentUserPlaceholder`/`substituteCurrentUser` in the
 * backend's `internal/dashboard` package, via a `GET /users/me` round trip
 * baked into `GET /dashboards/{id}`); `GET /dashboards/{id}` now returns
 * `Query`/`Slices` verbatim, so this frontend resolves it itself, at the
 * same point (and by the same generic value-substitution approach — the
 * backend's own `substituteCurrentUser` walked the filters object by value,
 * not by a hardcoded field name) `__current_team__` is already resolved
 * client-side (see `teamFilterPlaceholder.ts`).
 */
export const CURRENT_USER_PLACEHOLDER = "__current_user__";

/**
 * Substitutes {@link CURRENT_USER_PLACEHOLDER} wherever it appears in a
 * dashboard widget's filters with the signed-in user's own platform id
 * (`useCurrentUser().user.id`) — handling both filter shapes this app's
 * widgets use (same two shapes `resolveTeamPlaceholder`/
 * `resolveCurrentUserSentinels` already branch on):
 *
 * - the case-search generic field/op/values DSL (`{ filters:
 *   BeCaseFieldFilter[] }`, e.g. `{ field: "assignedUserId", op: "in",
 *   values: ["__current_user__"] }`)
 * - every other resourceType's flat `{ fieldName: string[] | string }`
 *   record (e.g. `{ assignedUserIds: ["__current_user__"] }`)
 *
 * Unlike the case-search DSL's `field`-scoped `resolveTeamPlaceholder` (which
 * only ever looks at `creTeam`/`sreTeam`), this walks every field generically
 * — mirroring the backend's own now-removed `substituteCurrentUser`, which
 * substituted the placeholder by VALUE wherever it appeared, with no
 * hardcoded field name — since a widget can put "the signed-in user" in any
 * field a resourceType's search supports (`assignedUserId` for case,
 * `assignedUserIds` for another resourceType's flat filters, and so on), not
 * only the one field name this app's widget configs happen to use today.
 *
 * When `currentUserId` is undefined (the signed-in user's profile hasn't
 * loaded yet — `CurrentUserProvider` does NOT gate its children on that
 * fetch, so every widget's first render happens while `/users/me` is still
 * in flight), the filters are returned UNCHANGED, placeholder and all.
 *
 * This is deliberately not the fail-open drop `resolveTeamPlaceholder` uses.
 * Dropping the condition widens the query: a widget whose only filter is
 * `assignedUserId in ["__current_user__"]` would, for the width of that
 * fetch, issue a completely unfiltered search and paint every engineer's
 * cases into a tile labelled as the viewer's own. Widening a user-scoped
 * filter is the one failure mode this resolver must not have, so it fails
 * closed instead — and returning the input untouched also preserves the
 * literal values of a mixed `["__current_user__", "<some uuid>"]` filter,
 * which the old per-entry drop discarded along with the placeholder.
 *
 * Leaving the placeholder in is a backstop, not the intended path: callers
 * must not send it. {@link hasCurrentUserPlaceholder} lets a caller detect
 * this state and defer the request until the profile lands — which is what
 * `useWidgetData`/`useWidgetPieData` do, so nothing is issued in the
 * meantime and the tile stays in its loading state. `widgetPreviewUrl.ts`'s
 * `resolveCurrentUserSentinels` already follows this same "return unchanged,
 * caller holds off" convention for its own `@me` sentinel. Should a request
 * still go out (a caller that skipped the check), the backend rejects the
 * non-UUID value with a 400 and the tile shows its error state — visibly
 * broken, but never broader than the viewer is entitled to see.
 */
export function resolveCurrentUserPlaceholder(
  filters: Record<string, unknown>,
  currentUserId: string | undefined,
): Record<string, unknown> {
  if (currentUserId === undefined) return filters;
  const fieldFilters = filters.filters;
  if (hasFieldFilterEntries(fieldFilters)) {
    return resolveCaseFieldFilters(filters, fieldFilters, currentUserId);
  }
  return resolveFlatFilters(filters, currentUserId);
}

/**
 * Reports whether `filters` still carries {@link CURRENT_USER_PLACEHOLDER}
 * anywhere — in either of the two filter shapes above. True means the filters
 * cannot be sent as they stand: the caller must hold the request until
 * `useCurrentUser().user.id` resolves (see the fail-closed note on
 * {@link resolveCurrentUserPlaceholder}). Cheap enough to call on every
 * render; these objects are a handful of entries deep.
 */
export function hasCurrentUserPlaceholder(filters: Record<string, unknown>): boolean {
  const fieldFilters = filters.filters;
  if (hasFieldFilterEntries(fieldFilters)) {
    return fieldFilters.some(
      (entry) =>
        isFieldFilterEntry(entry) && (entry.values?.includes(CURRENT_USER_PLACEHOLDER) ?? false),
    );
  }
  return Object.values(filters).some((value) => {
    if (value === CURRENT_USER_PLACEHOLDER) return true;
    return Array.isArray(value) && value.includes(CURRENT_USER_PLACEHOLDER);
  });
}

function resolveCaseFieldFilters(
  filters: Record<string, unknown>,
  fieldFilters: unknown[],
  currentUserId: string,
): Record<string, unknown> {
  let changed = false;
  const resolved = fieldFilters.map((entry) => {
    if (!isFieldFilterEntry(entry)) return entry;
    const values = entry.values;
    if (!values?.includes(CURRENT_USER_PLACEHOLDER)) return entry;
    changed = true;
    return {
      ...entry,
      values: values.map((v) => (v === CURRENT_USER_PLACEHOLDER ? currentUserId : v)),
    };
  });

  if (!changed) return filters;
  return { ...filters, filters: resolved };
}

function resolveFlatFilters(
  filters: Record<string, unknown>,
  currentUserId: string,
): Record<string, unknown> {
  let changed = false;
  const resolved: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(filters)) {
    if (Array.isArray(value) && value.every((v) => typeof v === "string")) {
      const strings = value as string[];
      if (!strings.includes(CURRENT_USER_PLACEHOLDER)) {
        resolved[key] = value;
        continue;
      }
      changed = true;
      resolved[key] = strings.map((v) => (v === CURRENT_USER_PLACEHOLDER ? currentUserId : v));
      continue;
    }
    if (value === CURRENT_USER_PLACEHOLDER) {
      changed = true;
      resolved[key] = currentUserId;
      continue;
    }
    resolved[key] = value;
  }

  if (!changed) return filters;
  return resolved;
}
