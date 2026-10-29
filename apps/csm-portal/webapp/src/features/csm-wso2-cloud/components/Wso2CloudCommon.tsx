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

import { Alert, Box, Button, Card, Chip, Typography } from "@wso2/oxygen-ui";
import { ArrowLeft } from "@wso2/oxygen-ui-icons-react";
import type { JSX, ReactNode } from "react";
import {
  CLOUD_TIER_LABELS,
  type CloudRequestStatus,
  type CloudTier,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * Shown at the top of every WSO2 Cloud page while its hooks serve sample data
 * (`api/sampleData.ts`). Remove it together with that file.
 */
export function SampleDataNotice(): JSX.Element {
  return (
    <Alert severity="info" variant="outlined">
      Sample data. This section is not connected to WSO2 Cloud yet, and requests raised here are
      not sent anywhere.
    </Alert>
  );
}

export function BackButton({ onClick }: { onClick: () => void }): JSX.Element {
  return (
    <Button
      variant="text"
      size="small"
      startIcon={<ArrowLeft size={16} />}
      onClick={onClick}
      sx={{ alignSelf: "flex-start" }}
    >
      Back
    </Button>
  );
}

/** A titled card, the same shape the account and project detail pages use. */
export function SectionCard({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}): JSX.Element {
  return (
    <Card sx={{ p: 2.5, display: "flex", flexDirection: "column", gap: 2 }}>
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1 }}>
        <Typography variant="subtitle2">{title}</Typography>
        {action}
      </Box>
      {children}
    </Card>
  );
}

export function MetaGrid({ children }: { children: ReactNode }): JSX.Element {
  return (
    <Box
      sx={{
        display: "grid",
        gap: 2,
        gridTemplateColumns: {
          xs: "1fr",
          sm: "repeat(2, minmax(0, 1fr))",
          md: "repeat(3, minmax(0, 1fr))",
        },
      }}
    >
      {children}
    </Box>
  );
}

export function MetaCell({
  label,
  children,
  mono = false,
}: {
  label: string;
  children: ReactNode;
  mono?: boolean;
}): JSX.Element {
  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 0.25, minWidth: 0 }}>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ textTransform: "uppercase", letterSpacing: 0.4 }}
      >
        {label}
      </Typography>
      <Typography
        component="div"
        variant="body2"
        sx={mono ? { fontFamily: "monospace", wordBreak: "break-all" } : { wordBreak: "break-word" }}
      >
        {children}
      </Typography>
    </Box>
  );
}

export function TierChip({ tier }: { tier: CloudTier }): JSX.Element {
  return (
    <Chip
      size="small"
      variant="outlined"
      label={CLOUD_TIER_LABELS[tier]}
      color={tier === "enterprise" ? "primary" : "default"}
    />
  );
}

const REQUEST_STATUS_CHIP: Record<
  CloudRequestStatus,
  { label: string; color: "warning" | "success" | "error" }
> = {
  pending: { label: "Pending", color: "warning" },
  approved: { label: "Approved", color: "success" },
  rejected: { label: "Rejected", color: "error" },
};

export function RequestStatusChip({ status }: { status: CloudRequestStatus }): JSX.Element {
  const { label, color } = REQUEST_STATUS_CHIP[status];
  return <Chip size="small" variant="outlined" label={label} color={color} />;
}

export interface DangerAction {
  title: string;
  description: string;
  buttonLabel: string;
  onClick: () => void;
  disabled?: boolean;
}

/**
 * The red-bordered list of irreversible actions at the bottom of a detail page,
 * as in the Ops portal. Each action only raises a request; the dialog it opens
 * says so.
 */
export function DangerZone({ actions }: { actions: DangerAction[] }): JSX.Element {
  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1 }}>
      <Typography variant="subtitle2" color="error">
        Danger zone
      </Typography>
      <Box sx={{ border: 1, borderColor: "error.main", borderRadius: 1, overflow: "hidden" }}>
        {actions.map((a, i) => (
          <Box
            key={a.title}
            sx={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 2,
              p: 2,
              borderTop: i === 0 ? 0 : 1,
              borderColor: "divider",
            }}
          >
            <Box sx={{ minWidth: 0 }}>
              <Typography variant="body2" color="error" sx={{ fontWeight: 600 }}>
                {a.title}
              </Typography>
              <Typography variant="body2" color="text.secondary">
                {a.description}
              </Typography>
            </Box>
            <Button
              variant="outlined"
              color="error"
              size="small"
              onClick={a.onClick}
              disabled={a.disabled}
              sx={{ flexShrink: 0 }}
            >
              {a.buttonLabel}
            </Button>
          </Box>
        ))}
      </Box>
    </Box>
  );
}
