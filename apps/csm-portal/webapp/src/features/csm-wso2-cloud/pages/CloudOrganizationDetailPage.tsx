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

import { Alert, Box, Button, Link, Skeleton, Typography } from "@wso2/oxygen-ui";
import { CircleArrowUp } from "@wso2/oxygen-ui-icons-react";
import { useState, type JSX } from "react";
import { Link as RouterLink, useLocation, useParams } from "react-router";
import { useNavTransition } from "@hooks/useNavTransition";
import { useGetCloudOrganization } from "@features/csm-wso2-cloud/api/useGetCloudOrganization";
import { useGetCloudRequests } from "@features/csm-wso2-cloud/api/useGetCloudRequests";
import ConfirmRequestDialog from "@features/csm-wso2-cloud/components/ConfirmRequestDialog";
import {
  BackButton,
  DangerZone,
  MetaCell,
  MetaGrid,
  SampleDataNotice,
  SectionCard,
  TierChip,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import {
  CLOUD_REQUEST_TYPE_LABELS,
  CLOUD_SUPPORT_LEVEL_LABELS,
  type NewCloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * One organisation: who owns it, what its subscription allows, and the two
 * actions an account manager takes on it, Upgrade and Delete. Both raise a
 * request for approval rather than changing anything directly.
 *
 * Enterprise login (the organisation's IdP and domain mappings) is left out
 * on purpose: it does not work in the Ops portal either, and comes later.
 */
export default function CloudOrganizationDetailPage(): JSX.Element {
  const { organizationId } = useParams<{ organizationId: string }>();
  const navigate = useNavTransition();
  const location = useLocation();
  const backPath =
    (location.state as { from?: string } | undefined)?.from ?? `${WSO2_CLOUD_BASE}/organizations`;
  const here = `${location.pathname}${location.search}`;

  const { data: org, isLoading, isError } = useGetCloudOrganization(organizationId);
  const { data: requests } = useGetCloudRequests();
  const [deleteRequest, setDeleteRequest] = useState<NewCloudRequest | null>(null);
  const [raised, setRaised] = useState(false);

  const back = <BackButton onClick={() => navigate(backPath)} />;

  if (isLoading) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Skeleton variant="rounded" width={280} height={32} />
        <Skeleton variant="rounded" height={160} />
      </Box>
    );
  }

  if (isError || !org) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Typography variant="h5">Organization not found</Typography>
        <Typography variant="body2" color="text.secondary">
          No organization with id <code>{organizationId}</code>.
        </Typography>
      </Box>
    );
  }

  const pending = (requests ?? []).filter(
    (r) => r.status === "pending" && r.target.kind === "organization" && r.target.id === org.id,
  );

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5 }}>
      {back}
      <SampleDataNotice />

      <Box>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
          <Typography variant="h5">{org.handle}</Typography>
          <TierChip tier={org.tier} />
        </Box>
        <Typography variant="body2" color="text.secondary" sx={{ fontFamily: "monospace" }}>
          {org.id}
        </Typography>
      </Box>

      {raised && (
        <Alert severity="success" onClose={() => setRaised(false)}>
          Request raised. Follow it under{" "}
          <Link component={RouterLink} to={`${WSO2_CLOUD_BASE}/requests?tab=mine`}>
            My requests
          </Link>
          .
        </Alert>
      )}
      {pending.length > 0 && (
        <Alert severity="info">
          Waiting for approval:{" "}
          {pending.map((r) => `${CLOUD_REQUEST_TYPE_LABELS[r.type]} (${r.summary})`).join(", ")}.
        </Alert>
      )}

      <SectionCard title="Organization information">
        <MetaGrid>
          <MetaCell label="Organization ID" mono>
            {org.id}
          </MetaCell>
          <MetaCell label="Handle">{org.handle}</MetaCell>
          <MetaCell label="Name">{org.name}</MetaCell>
          <MetaCell label="Owner">
            <Link
              component={RouterLink}
              to={`${WSO2_CLOUD_BASE}/users/${org.owner.id}`}
              state={{ from: here }}
            >
              {org.owner.email}
            </Link>
          </MetaCell>
          <MetaCell label="Created">{formatDateTime(org.createdOn)}</MetaCell>
        </MetaGrid>
      </SectionCard>

      <SectionCard
        title="Subscription"
        action={
          <Button
            variant="contained"
            size="small"
            startIcon={<CircleArrowUp size={16} />}
            onClick={() =>
              navigate(`${WSO2_CLOUD_BASE}/organizations/${org.id}/upgrade`, {
                state: { from: here },
              })
            }
          >
            Upgrade
          </Button>
        }
      >
        <MetaGrid>
          <MetaCell label="Tier">
            <TierChip tier={org.tier} />
          </MetaCell>
          <MetaCell label="Monthly active users">
            {org.limits.monthlyActiveUsers.toLocaleString()}
          </MetaCell>
          <MetaCell label="Service providers">{org.limits.serviceProviders}</MetaCell>
          <MetaCell label="Identity providers">{org.limits.identityProviders}</MetaCell>
          <MetaCell label="Sub-organizations">{org.limits.subOrganizations}</MetaCell>
          <MetaCell label="Support level">
            {CLOUD_SUPPORT_LEVEL_LABELS[org.limits.supportLevel]}
          </MetaCell>
        </MetaGrid>
      </SectionCard>

      <DangerZone
        actions={[
          {
            title: "Delete organization",
            description: "Raises a request to permanently delete this organization.",
            buttonLabel: "Delete",
            onClick: () =>
              setDeleteRequest({
                type: "delete_organization",
                target: { kind: "organization", id: org.id, label: org.handle },
                summary: `Delete ${org.handle}`,
              }),
          },
        ]}
      />

      <ConfirmRequestDialog
        request={deleteRequest}
        title={`Delete ${org.handle}?`}
        body="Once approved, the organization and everything in it is permanently deleted."
        confirmLabel="Request deletion"
        onClose={() => setDeleteRequest(null)}
        onRaised={() => {
          setDeleteRequest(null);
          setRaised(true);
        }}
      />
    </Box>
  );
}
