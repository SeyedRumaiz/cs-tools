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
import type { BeDashboardWidget } from "@api/backend/types";
import {
  denseWidgetGridSx,
  groupWidgetsBySection,
  isDenseSection,
} from "@features/csm-dashboard/utils/dashboardWidgetGridLayout";

function makeWidget(overrides: Partial<BeDashboardWidget> = {}): BeDashboardWidget {
  return {
    widgetId: "widget_1",
    displayName: "Widget",
    resourceType: "case",
    shape: "count",
    gridWidth: 4,
    query: {},
    ...overrides,
  } as BeDashboardWidget;
}

describe("isDenseSection", () => {
  it("is false for an empty widget list", () => {
    expect(isDenseSection([])).toBe(false);
  });

  it("is true when every widget in the list is shape 'count'", () => {
    expect(
      isDenseSection([
        makeWidget({ widgetId: "a" }),
        makeWidget({ widgetId: "b" }),
        makeWidget({ widgetId: "c" }),
      ]),
    ).toBe(true);
  });

  it("is false when at least one widget isn't shape 'count', regardless of position", () => {
    expect(
      isDenseSection([
        makeWidget({ widgetId: "a" }),
        makeWidget({ widgetId: "b", shape: "bar", slices: [] }),
      ]),
    ).toBe(false);
    expect(
      isDenseSection([
        makeWidget({ widgetId: "a", shape: "list" }),
        makeWidget({ widgetId: "b" }),
      ]),
    ).toBe(false);
  });

  it("is false for a single non-count widget", () => {
    expect(isDenseSection([makeWidget({ shape: "pie", slices: [] })])).toBe(false);
  });
});

describe("denseWidgetGridSx", () => {
  it("defaults to a 168px auto-fill minimum column width", () => {
    const sx = denseWidgetGridSx();
    expect(sx.display).toBe("grid");
    expect(sx.gridTemplateColumns).toBe("repeat(auto-fill, minmax(168px, 1fr))");
  });

  it("honors a custom minimum column width", () => {
    const sx = denseWidgetGridSx(200);
    expect(sx.gridTemplateColumns).toBe("repeat(auto-fill, minmax(200px, 1fr))");
  });
});

// `groupWidgetsBySection` itself is pre-existing (not part of this change),
// but exercised here alongside `isDenseSection` since a real caller
// (`DashboardWidgetGrid`) always evaluates the latter against the former's
// own output, one group at a time — this documents that exact composition.
describe("groupWidgetsBySection + isDenseSection composition", () => {
  it("classifies each of a multi-section dashboard's own groups independently", () => {
    const widgets = [
      makeWidget({ widgetId: "cre_a", section: "CRE" }),
      makeWidget({ widgetId: "cre_b", section: "CRE" }),
      makeWidget({ widgetId: "sre_a", section: "SRE", shape: "list" }),
      makeWidget({ widgetId: "sre_b", section: "SRE" }),
    ];
    const groups = groupWidgetsBySection(widgets);
    expect(groups).toHaveLength(2);

    const [creGroup, sreGroup] = groups;
    expect(creGroup.section).toBe("CRE");
    expect(isDenseSection(creGroup.widgets)).toBe(true);
    expect(sreGroup.section).toBe("SRE");
    expect(isDenseSection(sreGroup.widgets)).toBe(false);
  });
});
