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
  Box,
  Skeleton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
} from "@wso2/oxygen-ui";
import type { JSX } from "react";
import { useLocation, useSearchParams } from "react-router";
import QueryErrorState from "@components/QueryErrorState";
import { useNavTransition } from "@hooks/useNavTransition";
import { useSearchCloudUsers } from "@features/csm-wso2-cloud/api/useSearchCloudUsers";
import CloudSearchBar from "@features/csm-wso2-cloud/components/CloudSearchBar";
import UserStatusChip from "@features/csm-wso2-cloud/components/UserStatusChip";
import {
  SampleDataNotice,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";

const COLUMNS = ["Email", "Organizations", "Status", "Last sign-in"];
const SKELETON_ROWS = 4;

/** Find a WSO2 Cloud user by email, username or id, to lock, disable or delete them. */
export default function CloudUsersPage(): JSX.Element {
  const navigate = useNavTransition();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const { data, isLoading, isError, error } = useSearchCloudUsers(query);
  const users = data ?? [];
  const listPath = `${location.pathname}${location.search}`;

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5 }}>
      <Box>
        <Typography variant="h5">Users</Typography>
        <Typography variant="body2" color="text.secondary">
          Find a WSO2 Cloud user to lock, disable or delete their account.
        </Typography>
      </Box>
      <SampleDataNotice />

      <CloudSearchBar
        key={query}
        initialValue={query}
        label="Search users"
        placeholder="Email, username or user ID"
        onSearch={(q) => setSearchParams(q ? { q } : {})}
      />

      <Box sx={{ border: 1, borderColor: "divider", borderRadius: 1, overflow: "hidden" }}>
        <TableContainer>
          <Table size="small" sx={{ "& .MuiTableCell-root": { borderColor: "divider" } }}>
            <TableHead>
              <TableRow sx={{ bgcolor: "action.hover" }}>
                {COLUMNS.map((c) => (
                  <TableCell key={c}>{c}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {isLoading ? (
                Array.from({ length: SKELETON_ROWS }).map((_, i) => (
                  <TableRow key={i}>
                    {COLUMNS.map((c) => (
                      <TableCell key={c}>
                        <Skeleton variant="rounded" width="70%" height={18} />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : isError ? (
                <TableRow>
                  <TableCell colSpan={COLUMNS.length} align="center">
                    <QueryErrorState message="Failed to load users." error={error} />
                  </TableCell>
                </TableRow>
              ) : users.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={COLUMNS.length} align="center" sx={{ py: 4 }}>
                    <Typography variant="body2" color="text.secondary">
                      No users match "{query}".
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                users.map((u) => (
                  <TableRow
                    key={u.id}
                    hover
                    onClick={() =>
                      navigate(`${WSO2_CLOUD_BASE}/users/${u.id}`, { state: { from: listPath } })
                    }
                    sx={{ cursor: "pointer" }}
                  >
                    <TableCell sx={{ maxWidth: 320 }}>
                      <Typography variant="body2" noWrap title={u.email}>
                        {u.email}
                      </Typography>
                    </TableCell>
                    <TableCell sx={{ maxWidth: 280 }}>
                      <Typography
                        variant="body2"
                        noWrap
                        title={u.organizations.map((o) => o.handle).join(", ")}
                      >
                        {u.organizations.map((o) => o.handle).join(", ") || "—"}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      <UserStatusChip user={u} />
                    </TableCell>
                    <TableCell>{formatDateTime(u.lastLoginOn)}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Box>
    </Box>
  );
}
