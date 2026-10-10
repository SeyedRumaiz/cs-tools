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

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import TrackingIdCopy from "./TrackingIdCopy";

describe("TrackingIdCopy", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders nothing when the error carries no correlation ID", () => {
    const { container } = render(<TrackingIdCopy error={new Error("boom")} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the tracking ID and copies it to the clipboard", async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    const { container } = render(
      <TrackingIdCopy error={{ correlationId: "cp-1234" }} />,
    );

    expect(screen.getByText(/Tracking ID: cp-1234/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /copy tracking id/i }));

    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith("cp-1234");
    });
    await waitFor(() => {
      expect(container.querySelector(".lucide-check")).toBeInTheDocument();
    });
  });

  it("does not show a copied confirmation and points the user at the visible ID when the clipboard write fails", async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
    });
    const { container } = render(
      <TrackingIdCopy error={{ correlationId: "cp-5678" }} />,
    );

    fireEvent.click(screen.getByRole("button", { name: /copy tracking id/i }));

    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(1);
    });

    // Give the rejected promise a tick to settle, then confirm no "copied"
    // state was set — the ID text itself remains visible for manual copying.
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(container.querySelector(".lucide-check")).not.toBeInTheDocument();
    expect(screen.getByText(/Tracking ID: cp-5678/)).toBeInTheDocument();
  });
});
