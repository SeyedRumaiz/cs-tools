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

export interface TopBannerItem {
  enabled: boolean;
  html: string;
  closeable: boolean;
  storageKey: string;
  /**
   * Optional ISO 8601 timestamp with an offset (e.g. "2026-10-08T09:00:00+05:30").
   * The banner is not rendered before this instant. Missing means no start gate;
   * an unparseable value is ignored (and a warning is logged when rendering).
   * If it is at or after expiresAt the banner never shows.
   */
  startsAt?: string;
  /**
   * Optional ISO 8601 timestamp with an offset (e.g. "2026-10-10T18:00:00+05:30").
   * From this instant on the banner is not rendered. Missing means never expires;
   * an unparseable value is ignored (and a warning is logged when rendering).
   */
  expiresAt?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function toBanner(value: unknown): TopBannerItem | null {
  if (!isRecord(value) || typeof value.html !== "string" || !value.html) {
    return null;
  }
  const banner: TopBannerItem = {
    enabled: value.enabled === true,
    closeable: value.closeable === true,
    storageKey: typeof value.storageKey === "string" ? value.storageKey : "",
    html: value.html,
  };
  if (typeof value.startsAt === "string" && value.startsAt) {
    banner.startsAt = value.startsAt;
  }
  if (typeof value.expiresAt === "string" && value.expiresAt) {
    banner.expiresAt = value.expiresAt;
  }
  return banner;
}

/**
 * Resolves the banners to render, in order, from window.config.
 *
 * 1. Legacy CSM_PORTAL_TOP_BANNER_HTML (placed first):
 *    - a string is one non-closeable banner, gated by CSM_PORTAL_TOP_BANNER_ENABLED;
 *    - a banner object ({ enabled, closeable, storageKey, html, startsAt?, expiresAt? }) is used as-is and
 *      is gated by its own `enabled` field, not by CSM_PORTAL_TOP_BANNER_ENABLED.
 * 2. CSM_PORTAL_TOP_BANNERS entries, in array order.
 *
 * Only enabled banners are returned. Read at call time so config changes and
 * test overrides are honoured.
 */
export function getTopBanners(): TopBannerItem[] {
  const config = window.config;
  const result: TopBannerItem[] = [];

  const legacy: unknown = config?.CSM_PORTAL_TOP_BANNER_HTML;
  if (typeof legacy === "string") {
    if (config?.CSM_PORTAL_TOP_BANNER_ENABLED === true && legacy) {
      result.push({ enabled: true, closeable: false, storageKey: "", html: legacy });
    }
  } else {
    const banner = toBanner(legacy);
    if (banner) result.push(banner);
  }

  const list: unknown = config?.CSM_PORTAL_TOP_BANNERS;
  if (Array.isArray(list)) {
    for (const entry of list) {
      const banner = toBanner(entry);
      if (banner) result.push(banner);
    }
  }

  return result.filter((b) => b.enabled);
}
