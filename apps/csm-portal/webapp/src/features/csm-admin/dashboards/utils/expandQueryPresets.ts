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

import type {
  BeDashboardFilterPreset,
  BeDashboardWidget,
  BeWidgetResourceType,
} from "@api/backend/types";

import { usesCaseFieldFilterDsl } from "./widgetQueryConditions";

type Query = Record<string, unknown>;

/**
 * Expands `{ "preset": "<name>" }` filter references in a widget query into
 * copies of the catalogue's literal fragments, for the builder's Preview only.
 *
 * The backend does this when it loads a dashboard, so the live view never sees
 * a reference. The editor draft deliberately keeps the authored references (so
 * an export does not freeze a shared preset's current body), which means the
 * Preview tile would otherwise send them to `/cases/search` unexpanded. Never
 * feed the result to `onSave`: it is a throwaway copy for the Preview request.
 *
 * Mirrors the backend's `resolveQueryPresets`: references are looked for in
 * the top-level `filters` array and in each `anyOf` branch's `filters`, and a
 * reference is exactly `{ preset: "<name>" }`. Unlike the backend, this never
 * throws: a malformed reference, or a name missing from `presets` (catalogue
 * still loading, or the preset was deleted), is left in place untouched. The
 * request then fails visibly in the Preview tile rather than blanking the
 * dialog. `query` is never mutated.
 */
export function expandQueryPresets<Q extends Query | null | undefined>(
  query: Q,
  presets: readonly BeDashboardFilterPreset[] | undefined,
): Q {
  if (!query) return query;
  const byName = new Map((presets ?? []).map((p) => [p.name, p.filter]));
  let changed = false;

  const expandList = (list: unknown): unknown => {
    if (!Array.isArray(list)) return list;
    let listChanged = false;
    const out = list.map((entry) => {
      const fragment = presetFragment(entry, byName);
      if (!fragment) return entry;
      listChanged = true;
      return { ...fragment };
    });
    if (!listChanged) return list;
    changed = true;
    return out;
  };

  const next: Query = { ...query };
  if ("filters" in query) next.filters = expandList(query.filters);
  if (Array.isArray(query.anyOf)) {
    next.anyOf = query.anyOf.map((branch) => {
      if (branch === null || typeof branch !== "object" || Array.isArray(branch)) return branch;
      const b = branch as Query;
      const filters = expandList(b.filters);
      return filters === b.filters ? branch : { ...b, filters };
    });
  }
  return changed ? (next as Q) : query;
}

function presetFragment(
  entry: unknown,
  byName: ReadonlyMap<string, Record<string, unknown>>,
): Record<string, unknown> | undefined {
  if (entry === null || typeof entry !== "object" || Array.isArray(entry)) return undefined;
  const ref = entry as Record<string, unknown>;
  if (Object.keys(ref).length !== 1 || typeof ref.preset !== "string") return undefined;
  return byName.get(ref.preset);
}

/**
 * Mirrors the backend's `injectTypeFilter`: guarantees `query.filters`
 * carries a `type` predicate, appending `{ field: "type", op: "in", values:
 * [resourceType] }` when none exists. Only the top-level `filters` array is
 * checked (an `anyOf` branch's own `type` does not count, as on the
 * backend), any entry with `field === "type"` counts whatever its op, and a
 * missing query or a non-array `filters` is replaced, as on the backend.
 * Run it AFTER preset expansion: a preset may itself expand to a `type`
 * predicate.
 */
export function withImpliedTypeFilter(
  query: Query | null | undefined,
  resourceType: BeWidgetResourceType,
): Query | null | undefined {
  if (!usesCaseFieldFilterDsl(resourceType)) return query;
  const filters = Array.isArray(query?.filters) ? query.filters : [];
  const hasType = filters.some(
    (e) => e !== null && typeof e === "object" && (e as Query).field === "type",
  );
  if (hasType) return query;
  return {
    ...query,
    filters: [...filters, { field: "type", op: "in", values: [resourceType] }],
  };
}

/**
 * Builds the widget the Preview tile runs: presets expanded
 * ({@link expandQueryPresets}) and the implied `type` filter added
 * ({@link withImpliedTypeFilter}) on the widget query and, independently, on
 * every slice query (a slice's `filters` replaces the widget's wholesale),
 * so Preview sends what the live dashboard sends. Preview only: never pass
 * the result to `onSave`.
 */
export function toPreviewWidget(
  widget: BeDashboardWidget,
  presets: readonly BeDashboardFilterPreset[] | undefined,
): BeDashboardWidget {
  const resolve = (q: Query | null | undefined) =>
    withImpliedTypeFilter(expandQueryPresets(q, presets), widget.resourceType) ?? null;
  return {
    ...widget,
    query: resolve(widget.query),
    slices: widget.slices?.map((s) => ({ ...s, query: resolve(s.query) })),
  };
}
