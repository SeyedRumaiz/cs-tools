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
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  Typography,
} from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { Link as RouterLink, useLocation, useSearchParams } from "react-router";
import QueryErrorState from "@components/QueryErrorState";
import { useIdTokenClaims } from "@hooks/useIdTokenClaims";
import { useGetCloudRequests } from "@features/csm-wso2-cloud/api/useGetCloudRequests";
import { usePatchCloudRequest } from "@features/csm-wso2-cloud/api/usePatchCloudRequest";
import CancelRequestDialog from "@features/csm-wso2-cloud/components/CancelRequestDialog";
import RejectRequestDialog from "@features/csm-wso2-cloud/components/RejectRequestDialog";
import {
  RequestStatusChip,
  SampleDataNotice,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import {
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

type RequestsTab = "mine" | "pending" | "resolved";

const TABS: { key: RequestsTab; label: string; empty: string }[] = [
  { key: "mine", label: "My requests", empty: "You have not raised any requests." },
  { key: "pending", label: "Pending approvals", empty: "Nothing is waiting for approval." },
  { key: "resolved", label: "Resolved", empty: "No requests have been resolved yet." },
];

function isRequestsTab(value: string | null): value is RequestsTab {
  return TABS.some((t) => t.key === value);
}

/**
 * Every change raised from this section, in three views: the ones you raised,
 * the ones waiting for someone to approve, and the ones already decided. The
 * active view is kept in `?tab=` so a link from a detail page can open
 * "My requests" directly.
 */
export default function CloudRequestsPage(): JSX.Element {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTab = searchParams.get("tab");
  const tab: RequestsTab = isRequestsTab(rawTab) ? rawTab : "pending";
  const me = useIdTokenClaims()?.email ?? "you";
  const { data, isLoading, isError, error } = useGetCloudRequests();
  const resolve = usePatchCloudRequest();
  const [cancelling, setCancelling] = useState<CloudRequest | null>(null);
  const [rejecting, setRejecting] = useState<CloudRequest | null>(null);

  const all = data ?? [];
  const rows = all.filter((r) =>
    tab === "mine" ? r.requestedBy === me : tab === "pending" ? r.status === "pending" : r.status !== "pending",
  );
  const columns = [
    "Request",
    "Type",
    "Target",
    "Change",
    "Requested by",
    "Requested",
    "Status",
    ...(tab === "resolved" ? ["Resolved by"] : []),
    ...(tab === "resolved" ? [] : [""]),
  ];

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5 }}>
      <Box>
        <Typography variant="h5">Requests</Typography>
        <Typography variant="body2" color="text.secondary">
          Upgrades, deletions and user changes take effect only once approved.
        </Typography>
      </Box>
      <SampleDataNotice />

      <Tabs value={tab} onChange={(_, value: RequestsTab) => setSearchParams({ tab: value })}>
        {TABS.map((t) => (
          <Tab key={t.key} value={t.key} label={t.label} />
        ))}
      </Tabs>

      {resolve.isError && (
        <Alert severity="error" onClose={() => resolve.reset()}>
          {resolve.error.message}
        </Alert>
      )}

      <Box sx={{ border: 1, borderColor: "divider", borderRadius: 1, overflow: "hidden" }}>
        <TableContainer>
          <Table size="small" sx={{ "& .MuiTableCell-root": { borderColor: "divider" } }}>
            <TableHead>
              <TableRow sx={{ bgcolor: "action.hover" }}>
                {columns.map((c, i) => (
                  <TableCell key={`${c}-${i}`}>{c}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {isLoading ? (
                Array.from({ length: 3 }).map((_, i) => (
                  <TableRow key={i}>
                    {columns.map((c, j) => (
                      <TableCell key={`${c}-${j}`}>
                        <Skeleton variant="rounded" width="70%" height={18} />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : isError ? (
                <TableRow>
                  <TableCell colSpan={columns.length} align="center">
                    <QueryErrorState message="Failed to load requests." error={error} />
                  </TableCell>
                </TableRow>
              ) : rows.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={columns.length} align="center" sx={{ py: 4 }}>
                    <Typography variant="body2" color="text.secondary">
                      {TABS.find((t) => t.key === tab)?.empty}
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((r) => (
                  <RequestRow
                    key={r.id}
                    request={r}
                    tab={tab}
                    busy={resolve.isPending}
                    onApprove={() => resolve.mutate({ id: r.id, status: "approved" })}
                    onReject={() => setRejecting(r)}
                    onCancel={() => setCancelling(r)}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Box>

      <CancelRequestDialog request={cancelling} onClose={() => setCancelling(null)} />
      <RejectRequestDialog request={rejecting} onClose={() => setRejecting(null)} />
    </Box>
  );
}

function RequestRow({
  request: r,
  tab,
  busy,
  onApprove,
  onReject,
  onCancel,
}: {
  request: CloudRequest;
  tab: RequestsTab;
  busy: boolean;
  onApprove: () => void;
  onReject: () => void;
  onCancel: () => void;
}): JSX.Element {
  const location = useLocation();
  const targetPath =
    r.target.kind === "organization"
      ? `${WSO2_CLOUD_BASE}/organizations/${r.target.id}`
      : `${WSO2_CLOUD_BASE}/users/${r.target.id}`;

  return (
    <TableRow hover>
      <TableCell sx={{ fontFamily: "monospace" }}>{r.id}</TableCell>
      <TableCell>{CLOUD_REQUEST_TYPE_LABELS[r.type]}</TableCell>
      <TableCell sx={{ maxWidth: 240 }}>
        <Link
          component={RouterLink}
          to={targetPath}
          state={{ from: `${location.pathname}${location.search}` }}
          noWrap
          title={r.target.label}
          sx={{ display: "block" }}
        >
          {r.target.label}
        </Link>
      </TableCell>
      <TableCell sx={{ maxWidth: 260 }}>
        <Typography variant="body2" noWrap title={r.summary}>
          {r.summary}
        </Typography>
      </TableCell>
      <TableCell sx={{ maxWidth: 220 }}>
        <Typography variant="body2" noWrap title={r.requestedBy}>
          {r.requestedBy}
        </Typography>
      </TableCell>
      <TableCell>{formatDateTime(r.requestedOn)}</TableCell>
      <TableCell sx={{ maxWidth: 260 }}>
        <RequestStatusChip status={r.status} />
        {r.remark && (
          <Typography
            variant="caption"
            color="text.secondary"
            noWrap
            title={r.remark}
            sx={{ display: "block", mt: 0.5 }}
          >
            {r.remark}
          </Typography>
        )}
      </TableCell>
      {tab === "resolved" && (
        <TableCell sx={{ maxWidth: 220 }}>
          <Typography variant="body2" noWrap title={r.resolvedBy}>
            {r.resolvedBy ?? "—"}
          </Typography>
          <Typography variant="caption" color="text.secondary">
            {formatDateTime(r.resolvedOn)}
          </Typography>
        </TableCell>
      )}
      {tab === "pending" && (
        <TableCell align="right" sx={{ whiteSpace: "nowrap" }}>
          <Button size="small" color="success" disabled={busy} onClick={onApprove}>
            Approve
          </Button>
          <Button size="small" color="error" disabled={busy} onClick={onReject}>
            Reject
          </Button>
        </TableCell>
      )}
      {tab === "mine" && (
        <TableCell align="right" sx={{ whiteSpace: "nowrap" }}>
          {r.status === "pending" && (
            <Button size="small" color="error" onClick={onCancel}>
              Cancel
            </Button>
          )}
        </TableCell>
      )}
    </TableRow>
  );
}
