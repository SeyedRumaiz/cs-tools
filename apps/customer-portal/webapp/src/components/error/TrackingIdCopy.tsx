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

import { Box, Button, Tooltip, Typography } from "@wso2/oxygen-ui";
import { Check, Copy } from "@wso2/oxygen-ui-icons-react";
import { useState, type JSX } from "react";
import { getErrorReferenceId } from "@utils/correlationId";

interface TrackingIdCopyProps {
  /** The raw error object an error page/state received — duck-typed for a
   *  `correlationId` field via `getErrorReferenceId`. Renders nothing when
   *  absent, so it's always safe to render unconditionally. */
  error?: unknown;
}

/**
 * Support "Tracking ID" chip for an error page: shows the request's
 * correlation ID (when the error carries one) with a one-click copy button,
 * so a caller can hand it to support without having to open dev tools.
 * Matches `apps/csm-portal/webapp`'s equivalent `QueryErrorState` affordance.
 */
export default function TrackingIdCopy({
  error,
}: TrackingIdCopyProps): JSX.Element | null {
  const referenceId = getErrorReferenceId(error);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );

  if (!referenceId) return null;

  const handleCopy = (): void => {
    navigator.clipboard.writeText(referenceId).then(
      () => {
        setCopyState("copied");
        setTimeout(() => setCopyState("idle"), 2000);
      },
      () => {
        // Clipboard access denied/unavailable — the ID is still visible and
        // selectable in the text next to this button, so point the caller at it.
        setCopyState("failed");
        setTimeout(() => setCopyState("idle"), 3000);
      },
    );
  };

  const tooltipTitle =
    copyState === "copied"
      ? "Copied!"
      : copyState === "failed"
        ? "Copy failed — select the ID above instead"
        : "Copy tracking ID";

  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "center",
        gap: 0.75,
        px: 1.5,
        py: 0.75,
        borderRadius: 1,
        bgcolor: "action.hover",
      }}
    >
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ fontFamily: "monospace" }}
      >
        Tracking ID: {referenceId}
      </Typography>
      <Tooltip title={tooltipTitle} placement="top">
        <Button
          size="small"
          variant="text"
          color="inherit"
          onClick={handleCopy}
          sx={{ minWidth: 0, p: 0.5, color: "text.disabled" }}
          aria-label="Copy tracking ID"
        >
          {copyState === "copied" ? <Check size={13} /> : <Copy size={13} />}
        </Button>
      </Tooltip>
    </Box>
  );
}
