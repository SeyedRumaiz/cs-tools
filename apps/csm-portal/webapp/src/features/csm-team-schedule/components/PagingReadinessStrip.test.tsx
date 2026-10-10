/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import PagingReadinessStrip from "./PagingReadinessStrip";
import { readinessChain, readinessGap } from "../test/fixtures";

const base = { label: "CRE", days: 7, isLoading: false, isError: false };

describe("PagingReadinessStrip", () => {
  it("says a ready chain is ready, in words, with no list", () => {
    render(<PagingReadinessStrip {...base} chain={readinessChain({ chain: "CRE", label: "CRE" })} />);
    expect(screen.getByText(/✓ ready for the next 7 days/)).toBeInTheDocument();
    expect(screen.queryByRole("list")).not.toBeInTheDocument();
  });

  it("counts the gaps, lists errors before warnings, and names each severity", () => {
    const chain = readinessChain({
      chain: "SRE_SAAS",
      label: "SaaS SRE",
      ready: false,
      gaps: [
        readinessGap({ code: "W1", severity: "warning", message: "Could not check 1 person's mobile number" }),
        readinessGap({ code: "E1", message: "No L1 in TZ2 on Sat" }),
      ],
    });
    render(<PagingReadinessStrip {...base} label="SaaS SRE" chain={chain} />);
    expect(screen.getByText(/⚠ 2 gaps/)).toBeInTheDocument();
    const items = within(screen.getByRole("list")).getAllByRole("listitem");
    expect(items[0]).toHaveTextContent("Error");
    expect(items[0]).toHaveTextContent("No L1 in TZ2 on Sat");
    expect(items[1]).toHaveTextContent("Warning");
  });

  it("collapses to the first six and expands on Show all", () => {
    const gaps = Array.from({ length: 9 }, (_, i) => readinessGap({ code: `G${i}`, message: `Gap ${i}` }));
    render(<PagingReadinessStrip {...base} chain={readinessChain({ chain: "CRE", ready: false, gaps })} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(6);
    const more = screen.getByRole("button", { name: "Show all 9" });
    expect(more).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(more);
    expect(screen.getAllByRole("listitem")).toHaveLength(9);
    expect(screen.getByRole("button", { name: "Show fewer" })).toHaveAttribute("aria-expanded", "true");
  });

  it("offers the action each fix calls for", () => {
    const onFixResponders = vi.fn();
    const onOpenRota = vi.fn();
    const rotaGap = readinessGap({ code: "NO_L1", fix: "rota", date: "2026-10-10", zoneCode: "TZ2" });
    const chain = readinessChain({
      chain: "CRE",
      ready: false,
      gaps: [
        readinessGap({ code: "NO_RESPONDER", fix: "responders", teamKey: "alpha" }),
        rotaGap,
        readinessGap({ code: "NO_PHONE", fix: "profile" }),
        readinessGap({ code: "BAD_CONFIG", fix: "config" }),
        readinessGap({ code: "BAD_DATA", fix: "data" }),
      ],
    });
    render(
      <PagingReadinessStrip {...base} chain={chain} onFixResponders={onFixResponders} onOpenRota={onOpenRota} />,
    );
    const items = screen.getAllByRole("listitem");

    fireEvent.click(within(items[0]).getByRole("button", { name: "Set responders" }));
    expect(onFixResponders).toHaveBeenCalledWith("alpha");

    fireEvent.click(within(items[1]).getByRole("button", { name: /Open the rota on/ }));
    expect(onOpenRota).toHaveBeenCalledWith(rotaGap);

    expect(items[2]).toHaveTextContent("Ask them to add it under avatar › Profile");
    expect(items[3]).toHaveTextContent("Needs an admin");
    expect(items[4]).toHaveTextContent("Needs an admin");
  });

  it("says when the check could not be made rather than showing nothing", () => {
    const { rerender } = render(<PagingReadinessStrip {...base} isLoading />);
    expect(screen.getByText(/Checking the CRE paging chain/)).toBeInTheDocument();
    rerender(<PagingReadinessStrip {...base} isError />);
    expect(screen.getByText(/Could not check whether the CRE paging chain is ready/)).toBeInTheDocument();
  });

  it("offers Add number or Test call on a phone gap where the reader may act, else the profile hint", () => {
    const add = vi.fn();
    const chain = readinessChain({
      chain: "CRE",
      ready: false,
      gaps: [
        readinessGap({ code: "NO_PHONE", fix: "profile", userId: "u1" }),
        readinessGap({ code: "PHONE_UNTESTED", severity: "warning", fix: "profile", userId: "u2" }),
        readinessGap({ code: "PHONE_TEST_FAILED", fix: "profile", userId: "u3" }),
        readinessGap({ code: "ON_LEAVE", fix: "responders", teamKey: "alpha", date: "2026-10-10" }),
      ],
    });
    const onFixResponders = vi.fn();
    render(
      <PagingReadinessStrip
        {...base}
        chain={chain}
        onFixResponders={onFixResponders}
        phoneActionFor={(g) =>
          g.userId === "u1"
            ? { label: "Add number", onClick: add }
            : g.userId === "u2"
              ? { label: "Calling… result in about a minute", onClick: vi.fn(), pending: true }
              : null
        }
      />,
    );
    const items = screen.getAllByRole("listitem");
    // Errors first: NO_PHONE, PHONE_TEST_FAILED, ON_LEAVE, then the warning.
    fireEvent.click(within(items[0]).getByRole("button", { name: "Add number" }));
    expect(add).toHaveBeenCalled();
    expect(items[1]).toHaveTextContent("Ask them to add it under avatar › Profile");
    fireEvent.click(within(items[2]).getByRole("button", { name: "Set responders" }));
    expect(onFixResponders).toHaveBeenCalledWith("alpha");
    expect(within(items[3]).getByRole("button", { name: /Calling/ })).toHaveAttribute("aria-disabled", "true");
  });
});
