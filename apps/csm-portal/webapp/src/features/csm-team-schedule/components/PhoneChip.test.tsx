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

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import { pagingMember } from "../test/fixtures";
import PhoneChip, { CALLING_LABEL, type PagingPhoneControls } from "./PhoneChip";

const paging = { masked: "+94•••••123", phone: "+94771234123", setBy: "lead@example.com", setAt: "2026-10-01T09:00:00Z" };

function controls(over: Partial<PagingPhoneControls> = {}): PagingPhoneControls {
  return { edit: vi.fn(), remove: vi.fn(), test: vi.fn(), calling: () => false, ...over };
}

describe("PhoneChip", () => {
  it("says the state in words and shows only the masked number", () => {
    render(<PhoneChip member={pagingMember({ membershipId: "a", hasProfilePhone: false, pagingPhone: { ...paging, lastTestStatus: "busy" } })} />);
    expect(screen.getByText(/Paging number · test failed \(busy\)/)).toBeInTheDocument();
    expect(screen.getByText(/\+94•••••123/)).toBeInTheDocument();
    expect(screen.queryByText(/\+94771234123/)).not.toBeInTheDocument();
  });

  it("offers no actions to a reader who may not edit the number", () => {
    render(<PhoneChip member={pagingMember({ membershipId: "a", hasProfilePhone: false, canEditPhone: false })} controls={controls()} />);
    expect(screen.getByText("No number")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("offers Add number where there is no number at all", () => {
    const c = controls();
    const m = pagingMember({ membershipId: "a", name: "Jane Doe", hasProfilePhone: false, canEditPhone: true });
    render(<PhoneChip member={m} controls={c} />);
    fireEvent.click(screen.getByRole("button", { name: "Add a paging number for Jane Doe" }));
    expect(c.edit).toHaveBeenCalledWith(m);
    expect(screen.queryByRole("button", { name: /Test call/ })).not.toBeInTheDocument();
  });

  it("offers nothing to add when the profile has a number", () => {
    render(<PhoneChip member={pagingMember({ membershipId: "a", hasProfilePhone: true, canEditPhone: true })} controls={controls()} />);
    expect(screen.getByText("Profile ✓")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("offers Change, Remove and Test call on a paging number, and says Calling while a test is awaited", () => {
    const c = controls();
    const m = pagingMember({ membershipId: "a", name: "Jane Doe", hasProfilePhone: false, canEditPhone: true, pagingPhone: paging });
    const { rerender } = render(<PhoneChip member={m} controls={c} />);
    fireEvent.click(screen.getByRole("button", { name: "Change Jane Doe's paging number" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove Jane Doe's paging number" }));
    fireEvent.click(screen.getByRole("button", { name: "Test call Jane Doe's paging number" }));
    expect(c.edit).toHaveBeenCalledWith(m);
    expect(c.remove).toHaveBeenCalledWith(m);
    expect(c.test).toHaveBeenCalledTimes(1);

    rerender(<PhoneChip member={m} controls={{ ...c, calling: () => true }} />);
    const calling = screen.getByRole("button", { name: new RegExp(CALLING_LABEL) });
    expect(calling).toHaveTextContent(CALLING_LABEL);
    expect(calling).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(calling);
    expect(c.test).toHaveBeenCalledTimes(1);
  });
});
