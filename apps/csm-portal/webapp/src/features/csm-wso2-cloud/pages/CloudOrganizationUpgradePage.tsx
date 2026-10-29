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

import {
  Alert,
  Box,
  Button,
  MenuItem,
  Skeleton,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { useLocation, useParams } from "react-router";
import { useNavTransition } from "@hooks/useNavTransition";
import { useGetCloudOrganization } from "@features/csm-wso2-cloud/api/useGetCloudOrganization";
import { usePostCloudRequest } from "@features/csm-wso2-cloud/api/usePostCloudRequest";
import {
  BackButton,
  SampleDataNotice,
  SectionCard,
  TierChip,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import {
  CLOUD_SUPPORT_LEVEL_LABELS,
  CLOUD_TIER_LABELS,
  type CloudOrganization,
  type CloudOrganizationLimits,
  type CloudSupportLevel,
  type CloudTier,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

type NumericLimit = Exclude<keyof CloudOrganizationLimits, "supportLevel">;

const NUMERIC_LIMITS: { key: NumericLimit; label: string; help: string }[] = [
  {
    key: "monthlyActiveUsers",
    label: "Monthly active users",
    help: "Users who sign in at least once a month.",
  },
  { key: "serviceProviders", label: "Service providers", help: "Applications registered." },
  { key: "identityProviders", label: "Identity providers", help: "External IdPs connected." },
  { key: "subOrganizations", label: "Sub-organizations", help: "Child organizations allowed." },
];

/**
 * Upgrade one organisation's tier and limits. Submitting raises a request for
 * approval; the organisation keeps its current subscription until then.
 *
 * The Salesforce step (checking for a valid subscription and linking the
 * organisation to the right account) belongs at the bottom of this form. It is
 * a placeholder until the Salesforce side is agreed.
 */
export default function CloudOrganizationUpgradePage(): JSX.Element {
  const { organizationId } = useParams<{ organizationId: string }>();
  const navigate = useNavTransition();
  const location = useLocation();
  const backPath =
    (location.state as { from?: string } | undefined)?.from ??
    `${WSO2_CLOUD_BASE}/organizations/${organizationId}`;
  const { data: org, isLoading } = useGetCloudOrganization(organizationId);

  const back = <BackButton onClick={() => navigate(backPath)} />;

  if (isLoading) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Skeleton variant="rounded" height={320} />
      </Box>
    );
  }
  if (!org) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Typography variant="h5">Organization not found</Typography>
      </Box>
    );
  }

  // Keyed on the organisation so the form starts from its current values.
  return <UpgradeForm key={org.id} org={org} back={back} backPath={backPath} />;
}

function UpgradeForm({
  org,
  back,
  backPath,
}: {
  org: CloudOrganization;
  back: JSX.Element;
  backPath: string;
}): JSX.Element {
  const navigate = useNavTransition();
  const post = usePostCloudRequest();
  const [tier, setTier] = useState<CloudTier>("enterprise");
  const [limits, setLimits] = useState<CloudOrganizationLimits>(org.limits);

  const invalid = NUMERIC_LIMITS.some(
    ({ key }) => !Number.isInteger(limits[key]) || limits[key] < 0,
  );
  const unchanged =
    tier === org.tier &&
    (Object.keys(limits) as (keyof CloudOrganizationLimits)[]).every(
      (k) => limits[k] === org.limits[k],
    );

  const handleSubmit = () => {
    post.mutate(
      {
        type: "upgrade_organization",
        target: { kind: "organization", id: org.id, label: org.handle },
        summary:
          tier === org.tier
            ? `Limits change on ${CLOUD_TIER_LABELS[tier]}`
            : `${CLOUD_TIER_LABELS[org.tier]} → ${CLOUD_TIER_LABELS[tier]}`,
        tier,
        limits,
      },
      { onSuccess: () => navigate(`${WSO2_CLOUD_BASE}/requests?tab=mine`) },
    );
  };

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5, maxWidth: 880 }}>
      {back}
      <SampleDataNotice />

      <Box>
        <Typography variant="h5">Upgrade {org.handle}</Typography>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1, mt: 0.5 }}>
          <Typography variant="body2" color="text.secondary">
            Current tier
          </Typography>
          <TierChip tier={org.tier} />
        </Box>
      </Box>

      <SectionCard title="Subscription">
        <TextField
          select
          size="small"
          label="New tier"
          value={tier}
          onChange={(e) => setTier(e.target.value as CloudTier)}
          sx={{ maxWidth: 320 }}
        >
          {(Object.keys(CLOUD_TIER_LABELS) as CloudTier[]).map((t) => (
            <MenuItem key={t} value={t}>
              {CLOUD_TIER_LABELS[t]}
              {t === org.tier ? " (current)" : ""}
            </MenuItem>
          ))}
        </TextField>

        <Box
          sx={{
            display: "grid",
            gap: 2,
            gridTemplateColumns: { xs: "1fr", sm: "repeat(2, minmax(0, 1fr))" },
          }}
        >
          {NUMERIC_LIMITS.map(({ key, label, help }) => (
            <TextField
              key={key}
              size="small"
              type="number"
              label={label}
              value={Number.isNaN(limits[key]) ? "" : limits[key]}
              onChange={(e) => setLimits({ ...limits, [key]: parseInt(e.target.value, 10) })}
              helperText={`${help} Current: ${org.limits[key].toLocaleString()}`}
              slotProps={{ htmlInput: { min: 0, step: 1 } }}
            />
          ))}
          <TextField
            select
            size="small"
            label="Support level"
            value={limits.supportLevel}
            onChange={(e) =>
              setLimits({ ...limits, supportLevel: e.target.value as CloudSupportLevel })
            }
            helperText={`Current: ${CLOUD_SUPPORT_LEVEL_LABELS[org.limits.supportLevel]}`}
          >
            {(Object.keys(CLOUD_SUPPORT_LEVEL_LABELS) as CloudSupportLevel[]).map((s) => (
              <MenuItem key={s} value={s}>
                {CLOUD_SUPPORT_LEVEL_LABELS[s]}
              </MenuItem>
            ))}
          </TextField>
        </Box>
      </SectionCard>

      <SectionCard title="Salesforce">
        <Alert severity="info" variant="outlined">
          Coming later: check for a valid subscription in Salesforce and link this organization to
          the customer's account, or ask for the right account when there is none.
        </Alert>
      </SectionCard>

      {post.isError && <Alert severity="error">{post.error.message}</Alert>}

      <Box sx={{ display: "flex", gap: 1, justifyContent: "flex-end" }}>
        <Button onClick={() => navigate(backPath)} disabled={post.isPending}>
          Cancel
        </Button>
        <Button
          variant="contained"
          onClick={handleSubmit}
          disabled={unchanged || invalid || post.isPending}
        >
          Submit for approval
        </Button>
      </Box>
    </Box>
  );
}
