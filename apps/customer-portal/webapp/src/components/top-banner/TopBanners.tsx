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

import { useState, useEffect, useMemo, type JSX } from "react";
import { type TopBannerItem, topBannersConfig } from "@config/topBannersConfig";
import { useLogger } from "@hooks/useLogger";

function isDismissed(storageKey: string): boolean {
  try {
    return localStorage.getItem(storageKey) === "dismissed";
  } catch {
    return false;
  }
}

function persistDismissal(storageKey: string): void {
  try {
    localStorage.setItem(storageKey, "dismissed");
  } catch {
    // ignore storage errors
  }
}

// setTimeout stores its delay as a signed 32-bit int; larger values fire immediately.
const MAX_TIMEOUT_MS = 2 ** 31 - 1;

/** Epoch ms for a valid startsAt/expiresAt, or null when absent or unparseable. */
function parseTimestamp(value: string | undefined): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
}

interface BannerProps {
  banner: TopBannerItem;
}

const FALLBACK_STORAGE_KEY = "top_banner_fallback_v1";

function Banner({ banner }: BannerProps): JSX.Element | null {
  const { html, closeable } = banner;
  const resolvedStorageKey = banner.storageKey || FALLBACK_STORAGE_KEY;
  const logger = useLogger();
  const [closedByUser, setClosedByUser] = useState(false);

  useEffect(() => {
    if (closeable && !banner.storageKey) {
      logger.warn(
        "A top banner has closeable: true but no storageKey set. " +
          "A fallback key is being used — dismiss state may persist incorrectly.",
      );
    }
  }, [closeable, banner.storageKey, logger]);

  const startMs = useMemo(
    () => parseTimestamp(banner.startsAt),
    [banner.startsAt],
  );
  const expiryMs = useMemo(
    () => parseTimestamp(banner.expiresAt),
    [banner.expiresAt],
  );
  const [now, setNow] = useState(() => Date.now());

  // startsAt >= expiresAt: the window is empty, the banner never shows.
  const emptyWindow =
    startMs !== null && expiryMs !== null && startMs >= expiryMs;
  const inWindow =
    !emptyWindow &&
    (startMs === null || now >= startMs) &&
    (expiryMs === null || now < expiryMs);

  useEffect(() => {
    if (banner.startsAt && startMs === null) {
      logger.warn(
        `A top banner has an invalid startsAt "${banner.startsAt}". ` +
          "It is ignored and the banner is not delayed.",
      );
    }
  }, [banner.startsAt, startMs, logger]);

  useEffect(() => {
    if (banner.expiresAt && expiryMs === null) {
      logger.warn(
        `A top banner has an invalid expiresAt "${banner.expiresAt}". ` +
          "It is ignored and the banner will not auto-hide.",
      );
    }
  }, [banner.expiresAt, expiryMs, logger]);

  useEffect(() => {
    if (emptyWindow) {
      logger.warn(
        `A top banner has startsAt "${banner.startsAt}" at or after expiresAt ` +
          `"${banner.expiresAt}". It will never be shown.`,
      );
    }
  }, [emptyWindow, banner.startsAt, banner.expiresAt, logger]);

  // One scheduler for both boundaries: wake at the next of startsAt/expiresAt
  // still in the future. The clock is re-read on every hop, so boundaries
  // beyond the setTimeout limit are reached in chunks and an early-firing
  // timer cannot change visibility too soon.
  useEffect(() => {
    if (emptyWindow) return undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const schedule = (): void => {
      const current = Date.now();
      // Re-render only when a boundary was actually crossed since the last render.
      setNow((prev) =>
        [startMs, expiryMs].some((t) => t !== null && prev < t && t <= current)
          ? current
          : prev,
      );
      const upcoming = [startMs, expiryMs].filter(
        (t): t is number => t !== null && t > current,
      );
      if (upcoming.length === 0) return;
      timer = setTimeout(
        schedule,
        Math.min(Math.min(...upcoming) - current, MAX_TIMEOUT_MS),
      );
    };
    schedule();
    return () => {
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [startMs, expiryMs, emptyWindow]);

  // Dismissal is only consulted once the banner is actually in its window, so
  // a not-yet-started banner never reads (or writes) stored state.
  const dismissed = useMemo(
    () => closeable && inWindow && isDismissed(resolvedStorageKey),
    [closeable, inWindow, resolvedStorageKey],
  );

  if (!inWindow || closedByUser || dismissed) return null;

  const handleClose = (): void => {
    persistDismissal(resolvedStorageKey);
    setClosedByUser(true);
  };

  return (
    <div style={{ position: "relative", overflow: "hidden" }}>
      {/* biome-ignore lint/security/noDangerouslySetInnerHtml: operator-controlled config HTML */}
      <div dangerouslySetInnerHTML={{ __html: html }} />
      {closeable && (
        <div
          style={{
            position: "absolute",
            inset: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-end",
            paddingRight: "12px",
            pointerEvents: "none",
          }}
        >
          <button
            type="button"
            onClick={handleClose}
            aria-label="Close banner"
            style={{
              pointerEvents: "auto",
              background: "rgba(0,0,0,.55)",
              border: "1px solid rgba(255,255,255,.6)",
              color: "#fff",
              width: "24px",
              height: "24px",
              fontSize: "14px",
              lineHeight: "1",
              cursor: "pointer",
              borderRadius: "4px",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              flexShrink: 0,
            }}
          >
            &times;
          </button>
        </div>
      )}
    </div>
  );
}

/**
 * Renders all enabled top banners defined in CUSTOMER_PORTAL_TOP_BANNERS in config.js.
 * Banners are rendered top-to-bottom in array order.
 * Each banner independently tracks its own dismiss state via its storageKey.
 */
export default function TopBanners(): JSX.Element | null {
  const banners = topBannersConfig.filter((b) => b.enabled);

  if (banners.length === 0) return null;

  return (
    <>
      {banners.map((banner, index) => (
        <Banner key={banner.storageKey || index} banner={banner} />
      ))}
    </>
  );
}
