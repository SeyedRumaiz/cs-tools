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

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  DASHBOARD_SELECTION_STORAGE_KEY,
  readStoredDashboardSelection,
  writeStoredDashboardSelection,
} from "@features/csm-dashboard/utils/dashboardSelectionStorage";

beforeEach(() => window.localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("dashboardSelectionStorage", () => {
  it("returns null when nothing is stored", () => {
    expect(readStoredDashboardSelection()).toBeNull();
  });

  it("round-trips a dashboard with a team", () => {
    writeStoredDashboardSelection({ dashboardId: "abt", teamId: "team-a" });
    expect(readStoredDashboardSelection()).toEqual({
      dashboardId: "abt",
      teamId: "team-a",
    });
  });

  it("round-trips a dashboard without a team", () => {
    writeStoredDashboardSelection({ dashboardId: "ops", teamId: undefined });
    expect(readStoredDashboardSelection()).toEqual({
      dashboardId: "ops",
      teamId: undefined,
    });
  });

  it.each([
    ["malformed JSON", "{not json"],
    ["a non-object", "42"],
    ["null", "null"],
    ["a missing dashboardId", JSON.stringify({ teamId: "t" })],
    ["an empty dashboardId", JSON.stringify({ dashboardId: "" })],
    ["a non-string dashboardId", JSON.stringify({ dashboardId: 7 })],
  ])("returns null for %s", (_label, raw) => {
    window.localStorage.setItem(DASHBOARD_SELECTION_STORAGE_KEY, raw);
    expect(readStoredDashboardSelection()).toBeNull();
  });

  it("drops a non-string team id but keeps the dashboard", () => {
    window.localStorage.setItem(
      DASHBOARD_SELECTION_STORAGE_KEY,
      JSON.stringify({ dashboardId: "abt", teamId: 3 }),
    );
    expect(readStoredDashboardSelection()).toEqual({
      dashboardId: "abt",
      teamId: undefined,
    });
  });

  it("does not throw when reading is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readStoredDashboardSelection()).toBeNull();
  });

  it("does not throw when writing is blocked", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() =>
      writeStoredDashboardSelection({ dashboardId: "abt" }),
    ).not.toThrow();
  });
});
