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

import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveChatAlertSignals } from "./useLiveChatAlertSignals";

const oscillatorStart = vi.fn();

class FakeAudioContext {
  state = "running";
  currentTime = 0;
  destination = {};
  resume = vi.fn(() => Promise.resolve());
  createOscillator = vi.fn(() => ({
    type: "",
    frequency: { value: 0 },
    connect: (node: unknown) => node,
    start: oscillatorStart,
    stop: vi.fn(),
  }));
  createGain = vi.fn(() => ({
    gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
    connect: (node: unknown) => node,
  }));
}

function setHidden(hidden: boolean): void {
  Object.defineProperty(document, "hidden", { configurable: true, value: hidden });
}

describe("useLiveChatAlertSignals", () => {
  beforeEach(() => {
    oscillatorStart.mockClear();
    vi.stubGlobal("AudioContext", FakeAudioContext);
    Object.defineProperty(window, "AudioContext", { configurable: true, value: FakeAudioContext });
    document.title = "CSM Portal";
    setHidden(false);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("plays the sound once per newly pending case, not on re-renders", () => {
    const { rerender } = renderHook(({ ids }) => useLiveChatAlertSignals(ids), { initialProps: { ids: [] as string[] } });
    expect(oscillatorStart).not.toHaveBeenCalled();

    rerender({ ids: ["c1"] });
    const afterFirst = oscillatorStart.mock.calls.length;
    expect(afterFirst).toBeGreaterThan(0);

    rerender({ ids: ["c1"] });
    expect(oscillatorStart.mock.calls.length).toBe(afterFirst);

    rerender({ ids: ["c1", "c2"] });
    expect(oscillatorStart.mock.calls.length).toBeGreaterThan(afterFirst);
  });

  it("flashes the tab title while hidden and restores it afterwards", () => {
    vi.useFakeTimers();
    setHidden(true);
    const { rerender, unmount } = renderHook(({ ids }) => useLiveChatAlertSignals(ids), {
      initialProps: { ids: ["c1"] },
    });

    vi.advanceTimersByTime(1000);
    expect(document.title).toBe("(1) Live chat requested");
    vi.advanceTimersByTime(1000);
    expect(document.title).toBe("CSM Portal");

    rerender({ ids: [] });
    expect(document.title).toBe("CSM Portal");
    unmount();
  });

  it("leaves the title alone while the tab is visible", () => {
    vi.useFakeTimers();
    setHidden(false);
    renderHook(() => useLiveChatAlertSignals(["c1"]));
    vi.advanceTimersByTime(3000);
    expect(document.title).toBe("CSM Portal");
  });
});
