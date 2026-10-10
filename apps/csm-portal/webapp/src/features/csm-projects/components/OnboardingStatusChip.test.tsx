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
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom/vitest";
import type { BeProjectOnboardingMembership } from "@api/backend/types";
import OnboardingStatusChip from "@features/csm-projects/components/OnboardingStatusChip";

const FAILED_EMAIL: BeProjectOnboardingMembership = {
  membershipSfId: "a0X000000000001AAA",
  contactSfId: "003000000000001AAA",
  email: "jane.doe@example.com",
  projectContactId: null,
  steps: [
    {
      step: "IDENTITY",
      status: "SUCCEEDED",
      attemptCount: 1,
      lastError: null,
      eventType: "CREATED",
      eventModifiedOn: "2026-09-01T10:00:00Z",
      updatedOn: "2026-09-01T10:00:01Z",
    },
    {
      step: "EMAIL",
      status: "FAILED",
      attemptCount: 3,
      // Deliberately looks like markup: it must come out as literal text.
      lastError: "smtp: 550 <mailbox> unavailable",
      eventType: "CREATED",
      eventModifiedOn: "2026-09-01T10:00:00Z",
      updatedOn: "2026-09-02T10:00:01Z",
    },
  ],
};

describe("OnboardingStatusChip", () => {
  it("renders a dash when nothing was recorded for the contact", () => {
    render(<OnboardingStatusChip membership={undefined} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("renders a dash for a membership with no steps", () => {
    render(<OnboardingStatusChip membership={{ ...FAILED_EMAIL, steps: [] }} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("headlines the failed step as an error chip", () => {
    render(<OnboardingStatusChip membership={FAILED_EMAIL} />);
    const label = screen.getByText("Failed: EMAIL", { selector: ".MuiChip-label" });
    expect(label.closest(".MuiChip-root")).toHaveClass("MuiChip-colorError");
  });

  it("lists every step with attempt count and the failed step's lastError as plain text on hover", async () => {
    render(<OnboardingStatusChip membership={FAILED_EMAIL} />);
    fireEvent.mouseOver(screen.getByText("Failed: EMAIL", { selector: ".MuiChip-label" }));

    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("IDENTITY: SUCCEEDED");
    expect(tooltip).toHaveTextContent("EMAIL: FAILED (attempt 3)");
    // The angle brackets survive verbatim: rendered as text, not parsed as an element.
    expect(tooltip).toHaveTextContent("smtp: 550 <mailbox> unavailable");
    expect(tooltip.querySelector("mailbox")).toBeNull();
  });

  it("does not repeat '(attempt 1)' for first attempts", async () => {
    render(<OnboardingStatusChip membership={FAILED_EMAIL} />);
    fireEvent.mouseOver(screen.getByText("Failed: EMAIL", { selector: ".MuiChip-label" }));
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).not.toHaveTextContent("attempt 1");
  });
});
