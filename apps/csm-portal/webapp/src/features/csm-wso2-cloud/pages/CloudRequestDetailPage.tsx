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
  Link,
  Skeleton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
} from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { Link as RouterLink, useLocation, useParams } from "react-router";
import { useNavTransition } from "@hooks/useNavTransition";
import { useIdTokenClaims } from "@hooks/useIdTokenClaims";
import { useGetCloudRequest } from "@features/csm-wso2-cloud/api/useGetCloudRequest";
import { usePatchCloudRequest } from "@features/csm-wso2-cloud/api/usePatchCloudRequest";
import CancelRequestDialog from "@features/csm-wso2-cloud/components/CancelRequestDialog";
import RejectRequestDialog from "@features/csm-wso2-cloud/components/RejectRequestDialog";
import {
  BackButton,
  MetaCell,
  MetaGrid,
  RequestStatusChip,
  SampleDataNotice,
  SectionCard,
  TierChip,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import {
  CLOUD_LIMIT_LABELS,
  CLOUD_REQUEST_TYPE_LABELS,
  CLOUD_SUPPORT_LEVEL_LABELS,
  type CloudOrganizationLimits,
  type CloudRequest,
  type CloudRequestType,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

/** What approving each kind of request does, for the ones with nothing to compare. */
const REQUEST_EFFECTS: Record<Exclude<CloudRequestType, "upgrade_organization">, string> = {
  delete_organization: "The organization and everything in it is permanently deleted.",
  lock_user: "The user can no longer sign in until unlocked.",
  unlock_user: "The user can sign in again, and failed sign-in attempts are reset.",
  disable_user: "The user can no longer access the system until enabled.",
  enable_user: "The user can access the system again.",
  delete_user: "The user account is permanently deleted.",
};

/**
 * One request in full: what it changes, who raised it and, once decided, who
 * decided it and why. Pending requests can be approved or rejected here, and
 * cancelled by the person who raised them.
 */
export default function CloudRequestDetailPage(): JSX.Element {
  const { requestId } = useParams<{ requestId: string }>();
  const navigate = useNavTransition();
  const location = useLocation();
  const backPath =
    (location.state as { from?: string } | undefined)?.from ?? `${WSO2_CLOUD_BASE}/requests`;
  const here = `${location.pathname}${location.search}`;
  const me = useIdTokenClaims()?.email ?? "you";

  const { data: request, isLoading } = useGetCloudRequest(requestId);
  const resolve = usePatchCloudRequest();
  const [rejecting, setRejecting] = useState<CloudRequest | null>(null);
  const [cancelling, setCancelling] = useState<CloudRequest | null>(null);

  const back = <BackButton onClick={() => navigate(backPath)} />;

  if (isLoading) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Skeleton variant="rounded" width={280} height={32} />
        <Skeleton variant="rounded" height={200} />
      </Box>
    );
  }

  if (!request) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Typography variant="h5">Request not found</Typography>
        <Typography variant="body2" color="text.secondary">
          No request with id <code>{requestId}</code>. It may have been cancelled.
        </Typography>
      </Box>
    );
  }

  const pending = request.status === "pending";
  const targetPath =
    request.target.kind === "organization"
      ? `${WSO2_CLOUD_BASE}/organizations/${request.target.id}`
      : `${WSO2_CLOUD_BASE}/users/${request.target.id}`;

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5 }}>
      {back}
      <SampleDataNotice />

      <Box
        sx={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: 2,
          flexWrap: "wrap",
        }}
      >
        <Box>
          <Box sx={{ display: "flex", alignItems: "center", gap: 1.5 }}>
            <Typography variant="h5">{request.id}</Typography>
            <RequestStatusChip status={request.status} />
          </Box>
          <Typography variant="body2" color="text.secondary">
            {CLOUD_REQUEST_TYPE_LABELS[request.type]}: {request.summary}
          </Typography>
        </Box>
        {pending && (
          <Box sx={{ display: "flex", gap: 1 }}>
            {request.requestedBy === me && (
              <Button color="error" onClick={() => setCancelling(request)}>
                Cancel request
              </Button>
            )}
            <Button
              variant="outlined"
              color="error"
              disabled={resolve.isPending}
              onClick={() => setRejecting(request)}
            >
              Reject
            </Button>
            <Button
              variant="contained"
              color="success"
              disabled={resolve.isPending}
              onClick={() => resolve.mutate({ id: request.id, status: "approved" })}
            >
              Approve
            </Button>
          </Box>
        )}
      </Box>

      {resolve.isError && (
        <Alert severity="error" onClose={() => resolve.reset()}>
          {resolve.error.message}
        </Alert>
      )}

      <SectionCard title="Request">
        <MetaGrid>
          <MetaCell label="Type">{CLOUD_REQUEST_TYPE_LABELS[request.type]}</MetaCell>
          <MetaCell label={request.target.kind === "organization" ? "Organization" : "User"}>
            <Link component={RouterLink} to={targetPath} state={{ from: here }}>
              {request.target.label}
            </Link>
          </MetaCell>
          <MetaCell label="Requested by">{request.requestedBy}</MetaCell>
          <MetaCell label="Requested">{formatDateTime(request.requestedOn)}</MetaCell>
          {!pending && (
            <>
              <MetaCell label={request.status === "approved" ? "Approved by" : "Rejected by"}>
                {request.resolvedBy ?? "—"}
              </MetaCell>
              <MetaCell label={request.status === "approved" ? "Approved" : "Rejected"}>
                {formatDateTime(request.resolvedOn)}
              </MetaCell>
            </>
          )}
        </MetaGrid>
        {request.remark && (
          <MetaCell label="Reason">
            <Box component="span" sx={{ whiteSpace: "pre-wrap" }}>
              {request.remark}
            </Box>
          </MetaCell>
        )}
      </SectionCard>

      <SectionCard title={pending ? "What changes when approved" : "What it changed"}>
        {request.type === "upgrade_organization" ? (
          <UpgradeComparison request={request} />
        ) : (
          <Typography variant="body2">{REQUEST_EFFECTS[request.type]}</Typography>
        )}
      </SectionCard>

      <RejectRequestDialog request={rejecting} onClose={() => setRejecting(null)} />
      <CancelRequestDialog
        request={cancelling}
        onClose={() => setCancelling(null)}
        onCancelled={() => navigate(backPath)}
      />
    </Box>
  );
}

function formatLimit(
  limits: CloudOrganizationLimits | undefined,
  key: keyof CloudOrganizationLimits,
): string {
  if (!limits) return "—";
  return key === "supportLevel"
    ? CLOUD_SUPPORT_LEVEL_LABELS[limits.supportLevel]
    : limits[key].toLocaleString();
}

/**
 * The tier and limits before and after, one row each; the rows that change are
 * marked. "Before" is what the organisation had when the request was raised.
 */
function UpgradeComparison({ request }: { request: CloudRequest }): JSX.Element {
  const keys = Object.keys(CLOUD_LIMIT_LABELS) as (keyof CloudOrganizationLimits)[];
  const tierChanged = request.previousTier !== request.tier;

  return (
    <Box sx={{ border: 1, borderColor: "divider", borderRadius: 1, overflow: "hidden" }}>
      <TableContainer>
        <Table size="small" sx={{ "& .MuiTableCell-root": { borderColor: "divider" } }}>
          <TableHead>
            <TableRow sx={{ bgcolor: "action.hover" }}>
              <TableCell>Setting</TableCell>
              <TableCell>When requested</TableCell>
              <TableCell>Requested</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            <TableRow>
              <TableCell>Tier</TableCell>
              <TableCell>
                {request.previousTier ? <TierChip tier={request.previousTier} /> : "—"}
              </TableCell>
              <TableCell>
                {request.tier ? <TierChip tier={request.tier} /> : "—"}
                {tierChanged && (
                  <Typography
                    component="span"
                    variant="caption"
                    color="text.secondary"
                    sx={{ ml: 1 }}
                  >
                    changed
                  </Typography>
                )}
              </TableCell>
            </TableRow>
            {keys.map((key) => {
              const before = formatLimit(request.previousLimits, key);
              const after = formatLimit(request.limits, key);
              const changed = before !== after;
              return (
                <TableRow key={key}>
                  <TableCell>{CLOUD_LIMIT_LABELS[key]}</TableCell>
                  <TableCell>{before}</TableCell>
                  <TableCell sx={{ fontWeight: changed ? 600 : undefined }}>
                    {after}
                    {changed && (
                      <Typography
                        component="span"
                        variant="caption"
                        color="text.secondary"
                        sx={{ ml: 1 }}
                      >
                        changed
                      </Typography>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  );
}
