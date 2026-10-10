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

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const warn = vi.fn();
vi.mock("@hooks/useLogger", () => ({
  useLogger: () => ({ warn, info: vi.fn(), error: vi.fn(), debug: vi.fn() }),
}));

import TopBanners from "@components/top-banner/TopBanners";

type Cfg = Record<string, unknown>;

function setConfig(cfg: Cfg): void {
  (window as unknown as { config: Cfg }).config = cfg;
}

const banner = (over: Cfg = {}): Cfg => ({
  enabled: true,
  closeable: false,
  storageKey: "k1",
  html: "<div>banner one</div>",
  ...over,
});

describe("TopBanners", () => {
  beforeEach(() => {
    localStorage.clear();
    warn.mockClear();
    setConfig({});
  });
  afterEach(cleanup);

  it("renders nothing with no config", () => {
    const { container } = render(<TopBanners />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders multiple enabled banners in order and skips disabled ones", () => {
    setConfig({
      CSM_PORTAL_TOP_BANNERS: [
        banner({ storageKey: "a", html: "<p>first</p>" }),
        banner({ storageKey: "b", html: "<p>hidden</p>", enabled: false }),
        banner({ storageKey: "c", html: "<p>second</p>" }),
      ],
    });
    const { container } = render(<TopBanners />);
    expect(screen.queryByText("hidden")).toBeNull();
    const text = container.textContent;
    expect(text).toBe("firstsecond");
  });

  it("shows no close button when not closeable", () => {
    setConfig({ CSM_PORTAL_TOP_BANNERS: [banner()] });
    render(<TopBanners />);
    expect(screen.getByText("banner one")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Close banner" })).toBeNull();
  });

  it("closes, persists dismissal, and stays hidden on remount", () => {
    setConfig({
      CSM_PORTAL_TOP_BANNERS: [
        banner({ closeable: true, storageKey: "dismiss_me" }),
        banner({ storageKey: "other", html: "<div>stays</div>" }),
      ],
    });
    const first = render(<TopBanners />);
    fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
    expect(screen.queryByText("banner one")).toBeNull();
    expect(screen.getByText("stays")).toBeInTheDocument();
    expect(localStorage.getItem("dismiss_me")).toBe("dismissed");
    first.unmount();

    render(<TopBanners />);
    expect(screen.queryByText("banner one")).toBeNull();
    expect(screen.getByText("stays")).toBeInTheDocument();
  });

  it("uses a fallback key and warns when closeable without storageKey", () => {
    setConfig({
      CSM_PORTAL_TOP_BANNERS: [banner({ closeable: true, storageKey: "" })],
    });
    render(<TopBanners />);
    expect(warn).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
    expect(localStorage.getItem("top_banner_fallback_v1")).toBe("dismissed");
  });

  it("still closes when localStorage throws", () => {
    setConfig({ CSM_PORTAL_TOP_BANNERS: [banner({ closeable: true })] });
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    render(<TopBanners />);
    fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
    expect(screen.queryByText("banner one")).toBeNull();
    spy.mockRestore();
  });

  describe("legacy keys", () => {
    it("renders a legacy string as one non-closeable banner when enabled", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNER_ENABLED: true,
        CSM_PORTAL_TOP_BANNER_HTML: "<div>legacy</div>",
      });
      render(<TopBanners />);
      expect(screen.getByText("legacy")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Close banner" })).toBeNull();
    });

    it("hides a legacy string when ENABLED is not true", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNER_ENABLED: false,
        CSM_PORTAL_TOP_BANNER_HTML: "<div>legacy</div>",
      });
      render(<TopBanners />);
      expect(screen.queryByText("legacy")).toBeNull();
    });

    it("places the legacy banner before CSM_PORTAL_TOP_BANNERS", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNER_ENABLED: true,
        CSM_PORTAL_TOP_BANNER_HTML: "<p>legacy</p>",
        CSM_PORTAL_TOP_BANNERS: [banner({ html: "<p>listed</p>" })],
      });
      const { container } = render(<TopBanners />);
      expect(container.textContent).toBe("legacylisted");
    });

    it("accepts a banner object in CSM_PORTAL_TOP_BANNER_HTML with its own semantics", () => {
      setConfig({
        // ENABLED flag is ignored for the object form.
        CSM_PORTAL_TOP_BANNER_ENABLED: false,
        CSM_PORTAL_TOP_BANNER_HTML: banner({
          closeable: true,
          storageKey: "obj_key",
          html: "<div>object form</div>",
        }),
      });
      render(<TopBanners />);
      expect(screen.getByText("object form")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
      expect(screen.queryByText("object form")).toBeNull();
      expect(localStorage.getItem("obj_key")).toBe("dismissed");
    });

    it("skips an object-form banner whose enabled is false", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNER_ENABLED: true,
        CSM_PORTAL_TOP_BANNER_HTML: banner({ enabled: false }),
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
    });
  });

  describe("sanitization", () => {
    it("strips script tags and event handlers", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({
            html: '<div>safe<script>window.__pwned = 1</script><img src="x" onerror="window.__pwned = 1"></div>',
          }),
        ],
      });
      const { container } = render(<TopBanners />);
      expect(container.querySelector("script")).toBeNull();
      expect(container.querySelector("img")?.hasAttribute("onerror")).toBe(false);
      expect(screen.getByText("safe")).toBeInTheDocument();
    });

    it("keeps inline styles, strong, img and new-tab links with a safe rel", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({
            html: '<div style="background-color:#000;height:3rem"><a href="https://example.com" target="_blank"><img style="width:100%" src="https://cdn.example.com/a.png" role="presentation"></a><strong>bold</strong></div>',
          }),
        ],
      });
      const { container } = render(<TopBanners />);
      const styled = container.querySelector<HTMLElement>("strong")?.parentElement;
      expect(styled?.style.height).toBe("3rem");
      expect(container.querySelector("strong")?.textContent).toBe("bold");
      expect(container.querySelector("img")?.getAttribute("src")).toBe(
        "https://cdn.example.com/a.png",
      );
      const a = container.querySelector("a");
      expect(a?.getAttribute("target")).toBe("_blank");
      expect(a?.getAttribute("rel")).toBe("noopener noreferrer");
    });
  });

  describe("expiresAt", () => {
    const NOW = new Date("2026-10-10T10:00:00Z");

    beforeEach(() => {
      vi.useFakeTimers();
      vi.setSystemTime(NOW);
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it("does not render a banner whose expiry is in the past", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ expiresAt: "2026-10-10T15:00:00+05:30" })],
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("treats now === expiresAt as expired", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ expiresAt: "2026-10-10T10:00:00Z" })],
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("renders a banner whose expiry is in the future", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ expiresAt: "2026-10-10T18:00:00+05:30" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
    });

    it("hides a mounted banner when the expiry passes, and clears its timer on unmount", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({ expiresAt: "2026-10-10T10:00:10Z" }),
          banner({ storageKey: "b", html: "<div>no expiry</div>" }),
        ],
      });
      const { unmount } = render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(9_000);
      });
      expect(screen.getByText("banner one")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(1_000);
      });
      expect(screen.queryByText("banner one")).toBeNull();
      expect(screen.getByText("no expiry")).toBeInTheDocument();
      unmount();
      expect(vi.getTimerCount()).toBe(0);
    });

    it("clears a pending timer on unmount", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ expiresAt: "2026-10-10T10:00:10Z" })],
      });
      const { unmount } = render(<TopBanners />);
      expect(vi.getTimerCount()).toBe(1);
      unmount();
      expect(vi.getTimerCount()).toBe(0);
    });

    it("hides a far-future expiry beyond the setTimeout limit once it arrives", () => {
      // About 40 days out: more than 2^31-1 ms (~24.8 days).
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ expiresAt: "2026-11-19T10:00:00Z" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(2 ** 31 - 1);
      });
      expect(screen.getByText("banner one")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(40 * 24 * 3600 * 1000 - (2 ** 31 - 1));
      });
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("ignores an unparseable value, keeps the banner and warns", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ expiresAt: "next tuesday" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      expect(warn).toHaveBeenCalledTimes(1);
      expect(vi.getTimerCount()).toBe(0);
    });

    it("never expires when expiresAt is missing", () => {
      setConfig({ CSM_PORTAL_TOP_BANNERS: [banner()] });
      render(<TopBanners />);
      act(() => {
        vi.advanceTimersByTime(365 * 24 * 3600 * 1000);
      });
      expect(screen.getByText("banner one")).toBeInTheDocument();
      expect(warn).not.toHaveBeenCalled();
    });

    it("expires even if previously dismissed state exists, and respects the object form", () => {
      localStorage.setItem("obj_key", "dismissed");
      setConfig({
        CSM_PORTAL_TOP_BANNER_HTML: banner({
          closeable: true,
          storageKey: "obj_key",
          expiresAt: "2026-10-10T10:00:05Z",
        }),
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();

      localStorage.clear();
      cleanup();
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(5_000);
      });
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("has no expiry for the legacy string form", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNER_ENABLED: true,
        CSM_PORTAL_TOP_BANNER_HTML: "<div>legacy</div>",
      });
      render(<TopBanners />);
      act(() => {
        vi.advanceTimersByTime(365 * 24 * 3600 * 1000);
      });
      expect(screen.getByText("legacy")).toBeInTheDocument();
    });
  });

  describe("startsAt", () => {
    const NOW = new Date("2026-10-10T10:00:00Z");

    beforeEach(() => {
      vi.useFakeTimers();
      vi.setSystemTime(NOW);
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it("does not render before startsAt", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ startsAt: "2026-10-10T18:00:00+05:30" })],
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("renders after startsAt", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ startsAt: "2026-10-10T09:00:00Z" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
    });

    it("renders when now === startsAt", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ startsAt: "2026-10-10T10:00:00Z" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
    });

    it("shows a mounted banner live when startsAt arrives, leaving others", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({ startsAt: "2026-10-10T10:00:10Z" }),
          banner({ storageKey: "b", html: "<div>no start</div>" }),
        ],
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
      expect(screen.getByText("no start")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(9_000);
      });
      expect(screen.queryByText("banner one")).toBeNull();
      act(() => {
        vi.advanceTimersByTime(1_000);
      });
      expect(screen.getByText("banner one")).toBeInTheDocument();
      // Nothing left to schedule once the only boundary has passed.
      expect(vi.getTimerCount()).toBe(0);
    });

    it("clears the pending start timer on unmount", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ startsAt: "2026-10-10T10:00:10Z" })],
      });
      const { unmount } = render(<TopBanners />);
      expect(vi.getTimerCount()).toBe(1);
      unmount();
      expect(vi.getTimerCount()).toBe(0);
    });

    it("does not read or write dismissal state while not yet started", () => {
      const getItem = vi.spyOn(Storage.prototype, "getItem");
      const setItem = vi.spyOn(Storage.prototype, "setItem");
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({ closeable: true, startsAt: "2026-10-10T10:00:10Z" }),
        ],
      });
      render(<TopBanners />);
      expect(getItem).not.toHaveBeenCalled();
      expect(setItem).not.toHaveBeenCalled();
      getItem.mockRestore();
      setItem.mockRestore();
    });

    it("honours stored dismissal once started", () => {
      localStorage.setItem("k1", "dismissed");
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({ closeable: true, startsAt: "2026-10-10T10:00:10Z" }),
        ],
      });
      render(<TopBanners />);
      act(() => {
        vi.advanceTimersByTime(10_000);
      });
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("shows a started banner with a close button that still works", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({ closeable: true, startsAt: "2026-10-10T09:00:00Z" }),
        ],
      });
      render(<TopBanners />);
      fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
      expect(screen.queryByText("banner one")).toBeNull();
      expect(localStorage.getItem("k1")).toBe("dismissed");
    });

    describe("window with startsAt and expiresAt", () => {
      const win = (): Cfg => ({
        CSM_PORTAL_TOP_BANNERS: [
          banner({
            startsAt: "2026-10-10T10:00:10Z",
            expiresAt: "2026-10-10T10:00:20Z",
          }),
        ],
      });

      it("is hidden before, shown inside, hidden after (live)", () => {
        setConfig(win());
        render(<TopBanners />);
        expect(screen.queryByText("banner one")).toBeNull();
        act(() => {
          vi.advanceTimersByTime(10_000);
        });
        expect(screen.getByText("banner one")).toBeInTheDocument();
        act(() => {
          vi.advanceTimersByTime(10_000);
        });
        expect(screen.queryByText("banner one")).toBeNull();
        expect(vi.getTimerCount()).toBe(0);
      });

      it("renders when mounted inside the window", () => {
        vi.setSystemTime(new Date("2026-10-10T10:00:15Z"));
        setConfig(win());
        render(<TopBanners />);
        expect(screen.getByText("banner one")).toBeInTheDocument();
      });

      it("does not render when mounted after the window", () => {
        vi.setSystemTime(new Date("2026-10-10T10:01:00Z"));
        setConfig(win());
        render(<TopBanners />);
        expect(screen.queryByText("banner one")).toBeNull();
        expect(vi.getTimerCount()).toBe(0);
      });
    });

    it("never shows when startsAt >= expiresAt, with one warning", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({
            startsAt: "2026-10-10T09:00:00Z",
            expiresAt: "2026-10-10T09:00:00Z",
          }),
        ],
      });
      const { rerender } = render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
      act(() => {
        vi.advanceTimersByTime(24 * 3600 * 1000);
      });
      rerender(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
      expect(warn).toHaveBeenCalledTimes(1);
      expect(vi.getTimerCount()).toBe(0);
    });

    it("ignores an unparseable value, shows the banner and warns", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ startsAt: "next tuesday" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      expect(warn).toHaveBeenCalledTimes(1);
      expect(vi.getTimerCount()).toBe(0);
    });

    it("an invalid startsAt does not disable a valid expiresAt", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [
          banner({ startsAt: "bogus", expiresAt: "2026-10-10T10:00:05Z" }),
        ],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(5_000);
      });
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("leaves a banner without startsAt unaffected", () => {
      setConfig({ CSM_PORTAL_TOP_BANNERS: [banner()] });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      expect(warn).not.toHaveBeenCalled();
      expect(vi.getTimerCount()).toBe(0);
    });

    it("shows a far-future start beyond the setTimeout limit once it arrives", () => {
      // 40 days out: more than 2^31-1 ms (~24.8 days).
      setConfig({
        CSM_PORTAL_TOP_BANNERS: [banner({ startsAt: "2026-11-19T10:00:00Z" })],
      });
      render(<TopBanners />);
      act(() => {
        vi.advanceTimersByTime(2 ** 31 - 1);
      });
      expect(screen.queryByText("banner one")).toBeNull();
      act(() => {
        vi.advanceTimersByTime(40 * 24 * 3600 * 1000 - (2 ** 31 - 1));
      });
      expect(screen.getByText("banner one")).toBeInTheDocument();
    });

    it("respects startsAt in the banner-object form of the legacy key", () => {
      setConfig({
        CSM_PORTAL_TOP_BANNER_HTML: banner({
          html: "<div>object form</div>",
          startsAt: "2026-10-10T10:00:05Z",
        }),
      });
      render(<TopBanners />);
      expect(screen.queryByText("object form")).toBeNull();
      act(() => {
        vi.advanceTimersByTime(5_000);
      });
      expect(screen.getByText("object form")).toBeInTheDocument();
    });
  });
});
