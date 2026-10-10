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

import type {
  BeOnboardingStepStatus,
  BeProjectContact,
  BeProjectOnboardingMembership,
  BeProjectOnboardingStep,
} from "@api/backend/types";
import type { SemanticRole } from "@components/SemanticChip";

/**
 * Worst-first ranking of step statuses: a single FAILED step outranks any
 * number of SKIPPED or SUCCEEDED ones, and SKIPPED outranks SUCCEEDED. Used
 * to collapse a membership's steps into one headline status.
 */
const STATUS_RANK: Record<BeOnboardingStepStatus, number> = {
  FAILED: 0,
  SKIPPED: 1,
  SUCCEEDED: 2,
};

/** The step that marks the flow as finished for a membership. */
const FINAL_STEP = "REGISTRATION";

/**
 * The worst status across a membership's recorded steps, or `undefined` when
 * nothing has been recorded. A status this build does not know is treated as
 * the worst, so a new upstream value surfaces rather than being hidden.
 */
export function worstOnboardingStatus(
  steps: readonly BeProjectOnboardingStep[],
): BeOnboardingStepStatus | undefined {
  let worst: BeOnboardingStepStatus | undefined;
  for (const step of steps) {
    const rank = STATUS_RANK[step.status];
    if (rank === undefined) return step.status;
    if (worst === undefined || rank < STATUS_RANK[worst]) worst = step.status;
  }
  return worst;
}

/** What the Onboarding cell shows for one contact. */
export interface OnboardingSummary {
  /** The worst status across the membership's steps. */
  status: BeOnboardingStepStatus;
  /** Chip text, e.g. `Failed: EMAIL`, `Skipped: IDENTITY, EMAIL`, `In progress`, `Completed`. */
  label: string;
  role: SemanticRole;
}

/**
 * Collapses a membership's steps into the chip to show for it. FAILED and
 * SKIPPED name the steps concerned so the headline says where the flow stuck
 * without opening the detail; all-SUCCEEDED reads `Completed` once
 * REGISTRATION is among the recorded steps and `In progress` before that,
 * since the ledger has no pending state of its own — a step not yet recorded
 * is simply absent. Returns `undefined` for no membership or no steps.
 */
export function summarizeOnboarding(
  membership: BeProjectOnboardingMembership | undefined,
): OnboardingSummary | undefined {
  if (!membership || membership.steps.length === 0) return undefined;
  const status = worstOnboardingStatus(membership.steps);
  if (status === undefined) return undefined;

  const stepsWith = (s: BeOnboardingStepStatus): string =>
    membership.steps
      .filter((step) => step.status === s)
      .map((step) => step.step)
      .join(", ");

  switch (status) {
    case "FAILED":
      return { status, label: `Failed: ${stepsWith("FAILED")}`, role: "error" };
    case "SKIPPED":
      return { status, label: `Skipped: ${stepsWith("SKIPPED")}`, role: "default" };
    case "SUCCEEDED": {
      const finished = membership.steps.some((step) => step.step === FINAL_STEP);
      return finished
        ? { status, label: "Completed", role: "success" }
        : { status, label: "In progress", role: "info" };
    }
    default:
      // An upstream status this build does not know: show it verbatim, as
      // a warning, rather than guess.
      return { status, label: String(status), role: "warning" };
  }
}

/** Most recent `updatedOn` across a membership's steps, as epoch millis (0 when none parse). */
function latestUpdate(membership: BeProjectOnboardingMembership): number {
  let latest = 0;
  for (const step of membership.steps) {
    const t = Date.parse(step.updatedOn);
    if (!Number.isNaN(t) && t > latest) latest = t;
  }
  return latest;
}

/**
 * Indexes memberships by lower-cased email for matching against the contact
 * rows. A project contact row carries no membership or `project_contact` id,
 * so email is the only key the two share. Should one email have several
 * memberships on the same project (a re-invite), the most recently updated
 * one wins — that is the invitation currently being chased.
 */
export function indexOnboardingMembershipsByEmail(
  memberships: readonly BeProjectOnboardingMembership[],
): Map<string, BeProjectOnboardingMembership> {
  const index = new Map<string, BeProjectOnboardingMembership>();
  for (const membership of memberships) {
    const key = membership.email.trim().toLowerCase();
    if (!key) continue;
    const existing = index.get(key);
    if (!existing || latestUpdate(membership) >= latestUpdate(existing)) {
      index.set(key, membership);
    }
  }
  return index;
}

/**
 * The membership recorded for a contact row, matched by lower-cased email;
 * `undefined` when the row has no email or nothing was recorded for it.
 */
export function findOnboardingMembership(
  contact: BeProjectContact,
  index: ReadonlyMap<string, BeProjectOnboardingMembership>,
): BeProjectOnboardingMembership | undefined {
  const key = contact.email?.trim().toLowerCase();
  return key ? index.get(key) : undefined;
}
