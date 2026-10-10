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

import DOMPurify from "dompurify";
import { useEffect, useMemo, useState, type JSX } from "react";
import { getTopBanners, type TopBannerItem } from "@config/topBannersConfig";
import { useLogger } from "@hooks/useLogger";

const FALLBACK_STORAGE_KEY = "top_banner_fallback_v1";

// setTimeout stores its delay as a signed 32-bit int; larger values fire immediately.
const MAX_TIMEOUT_MS = 2 ** 31 - 1;

/** Epoch ms for a valid startsAt/expiresAt, or null when absent or unparseable. */
function parseTimestamp(value: string | undefined): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
}

// Dedicated instance so the hook and the allowed `target` attribute do not
// leak into other DOMPurify usage in the app. Default DOMPurify strips
// `target`, which would turn banner links meant to open in a new tab into
// same-tab navigations; keep it and force a safe `rel`.
const purifier = DOMPurify(window);
purifier.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A" && node.getAttribute("target") === "_blank") {
    node.setAttribute("rel", "noopener noreferrer");
  }
});

function sanitizeBannerHtml(html: string): string {
  return purifier.sanitize(html, { ADD_ATTR: ["target"] });
}

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

function Banner({ banner }: { banner: TopBannerItem }): JSX.Element | null {
  const { closeable } = banner;
  const resolvedStorageKey = banner.storageKey || FALLBACK_STORAGE_KEY;
  const logger = useLogger();
  const [closedByUser, setClosedByUser] = useState(false);
  const sanitizedHtml = useMemo(() => sanitizeBannerHtml(banner.html), [banner.html]);
  const startMs = useMemo(() => parseTimestamp(banner.startsAt), [banner.startsAt]);
  const expiryMs = useMemo(() => parseTimestamp(banner.expiresAt), [banner.expiresAt]);
  const [now, setNow] = useState(() => Date.now());

  // startsAt >= expiresAt: the window is empty, the banner never shows.
  const emptyWindow = startMs !== null && expiryMs !== null && startMs >= expiryMs;
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

  useEffect(() => {
    if (closeable && !banner.storageKey) {
      logger.warn(
        "A top banner has closeable: true but no storageKey set. " +
          "A fallback key is being used; dismiss state may persist incorrectly.",
      );
    }
  }, [closeable, banner.storageKey, logger]);

  if (!inWindow || closedByUser || dismissed || !sanitizedHtml) return null;

  const handleClose = (): void => {
    persistDismissal(resolvedStorageKey);
    setClosedByUser(true);
  };

  return (
    <div style={{ position: "relative", overflow: "hidden" }}>
      <div
        style={{ display: "block", lineHeight: 0, fontSize: 0, overflow: "hidden" }}
        dangerouslySetInnerHTML={{ __html: sanitizedHtml }}
      />
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
 * Renders all enabled top banners (CSM_PORTAL_TOP_BANNERS, plus the legacy
 * CSM_PORTAL_TOP_BANNER_* keys) top-to-bottom in order. Each banner tracks its
 * own dismiss state via its storageKey. Banner HTML is sanitized.
 */
export default function TopBanners(): JSX.Element | null {
  const banners = getTopBanners();

  if (banners.length === 0) return null;

  return (
    <>
      {banners.map((banner, index) => (
        <Banner key={`${index}:${banner.storageKey}`} banner={banner} />
      ))}
    </>
  );
}
