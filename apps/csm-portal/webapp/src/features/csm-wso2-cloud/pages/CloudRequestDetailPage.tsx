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
import { usePortalAccess } from "@context/current-user/usePortalAccess";
import { useGetCloudRequest } from "@features/csm-wso2-cloud/api/useGetCloudRequest";
import { usePatchCloudRequest } from "@features/csm-wso2-cloud/api/usePatchCloudRequest";
import { useLinkCloudRequestSalesforce } from "@features/csm-wso2-cloud/api/useLinkCloudRequestSalesforce";
import CancelRequestDialog from "@features/csm-wso2-cloud/components/CancelRequestDialog";
import RejectRequestDialog from "@features/csm-wso2-cloud/components/RejectRequestDialog";
import {
  BackButton,
  MetaCell,
  MetaGrid,
  RequestStatusChip,
  SampleDataNotice,
  SectionCard,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import {
  CLOUD_BILLING_PERIOD_LABELS,
  CLOUD_REGION_LABELS,
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudRegion,
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
  const { isAdmin } = usePortalAccess();

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
  // A customer upgrade needs no approval: it is applied when it is raised.
  const customerUpgrade = request.upgrade?.kind === "customer";
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
            <Typography variant="h5">{request.number}</Typography>
            <RequestStatusChip status={request.status} />
          </Box>
          <Typography variant="body2" color="text.secondary">
            {CLOUD_REQUEST_TYPE_LABELS[request.type]}: {request.summary}
          </Typography>
        </Box>
        {pending && (
          <Box sx={{ display: "flex", gap: 1 }}>
            {/* The requester or an admin; the backend enforces the same rule. */}
            {(request.requestedBy.toLowerCase() === me.toLowerCase() || isAdmin) && (
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
              <MetaCell label={request.status === "approved" ? (customerUpgrade ? "Applied by" : "Approved by") : "Rejected by"}>
                {request.resolvedBy ?? "—"}
              </MetaCell>
              <MetaCell label={request.status === "approved" ? (customerUpgrade ? "Applied" : "Approved") : "Rejected"}>
                {formatDateTime(request.resolvedOn)}
              </MetaCell>
            </>
          )}
        </MetaGrid>
        {request.reason && (
          <MetaCell label="Reason for the request">
            <Box component="span" sx={{ whiteSpace: "pre-wrap" }}>
              {request.reason}
            </Box>
          </MetaCell>
        )}
        {request.remark && (
          <MetaCell label="Reason for rejecting">
            <Box component="span" sx={{ whiteSpace: "pre-wrap" }}>
              {request.remark}
            </Box>
          </MetaCell>
        )}
      </SectionCard>

      <SectionCard title={pending ? "What changes when approved" : "What it changed"}>
        {request.type === "upgrade_organization" ? (
          <UpgradeDetails request={request} />
        ) : (
          <Typography variant="body2">{REQUEST_EFFECTS[request.type]}</Typography>
        )}
      </SectionCard>

      {request.upgrade?.kind === "customer" && <SalesforceLinkCard request={request} />}

      <RejectRequestDialog request={rejecting} onClose={() => setRejecting(null)} />
      <CancelRequestDialog
        request={cancelling}
        onClose={() => setCancelling(null)}
        onCancelled={() => navigate(backPath)}
      />
    </Box>
  );
}

function regionLabel(region: string | undefined): string {
  if (!region) return "—";
  const label = CLOUD_REGION_LABELS[region as CloudRegion];
  return label ? `${label} (${region})` : region;
}

/**
 * A customer upgrade's Salesforce link: once approved, its organisation is
 * linked to the customer's account in Salesforce. A link that failed does not
 * undo the upgrade; it can be tried again here (the backend allows WSO2 Cloud
 * approvers and admins).
 */
function SalesforceLinkCard({ request }: { request: CloudRequest }): JSX.Element {
  const link = useLinkCloudRequestSalesforce();
  const status = request.upgrade?.salesforceLink;
  const retry = (label: string) => (
    <Button variant="outlined" size="small" disabled={link.isPending} onClick={() => link.mutate(request.id)}>
      {label}
    </Button>
  );
  let body: JSX.Element;
  if (request.status === "pending") {
    body = (
      <Typography variant="body2">
        A customer upgrade needs no approval, but this one could not be applied when it was raised. When it is
        approved, the organization is linked in Salesforce to account {request.upgrade?.salesforceAccountId}.
      </Typography>
    );
  } else if (request.status === "rejected") {
    body = <Typography variant="body2">Rejected, so Salesforce was not changed.</Typography>;
  } else if (status?.status === "linked") {
    body = (
      <Alert severity="success" variant="outlined">
        Linked in Salesforce to account {request.upgrade?.salesforceAccountId}
        {status.linkedOn ? ` on ${formatDateTime(status.linkedOn)}` : ""}.
      </Alert>
    );
  } else if (status?.status === "failed") {
    body = (
      <Alert severity="error" action={retry("Retry")}>
        The upgrade was applied, but linking the organization in Salesforce failed. {status.error}
      </Alert>
    );
  } else {
    body = (
      <Alert severity="warning" action={retry("Link now")}>
        The upgrade was applied, but the organization is not linked in Salesforce yet.
      </Alert>
    );
  }
  return (
    <SectionCard title="Salesforce">
      {body}
      {link.isError && <Alert severity="error">{link.error.message}</Alert>}
    </SectionCard>
  );
}

/**
 * What an upgrade asks WSO2 Cloud for: the subscription it moves, the plan
 * before and after, and how it is billed. "Before" is the plan the
 * subscription had when the request was raised. The billing account is
 * created by the approval, so it shows only once there is one.
 */
function UpgradeDetails({ request }: { request: CloudRequest }): JSX.Element {
  const upgrade = request.upgrade;
  if (!upgrade) {
    return <Typography variant="body2">This upgrade names no subscription or plan.</Typography>;
  }
  const planChanged = upgrade.previousPlanCode !== upgrade.planCode;
  const rows: { label: string; before?: string; value: string; changed?: boolean }[] = [
    { label: "Product", value: upgrade.product ?? "—" },
    { label: "Subscription", value: upgrade.subscriptionId },
    { label: "Plan", before: upgrade.previousPlanCode ?? "—", value: upgrade.planCode, changed: planChanged },
    {
      label: "Billing",
      value: `Wire transfer${upgrade.billingPeriod ? `, ${CLOUD_BILLING_PERIOD_LABELS[upgrade.billingPeriod].toLowerCase()}` : ""}`,
    },
    { label: "Notify the customer", value: upgrade.notifyCustomer ? "Yes" : "No" },
    {
      label: "Upgrade kind",
      value:
        upgrade.kind === "customer" ? "Customer" : upgrade.kind === "internal" ? "Internal" : "Not recorded (treated as internal)",
    },
    ...(upgrade.kind === "customer"
      ? [
          { label: "Salesforce account", value: upgrade.salesforceAccountId ?? "—" },
          { label: "WSO2 Cloud region", value: regionLabel(upgrade.region) },
        ]
      : []),
    {
      label: "Billing account",
      value: upgrade.billingAccountId ?? (request.status === "pending" ? "Created when approved" : "—"),
    },
  ];

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
            {rows.map((row) => (
              <TableRow key={row.label}>
                <TableCell>{row.label}</TableCell>
                <TableCell>{row.before ?? ""}</TableCell>
                <TableCell sx={{ fontWeight: row.changed ? 600 : undefined, overflowWrap: "anywhere" }}>
                  {row.value}
                  {row.changed && (
                    <Typography component="span" variant="caption" color="text.secondary" sx={{ ml: 1 }}>
                      changed
                    </Typography>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  );
}
