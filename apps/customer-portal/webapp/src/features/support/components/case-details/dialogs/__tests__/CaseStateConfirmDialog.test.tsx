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
import { ThemeProvider, createTheme } from "@wso2/oxygen-ui";
import CaseStateConfirmDialog from "../CaseStateConfirmDialog";

describe("CaseStateConfirmDialog", () => {
  it("invokes confirm callback when confirm button is clicked", () => {
    const onClose = vi.fn();
    const onConfirm = vi.fn();

    render(
      <ThemeProvider theme={createTheme()}>
        <CaseStateConfirmDialog
          open
          actionLabel="Close"
          isPending={false}
          onClose={onClose}
          onConfirm={onConfirm}
        />
      </ThemeProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  // Regression test for a real, reported bug: closing a case 400'd with
  // "resolutionCode, cause, and closeNotes are required when state is
  // closed or solution_proposed" because nothing in the webapp ever
  // collected them. Confirms the dialog now gates on all three being
  // filled, and submits exactly what was selected/typed.
  it("requires resolutionCode, cause, and closeNotes before confirming, and submits them", () => {
    const onClose = vi.fn();
    const onConfirm = vi.fn();

    render(
      <ThemeProvider theme={createTheme()}>
        <CaseStateConfirmDialog
          open
          actionLabel="Close"
          isPending={false}
          onClose={onClose}
          onConfirm={onConfirm}
          requiresResolutionFields
          resolutionCodes={[
            { id: "SOLVED_WORKAROUND_PROVIDED", label: "Solved Workaround Provided" },
          ]}
          causes={[{ id: "PRODUCT_BUG", label: "Product Bug" }]}
        />
      </ThemeProvider>,
    );

    const confirmButton = screen.getByRole("button", { name: "Confirm" });
    expect(confirmButton).toBeDisabled();

    const comboboxes = screen.getAllByRole("combobox");
    fireEvent.mouseDown(comboboxes[0]);
    fireEvent.click(screen.getByRole("option", { name: "Solved Workaround Provided" }));

    fireEvent.mouseDown(comboboxes[1]);
    fireEvent.click(screen.getByRole("option", { name: "Product Bug" }));

    expect(confirmButton).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Close notes"), {
      target: { value: "Fixed via workaround." },
    });

    expect(confirmButton).not.toBeDisabled();

    fireEvent.click(confirmButton);
    expect(onConfirm).toHaveBeenCalledWith({
      resolutionCode: "SOLVED_WORKAROUND_PROVIDED",
      cause: "PRODUCT_BUG",
      closeNotes: "Fixed via workaround.",
    });
  });
});
