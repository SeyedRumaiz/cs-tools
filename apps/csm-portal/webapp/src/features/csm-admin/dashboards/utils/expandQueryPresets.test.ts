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

import { describe, expect, it } from "vitest";
import type { BeDashboardFilterPreset, BeDashboardWidget } from "@api/backend/types";
import {
  expandQueryPresets,
  withImpliedTypeFilter,
  toPreviewWidget,
} from "@features/csm-admin/dashboards/utils/expandQueryPresets";

const STATE_FILTER = {
  field: "state",
  op: "in",
  values: ["open", "work_in_progress"],
};
const presets: BeDashboardFilterPreset[] = [{ name: "activeCaseStates", filter: STATE_FILTER }];

describe("expandQueryPresets", () => {
  it("replaces a preset reference in the top-level filters with a copy of its fragment", () => {
    const query = {
      filters: [
        { field: "assignedUserId", op: "in", values: ["__current_user__"] },
        { preset: "activeCaseStates" },
      ],
    };

    const expanded = expandQueryPresets(query, presets);

    expect(expanded).toEqual({
      filters: [{ field: "assignedUserId", op: "in", values: ["__current_user__"] }, STATE_FILTER],
    });
    const entries = (expanded as { filters: unknown[] }).filters;
    expect(entries[1]).not.toBe(STATE_FILTER);
  });

  it("expands references inside each anyOf branch's filters", () => {
    const expanded = expandQueryPresets(
      {
        anyOf: [
          { filters: [{ preset: "activeCaseStates" }] },
          { filters: [{ field: "tag", op: "in", values: ["patch"] }] },
        ],
      },
      presets,
    );

    expect(expanded).toEqual({
      anyOf: [
        { filters: [STATE_FILTER] },
        { filters: [{ field: "tag", op: "in", values: ["patch"] }] },
      ],
    });
  });

  it("never mutates its input", () => {
    const query = { filters: [{ preset: "activeCaseStates" }] };
    expandQueryPresets(query, presets);
    expect(query).toEqual({ filters: [{ preset: "activeCaseStates" }] });
  });

  it("returns the same object when there is nothing to expand", () => {
    const query = { filters: [{ field: "tag", op: "in", values: ["patch"] }] };
    expect(expandQueryPresets(query, presets)).toBe(query);
    expect(expandQueryPresets(undefined, presets)).toBeUndefined();
  });

  it("leaves an unknown preset name in place instead of throwing", () => {
    const query = { filters: [{ preset: "deletedPreset" }] };
    expect(expandQueryPresets(query, presets)).toBe(query);
    expect(expandQueryPresets(query, undefined)).toBe(query);
  });

  it("leaves a malformed reference (extra keys or non-string name) in place", () => {
    const query = {
      filters: [{ preset: "activeCaseStates", field: "x" }, { preset: 5 }],
    };
    expect(expandQueryPresets(query, presets)).toBe(query);
  });
});

describe("withImpliedTypeFilter", () => {
  const TYPE = { field: "type", op: "in", values: ["case"] };

  it("appends the type predicate when the top-level filters have none", () => {
    const query = { filters: [{ field: "tag", op: "in", values: ["patch"] }] };
    expect(withImpliedTypeFilter(query, "case")).toEqual({
      filters: [{ field: "tag", op: "in", values: ["patch"] }, TYPE],
    });
    expect(query.filters).toHaveLength(1);
  });

  it("creates the query for a missing or null query, and replaces a non-array filters", () => {
    expect(withImpliedTypeFilter(undefined, "case")).toEqual({ filters: [TYPE] });
    expect(withImpliedTypeFilter(null, "case")).toEqual({ filters: [TYPE] });
    expect(withImpliedTypeFilter({ filters: "bad" }, "case")).toEqual({ filters: [TYPE] });
  });

  it("uses the widget's own resource type as the value", () => {
    expect(withImpliedTypeFilter({}, "engagement")).toEqual({
      filters: [{ field: "type", op: "in", values: ["engagement"] }],
    });
  });

  it("leaves a query with an explicit type predicate untouched, whatever its op or values", () => {
    const query = { filters: [{ field: "type", op: "notIn", values: ["announcement"] }] };
    expect(withImpliedTypeFilter(query, "case")).toBe(query);
  });

  it("does not count a type predicate inside an anyOf branch, and keeps the anyOf", () => {
    const query = { anyOf: [{ filters: [{ field: "type", op: "in", values: ["case"] }] }] };
    expect(withImpliedTypeFilter(query, "case")).toEqual({ ...query, filters: [TYPE] });
  });

  it("does nothing for resource types outside the case-search family", () => {
    const query = { assignedUserIds: ["x"] };
    expect(withImpliedTypeFilter(query, "call_request")).toBe(query);
    expect(withImpliedTypeFilter(undefined, "call_request")).toBeUndefined();
  });

  it("counts a type predicate that a preset expands to", () => {
    const withTypePreset: BeDashboardFilterPreset[] = [
      { name: "onlyCases", filter: { field: "type", op: "in", values: ["case"] } },
    ];
    const widget: BeDashboardWidget = {
      widgetId: "w",
      displayName: "W",
      resourceType: "case",
      shape: "count",
      gridWidth: 4,
      query: { filters: [{ preset: "onlyCases" }] },
    };
    expect(toPreviewWidget(widget, withTypePreset).query).toEqual({
      filters: [{ field: "type", op: "in", values: ["case"] }],
    });
  });
});

describe("toPreviewWidget", () => {
  const widget: BeDashboardWidget = {
    widgetId: "w1",
    displayName: "Mine",
    resourceType: "case",
    shape: "pie",
    gridWidth: 4,
    query: { filters: [{ preset: "activeCaseStates" }] },
    slices: [
      { label: "A", query: { filters: [{ preset: "activeCaseStates" }] } },
      { label: "B", query: null },
    ],
  };

  it("expands presets and adds the implied type filter on the widget query and every slice query, leaving the original untouched", () => {
    const expanded = toPreviewWidget(widget, presets);

    const type = { field: "type", op: "in", values: ["case"] };
    expect(expanded.query).toEqual({ filters: [STATE_FILTER, type] });
    expect(expanded.slices).toEqual([
      { label: "A", query: { filters: [STATE_FILTER, type] } },
      { label: "B", query: { filters: [type] } },
    ]);
    expect(widget.query).toEqual({ filters: [{ preset: "activeCaseStates" }] });
  });
});
