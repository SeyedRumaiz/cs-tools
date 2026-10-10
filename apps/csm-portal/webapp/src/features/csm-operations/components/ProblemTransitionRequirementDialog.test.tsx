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

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";

vi.mock("@api/useSearchUsersByName", () => ({
  useSearchInternalUsersByName: () => ({ data: [], isFetching: false, isError: false }),
}));
// AsyncEntitySelect is a full type-ahead Autocomplete; stub it as a plain
// labeled input that reports its id straight through onChange.
vi.mock("@components/AsyncEntitySelect", () => ({
  default: ({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) => (
    <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

import ProblemTransitionRequirementDialog from "@features/csm-operations/components/ProblemTransitionRequirementDialog";

describe("ProblemTransitionRequirementDialog", () => {
  it("assess: needs an assignee, then sends it with the transition", () => {
    const onConfirm = vi.fn();
    render(<ProblemTransitionRequirementDialog transition="assess" isSubmitting={false} onClose={vi.fn()} onConfirm={onConfirm} />);
    const action = screen.getByRole("button", { name: "Assign and move to Assess" });
    expect(action).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Assigned to"), { target: { value: "user-9" } });
    expect(action).not.toBeDisabled();
    fireEvent.click(action);
    expect(onConfirm).toHaveBeenCalledWith({ transition: "assess", assignedToId: "user-9" });
  });

  it("resolve: needs fix notes (not just spaces), then sends them trimmed", () => {
    const onConfirm = vi.fn();
    render(<ProblemTransitionRequirementDialog transition="resolve" isSubmitting={false} onClose={vi.fn()} onConfirm={onConfirm} />);
    const action = screen.getByRole("button", { name: "Move to Resolved" });
    fireEvent.change(screen.getByLabelText(/Fix notes/), { target: { value: "   " } });
    expect(action).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Fix notes/), { target: { value: "  rolled back the config  " } });
    fireEvent.click(action);
    expect(onConfirm).toHaveBeenCalledWith({ transition: "resolve", fixNotes: "rolled back the config" });
  });

  it("disables Cancel and the action while submitting", () => {
    render(<ProblemTransitionRequirementDialog transition="resolve" isSubmitting onClose={vi.fn()} onConfirm={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move to Resolved" })).toBeDisabled();
  });
});
