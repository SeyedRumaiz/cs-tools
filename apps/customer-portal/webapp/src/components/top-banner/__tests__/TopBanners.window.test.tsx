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
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TopBannerItem } from "@config/topBannersConfig";

const warn = vi.fn();
vi.mock("@hooks/useLogger", () => ({
  useLogger: () => ({ warn, info: vi.fn(), error: vi.fn(), debug: vi.fn() }),
}));

// topBannersConfig reads window.config once at import time, so substitute a
// mutable array that each test fills.
const { mockBanners } = vi.hoisted(() => ({
  mockBanners: [] as TopBannerItem[],
}));
vi.mock("@config/topBannersConfig", () => ({ topBannersConfig: mockBanners }));

import TopBanners from "@components/top-banner/TopBanners";

const banner = (over: Partial<TopBannerItem> = {}): TopBannerItem => ({
  enabled: true,
  closeable: false,
  storageKey: "k1",
  html: "<div>banner one</div>",
  ...over,
});

function setConfig(cfg: { banners: TopBannerItem[] }): void {
  setBanners(...cfg.banners);
}

function setBanners(...items: TopBannerItem[]): void {
  mockBanners.splice(0, mockBanners.length, ...items);
}

const DAY_MS = 24 * 3600 * 1000;

describe("TopBanners startsAt/expiresAt", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-10T10:00:00Z"));
    localStorage.clear();
    warn.mockClear();
    setBanners();
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("does not render a banner whose expiry is in the past", () => {
    setBanners(banner({ expiresAt: "2026-10-10T15:00:00+05:30" }));
    render(<TopBanners />);
    expect(screen.queryByText("banner one")).toBeNull();
  });

  it("treats now === expiresAt as expired", () => {
    setBanners(banner({ expiresAt: "2026-10-10T10:00:00Z" }));
    render(<TopBanners />);
    expect(screen.queryByText("banner one")).toBeNull();
  });

  it("renders a banner whose expiry is in the future", () => {
    setBanners(banner({ expiresAt: "2026-10-10T18:00:00+05:30" }));
    render(<TopBanners />);
    expect(screen.getByText("banner one")).toBeInTheDocument();
  });

  it("hides a mounted banner when the expiry passes, leaving others", () => {
    setBanners(
      banner({ expiresAt: "2026-10-10T10:00:10Z" }),
      banner({ storageKey: "b", html: "<div>no expiry</div>" }),
    );
    render(<TopBanners />);
    act(() => {
      vi.advanceTimersByTime(9_000);
    });
    expect(screen.getByText("banner one")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(screen.queryByText("banner one")).toBeNull();
    expect(screen.getByText("no expiry")).toBeInTheDocument();
  });

  it("clears a pending timer on unmount", () => {
    setBanners(banner({ expiresAt: "2026-10-10T10:00:10Z" }));
    const { unmount } = render(<TopBanners />);
    expect(vi.getTimerCount()).toBe(1);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("hides a far-future expiry beyond the setTimeout limit once it arrives", () => {
    // 40 days out: more than 2^31-1 ms (~24.8 days).
    setBanners(banner({ expiresAt: "2026-11-19T10:00:00Z" }));
    render(<TopBanners />);
    act(() => {
      vi.advanceTimersByTime(2 ** 31 - 1);
    });
    expect(screen.getByText("banner one")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(40 * DAY_MS - (2 ** 31 - 1));
    });
    expect(screen.queryByText("banner one")).toBeNull();
  });

  it("ignores an unparseable value, keeps the banner and warns", () => {
    setBanners(banner({ expiresAt: "next tuesday" }));
    render(<TopBanners />);
    expect(screen.getByText("banner one")).toBeInTheDocument();
    expect(warn).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("expires regardless of closeable and stored dismissal", () => {
    localStorage.setItem("k1", "dismissed");
    setBanners(
      banner({ closeable: true, expiresAt: "2026-10-10T09:00:00Z" }),
    );
    render(<TopBanners />);
    expect(screen.queryByText("banner one")).toBeNull();
  });

  it("leaves a banner without expiresAt unaffected, including close behaviour", () => {
    setBanners(banner({ closeable: true }));
    render(<TopBanners />);
    act(() => {
      vi.advanceTimersByTime(365 * DAY_MS);
    });
    expect(screen.getByText("banner one")).toBeInTheDocument();
    expect(warn).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
    expect(screen.queryByText("banner one")).toBeNull();
    expect(localStorage.getItem("k1")).toBe("dismissed");
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
        banners: [banner({ startsAt: "2026-10-10T18:00:00+05:30" })],
      });
      render(<TopBanners />);
      expect(screen.queryByText("banner one")).toBeNull();
    });

    it("renders after startsAt", () => {
      setConfig({
        banners: [banner({ startsAt: "2026-10-10T09:00:00Z" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
    });

    it("renders when now === startsAt", () => {
      setConfig({
        banners: [banner({ startsAt: "2026-10-10T10:00:00Z" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
    });

    it("shows a mounted banner live when startsAt arrives, leaving others", () => {
      setConfig({
        banners: [
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
        banners: [banner({ startsAt: "2026-10-10T10:00:10Z" })],
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
        banners: [
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
        banners: [
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
        banners: [
          banner({ closeable: true, startsAt: "2026-10-10T09:00:00Z" }),
        ],
      });
      render(<TopBanners />);
      fireEvent.click(screen.getByRole("button", { name: "Close banner" }));
      expect(screen.queryByText("banner one")).toBeNull();
      expect(localStorage.getItem("k1")).toBe("dismissed");
    });

    describe("window with startsAt and expiresAt", () => {
      const win = (): { banners: TopBannerItem[] } => ({
        banners: [
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
        banners: [
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
        banners: [banner({ startsAt: "next tuesday" })],
      });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      expect(warn).toHaveBeenCalledTimes(1);
      expect(vi.getTimerCount()).toBe(0);
    });

    it("an invalid startsAt does not disable a valid expiresAt", () => {
      setConfig({
        banners: [
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
      setConfig({ banners: [banner()] });
      render(<TopBanners />);
      expect(screen.getByText("banner one")).toBeInTheDocument();
      expect(warn).not.toHaveBeenCalled();
      expect(vi.getTimerCount()).toBe(0);
    });

    it("shows a far-future start beyond the setTimeout limit once it arrives", () => {
      // 40 days out: more than 2^31-1 ms (~24.8 days).
      setConfig({
        banners: [banner({ startsAt: "2026-11-19T10:00:00Z" })],
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
  });
});
