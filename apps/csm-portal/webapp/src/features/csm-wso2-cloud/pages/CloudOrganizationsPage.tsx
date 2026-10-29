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
import { useEffect, useRef, type JSX } from "react";
import { useLocation, useSearchParams } from "react-router";
import QueryErrorState from "@components/QueryErrorState";
import { useNavTransition } from "@hooks/useNavTransition";
import { useSearchCloudOrganizations } from "@features/csm-wso2-cloud/api/useSearchCloudOrganizations";
import CloudSearchBar from "@features/csm-wso2-cloud/components/CloudSearchBar";
import {
  SampleDataNotice,
  TierChip,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";

const COLUMNS = ["Handle", "Name", "Owner", "Tier", "Created"];
const SKELETON_ROWS = 4;

/**
 * Find an organisation by its handle, name or id, or by its owner's email.
 *
 * Searching for one organisation's exact handle or id opens it directly;
 * anything else (an owner with several organisations, a partial name) lists
 * the matches. The query lives in `?q=` so Back from an organisation returns
 * to the same results.
 */
export default function CloudOrganizationsPage(): JSX.Element {
  const navigate = useNavTransition();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const { data, isLoading, isFetching, isError, error } = useSearchCloudOrganizations(query);
  const organizations = data ?? [];

  // Set by a submitted search, so reaching this page through Back with an
  // exact query in the URL lists it instead of bouncing straight back into
  // the organisation.
  const openExactMatch = useRef(false);
  const listPath = `${location.pathname}${location.search}`;

  const openOrganization = (id: string) =>
    navigate(`${WSO2_CLOUD_BASE}/organizations/${id}`, { state: { from: listPath } });

  useEffect(() => {
    if (!openExactMatch.current || isFetching || !data) return;
    openExactMatch.current = false;
    const q = query.toLowerCase();
    if (data.length === 1 && (data[0].handle === q || data[0].id === q)) {
      navigate(`${WSO2_CLOUD_BASE}/organizations/${data[0].id}`, { state: { from: listPath } });
    }
  }, [data, isFetching, query, navigate, listPath]);

  const handleSearch = (q: string) => {
    openExactMatch.current = q.length > 0;
    setSearchParams(q ? { q } : {});
  };

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5 }}>
      <Box>
        <Typography variant="h5">Organizations</Typography>
        <Typography variant="body2" color="text.secondary">
          Find a WSO2 Cloud organisation to view, upgrade or delete it.
        </Typography>
      </Box>
      <SampleDataNotice />

      <CloudSearchBar
        key={query}
        initialValue={query}
        label="Search organizations"
        placeholder="Handle, name, organisation ID or owner email"
        onSearch={handleSearch}
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
                    <QueryErrorState message="Failed to load organizations." error={error} />
                  </TableCell>
                </TableRow>
              ) : organizations.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={COLUMNS.length} align="center" sx={{ py: 4 }}>
                    <Typography variant="body2" color="text.secondary">
                      No organizations match "{query}".
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                organizations.map((o) => (
                  <TableRow
                    key={o.id}
                    hover
                    onClick={() => openOrganization(o.id)}
                    sx={{ cursor: "pointer" }}
                  >
                    <TableCell sx={{ maxWidth: 240 }}>
                      <Typography variant="body2" noWrap title={o.handle}>
                        {o.handle}
                      </Typography>
                    </TableCell>
                    <TableCell sx={{ maxWidth: 280 }}>
                      <Typography variant="body2" noWrap title={o.name}>
                        {o.name}
                      </Typography>
                    </TableCell>
                    <TableCell sx={{ maxWidth: 280 }}>
                      <Typography variant="body2" noWrap title={o.owner.email}>
                        {o.owner.email}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      <TierChip tier={o.tier} />
                    </TableCell>
                    <TableCell>{formatDateTime(o.createdOn)}</TableCell>
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
