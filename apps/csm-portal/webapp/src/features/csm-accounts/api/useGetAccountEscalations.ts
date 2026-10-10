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

import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { useBackendApi } from "@api/backend/client";

export interface AccountEscalation {
  id: string;
  severity: string;
  state: string;
  escalatedOn: string;
}

/**
 * GET /accounts/{accountId}/escalations — an ex-Support Portal Lite feature
 * with no entity-service equivalent (CreateEscalation is an explicit stub
 * there), so there's nothing to merge this onto. Kept viewer-only (see
 * isSplAudience's own doc comment on PortalAccess) since that's the audience
 * it always had.
 */
export function useGetAccountEscalations(
  accountId: string,
  offset: number,
  limit: number,
): UseQueryResult<AccountEscalation[], Error> {
  const api = useBackendApi();
  return useQuery<AccountEscalation[], Error>({
    queryKey: ["account-escalations", accountId, offset, limit],
    queryFn: () =>
      api
        .get<AccountEscalation[]>(
          `/accounts/${encodeURIComponent(accountId)}/escalations?offset=${offset}&limit=${limit}`,
        )
        .then((r) => r ?? []),
    enabled: Boolean(accountId),
  });
}
