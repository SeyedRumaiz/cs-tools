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

import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider, createTheme } from "@wso2/oxygen-ui";
import UsageAndMetricsTabContent from "../UsageAndMetricsTabContent";

vi.mock("react-router", () => ({
  useParams: () => ({ projectId: "p1" }),
}));

const deployments = Array.from({ length: 8 }, (_, i) => ({
  id: `dep-${i}`,
  name: `deployment-${i}`,
  type: { id: "t1", label: "Primary Production" },
  instanceCount: 1,
  productCount: 1,
}));

let resizeObserverCallback: ResizeObserverCallback | undefined;
const resizeObserverObserve = vi.fn();
const resizeObserverDisconnect = vi.fn();

class ResizeObserverMock {
  constructor(callback: ResizeObserverCallback) {
    resizeObserverCallback = callback;
  }

  observe(target: Element): void {
    resizeObserverObserve(target);
  }

  disconnect(): void {
    resizeObserverDisconnect();
  }
}

vi.mock("@api/usePostProjectDeploymentsSearch", () => ({
  usePostProjectDeploymentsSearchAll: () => ({ data: deployments }),
}));

vi.mock("@features/usage-metrics/components/UsageEnvironmentProductsPanel", () => ({
  default: () => <div data-testid="usage-environment-products-panel" />,
}));

vi.mock("@features/usage-metrics/components/UsageMetricsTimeRangeSelector", () => ({
  default: () => <div data-testid="usage-time-range-selector" />,
}));

vi.mock("@features/usage-metrics/components/DeploymentUsageUploadDialog", () => ({
  default: () => null,
}));

// Regression tests for a real, reported bug (digiops-cs#3241): the
// deployment tab strip scrolls horizontally once there are more deployments
// than fit, but gave no visual sign that there was more to see -- the last
// tab just looked abruptly clipped against the Upload button. jsdom reports
// 0 for scrollWidth/clientWidth/scrollLeft by default, so each test defines
// them directly on the scroll container to simulate a specific scroll state.
describe("UsageAndMetricsTabContent deployment tab scroll affordance", () => {
  beforeEach(() => {
    resizeObserverCallback = undefined;
    resizeObserverObserve.mockClear();
    resizeObserverDisconnect.mockClear();
    vi.stubGlobal("ResizeObserver", ResizeObserverMock);
  });

  function mockScrollContainer(el: HTMLElement, overrides: Partial<Pick<HTMLElement, "scrollWidth" | "clientWidth" | "scrollLeft">>) {
    Object.defineProperty(el, "scrollWidth", { configurable: true, value: overrides.scrollWidth ?? 0 });
    Object.defineProperty(el, "clientWidth", { configurable: true, value: overrides.clientWidth ?? 0 });
    Object.defineProperty(el, "scrollLeft", { configurable: true, writable: true, value: overrides.scrollLeft ?? 0 });
  }

  function renderContent() {
    return render(
      <ThemeProvider theme={createTheme()}>
        <UsageAndMetricsTabContent />
      </ThemeProvider>,
    );
  }

  // TabBar renders role="tablist" on its own root; the actual scrollable
  // container (the ref in UsageAndMetricsTabContent) is that element's
  // grandparent -- tablist -> the "width: max-content" wrapper -> the
  // overflowX:"auto" scroll container.
  function getTabScrollContainer(): HTMLElement {
    const tablist = screen.getByRole("tablist");
    return tablist.parentElement!.parentElement as HTMLElement;
  }

  it("shows no scroll arrows when every tab already fits", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 400, clientWidth: 400, scrollLeft: 0 });
    fireEvent.scroll(scrollEl);

    expect(screen.queryByRole("button", { name: /scroll deployment tabs/i })).not.toBeInTheDocument();
  });

  it("shows a right arrow (not left) when scrolled to the start with overflow", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 1200, clientWidth: 400, scrollLeft: 0 });
    fireEvent.scroll(scrollEl);

    expect(screen.getByRole("button", { name: "Scroll deployment tabs right" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Scroll deployment tabs left" })).not.toBeInTheDocument();
  });

  it("recalculates the affordance when the tab scroller resizes", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 1200, clientWidth: 400, scrollLeft: 0 });

    act(() => resizeObserverCallback?.([], {} as ResizeObserver));

    expect(resizeObserverObserve).toHaveBeenCalledWith(scrollEl);
    expect(screen.getByRole("button", { name: "Scroll deployment tabs right" })).toBeInTheDocument();
  });

  it("works when ResizeObserver is unavailable", () => {
    vi.stubGlobal("ResizeObserver", undefined);

    expect(renderContent).not.toThrow();
  });

  it("shows a left arrow (not right) when scrolled to the end", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 1200, clientWidth: 400, scrollLeft: 800 });
    fireEvent.scroll(scrollEl);

    expect(screen.getByRole("button", { name: "Scroll deployment tabs left" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Scroll deployment tabs right" })).not.toBeInTheDocument();
  });

  it("shows both arrows when scrolled to the middle", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 1200, clientWidth: 400, scrollLeft: 400 });
    fireEvent.scroll(scrollEl);

    expect(screen.getByRole("button", { name: "Scroll deployment tabs left" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Scroll deployment tabs right" })).toBeInTheDocument();
  });

  it("scrolls the tab strip when the right arrow is clicked", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 1200, clientWidth: 400, scrollLeft: 0 });
    fireEvent.scroll(scrollEl);

    const scrollBy = vi.fn();
    scrollEl.scrollBy = scrollBy;
    fireEvent.click(screen.getByRole("button", { name: "Scroll deployment tabs right" }));

    expect(scrollBy).toHaveBeenCalledWith({ left: 320, behavior: "smooth" });
  });

  it("uses theme CSS variable in fade mask backgrounds to support dark mode", () => {
    renderContent();
    const scrollEl = getTabScrollContainer();
    mockScrollContainer(scrollEl, { scrollWidth: 1200, clientWidth: 400, scrollLeft: 400 });
    fireEvent.scroll(scrollEl);

    const leftButton = screen.getByRole("button", { name: "Scroll deployment tabs left" });
    const rightButton = screen.getByRole("button", { name: "Scroll deployment tabs right" });

    const leftMask = leftButton.parentElement!;
    const rightMask = rightButton.parentElement!;

    expect(getComputedStyle(leftMask).background).toContain("var(--oxygen-palette-background-paper");
    expect(getComputedStyle(rightMask).background).toContain("var(--oxygen-palette-background-paper");
  });
});
