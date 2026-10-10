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

import { describe, expect, it } from "vitest";
import type {
  BeProjectContact,
  BeProjectOnboardingMembership,
  BeProjectOnboardingStep,
} from "@api/backend/types";
import {
  findOnboardingMembership,
  indexOnboardingMembershipsByEmail,
  summarizeOnboarding,
  worstOnboardingStatus,
} from "@features/csm-projects/utils/onboardingStatus";

function step(
  name: BeProjectOnboardingStep["step"],
  status: BeProjectOnboardingStep["status"],
  overrides: Partial<BeProjectOnboardingStep> = {},
): BeProjectOnboardingStep {
  return {
    step: name,
    status,
    attemptCount: 1,
    lastError: null,
    eventType: "CREATED",
    eventModifiedOn: "2026-09-01T10:00:00Z",
    updatedOn: "2026-09-01T10:00:01Z",
    ...overrides,
  };
}

function membership(
  email: string,
  steps: BeProjectOnboardingStep[],
  overrides: Partial<BeProjectOnboardingMembership> = {},
): BeProjectOnboardingMembership {
  return {
    membershipSfId: "a0X000000000001AAA",
    contactSfId: null,
    email,
    projectContactId: null,
    steps,
    ...overrides,
  };
}

describe("worstOnboardingStatus", () => {
  it("returns undefined for no steps", () => {
    expect(worstOnboardingStatus([])).toBeUndefined();
  });

  it("ranks FAILED above SKIPPED above SUCCEEDED", () => {
    expect(worstOnboardingStatus([step("IDENTITY", "SUCCEEDED")])).toBe("SUCCEEDED");
    expect(
      worstOnboardingStatus([step("IDENTITY", "SUCCEEDED"), step("EMAIL", "SKIPPED")]),
    ).toBe("SKIPPED");
    expect(
      worstOnboardingStatus([
        step("IDENTITY", "SKIPPED"),
        step("DATABASE", "SUCCEEDED"),
        step("EMAIL", "FAILED"),
      ]),
    ).toBe("FAILED");
  });
});

describe("summarizeOnboarding", () => {
  it("returns undefined for no membership or no steps", () => {
    expect(summarizeOnboarding(undefined)).toBeUndefined();
    expect(summarizeOnboarding(membership("a@example.com", []))).toBeUndefined();
  });

  it("names the failed steps in the label with the error role", () => {
    const summary = summarizeOnboarding(
      membership("a@example.com", [
        step("IDENTITY", "SUCCEEDED"),
        step("DATABASE", "SUCCEEDED"),
        step("EMAIL", "FAILED", { attemptCount: 3, lastError: "smtp: 550 mailbox unavailable" }),
      ]),
    );
    expect(summary).toEqual({ status: "FAILED", label: "Failed: EMAIL", role: "error" });
  });

  it("names every skipped step when nothing failed", () => {
    const summary = summarizeOnboarding(
      membership("a@example.com", [
        step("IDENTITY", "SKIPPED"),
        step("DATABASE", "SUCCEEDED"),
        step("EMAIL", "SKIPPED"),
      ]),
    );
    expect(summary).toEqual({
      status: "SKIPPED",
      label: "Skipped: IDENTITY, EMAIL",
      role: "default",
    });
  });

  it("reads In progress while all recorded steps succeeded but REGISTRATION is not yet recorded", () => {
    const summary = summarizeOnboarding(
      membership("a@example.com", [
        step("IDENTITY", "SUCCEEDED"),
        step("DATABASE", "SUCCEEDED"),
        step("EMAIL", "SUCCEEDED"),
      ]),
    );
    expect(summary).toEqual({ status: "SUCCEEDED", label: "In progress", role: "info" });
  });

  it("reads Completed once REGISTRATION succeeded too", () => {
    const summary = summarizeOnboarding(
      membership("a@example.com", [
        step("IDENTITY", "SUCCEEDED"),
        step("DATABASE", "SUCCEEDED"),
        step("EMAIL", "SUCCEEDED"),
        step("REGISTRATION", "SUCCEEDED"),
      ]),
    );
    expect(summary).toEqual({ status: "SUCCEEDED", label: "Completed", role: "success" });
  });
});

describe("indexOnboardingMembershipsByEmail / findOnboardingMembership", () => {
  const jane = membership("jane.doe@example.com", [step("IDENTITY", "SUCCEEDED")]);
  const index = indexOnboardingMembershipsByEmail([jane]);

  it("matches a contact by email regardless of case and surrounding whitespace", () => {
    const contact: BeProjectContact = { email: "  Jane.Doe@Example.com " };
    expect(findOnboardingMembership(contact, index)).toBe(jane);
  });

  it("returns undefined for a contact with no email or no recorded membership", () => {
    expect(findOnboardingMembership({}, index)).toBeUndefined();
    expect(findOnboardingMembership({ email: "nobody@example.com" }, index)).toBeUndefined();
  });

  it("keeps the most recently updated membership when one email was invited twice", () => {
    const older = membership(
      "twice@example.com",
      [step("EMAIL", "FAILED", { updatedOn: "2026-01-01T00:00:00Z" })],
      { membershipSfId: "a0X-old" },
    );
    const newer = membership(
      "twice@example.com",
      [step("EMAIL", "SUCCEEDED", { updatedOn: "2026-06-01T00:00:00Z" })],
      { membershipSfId: "a0X-new" },
    );
    expect(indexOnboardingMembershipsByEmail([newer, older]).get("twice@example.com")).toBe(newer);
    expect(indexOnboardingMembershipsByEmail([older, newer]).get("twice@example.com")).toBe(newer);
  });
});
