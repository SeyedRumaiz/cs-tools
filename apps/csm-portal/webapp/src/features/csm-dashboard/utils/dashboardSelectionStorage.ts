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

/**
 * Remembers the dashboard (and team, for a team-based dashboard) a user last
 * picked on the main dashboard page, in this browser only, so the next visit
 * to a bare `/dashboard` lands on the same one. Same idiom as the other
 * `csm.*` localStorage preferences: read once, best-effort, never throws.
 *
 * Stored values are only ever a hint. The caller must validate them against
 * the dashboards/teams that are currently available and fall back to its own
 * defaults when they no longer match.
 */

export const DASHBOARD_SELECTION_STORAGE_KEY = "csm.dashboard.selection";

export interface StoredDashboardSelection {
  dashboardId: string;
  /** Present only when the stored dashboard was team-based. */
  teamId?: string;
}

/** The last remembered selection, or `null` when nothing usable is stored
 * (empty, malformed, wrong shape, or storage unavailable). */
export function readStoredDashboardSelection(): StoredDashboardSelection | null {
  try {
    const raw = window.localStorage.getItem(DASHBOARD_SELECTION_STORAGE_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return null;
    const { dashboardId, teamId } = parsed as Record<string, unknown>;
    if (typeof dashboardId !== "string" || dashboardId === "") return null;
    return {
      dashboardId,
      teamId: typeof teamId === "string" && teamId !== "" ? teamId : undefined,
    };
  } catch {
    /* localStorage may be unavailable or hold malformed JSON: no stored hint */
    return null;
  }
}

/** Remember `selection`. Best effort: a full or blocked store is ignored. */
export function writeStoredDashboardSelection(
  selection: StoredDashboardSelection,
): void {
  try {
    window.localStorage.setItem(
      DASHBOARD_SELECTION_STORAGE_KEY,
      JSON.stringify(selection),
    );
  } catch {
    /* localStorage may be unavailable: the selection just isn't remembered */
  }
}
