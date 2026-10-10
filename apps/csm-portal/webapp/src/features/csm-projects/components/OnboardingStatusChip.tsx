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

import { Box, Tooltip, Typography } from "@wso2/oxygen-ui";
import type { JSX } from "react";
import SemanticChip from "@components/SemanticChip";
import type { BeProjectOnboardingMembership } from "@api/backend/types";
import { summarizeOnboarding } from "@features/csm-projects/utils/onboardingStatus";

interface OnboardingStatusChipProps {
  /** The membership matched to this contact row; `undefined` renders a dash. */
  membership: BeProjectOnboardingMembership | undefined;
}

/**
 * One contact's customer-onboarding status: a chip carrying the worst status
 * across the membership's recorded steps (`Failed: EMAIL`, `Skipped: …`,
 * `In progress`, `Completed`), with a hover tooltip listing every step, its
 * status and attempt count, and the last error of a FAILED step.
 *
 * Everything shown is what the ledger row holds. `lastError` is upstream
 * error text and is rendered as plain text through React's normal escaping —
 * never as HTML. A row with no recorded steps renders a plain dash, matching
 * the other empty cells in the table.
 */
export default function OnboardingStatusChip({
  membership,
}: OnboardingStatusChipProps): JSX.Element {
  const summary = summarizeOnboarding(membership);
  if (!membership || !summary) {
    return <>—</>;
  }

  const detail = (
    <Box component="ul" sx={{ m: 0, pl: 2 }}>
      {membership.steps.map((step) => (
        <li key={step.step}>
          <Typography variant="caption" component="div">
            {step.step}: {step.status}
            {step.attemptCount > 1 ? ` (attempt ${step.attemptCount})` : ""}
          </Typography>
          {step.status === "FAILED" && step.lastError ? (
            <Typography
              variant="caption"
              component="div"
              sx={{ opacity: 0.85, wordBreak: "break-word" }}
            >
              {step.lastError}
            </Typography>
          ) : null}
        </li>
      ))}
    </Box>
  );

  return (
    <Tooltip title={detail} arrow placement="top-start">
      {/* SemanticChip is a plain function component; the span gives the
          Tooltip a ref-holding, prop-forwarding child to anchor on. */}
      <Box component="span" sx={{ display: "inline-flex" }}>
        <SemanticChip role={summary.role} label={summary.label} variant="outlined" />
      </Box>
    </Tooltip>
  );
}
