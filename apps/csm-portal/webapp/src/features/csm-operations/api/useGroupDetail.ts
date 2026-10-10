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
import { ApiQueryKeys } from "@constants/apiConstants";
import { useBackendApi } from "@api/backend/client";
import type { BeGroupDetail } from "@api/backend/types";

/**
 * Look up one group and its active members via `GET /groups/{id}` (PostgreSQL
 * data source, internal staff only) -- what opens from the Assignment group of
 * a change request's approval stage. `id` is the stage's `assignmentGroup.id`
 * (a group id, not a team id). Resolves to `null` when the id is unknown (the
 * backend's 404) so callers render a not-found state rather than an error.
 *
 * Pass `enabled: false` (or no id) to keep it idle; the dialog that shows the
 * group mounts this only once it is opened, so the Approval tab makes no extra
 * request until a group is clicked.
 */
export function useGroupDetail(
  id: string | undefined,
  options: { enabled?: boolean } = {},
): UseQueryResult<BeGroupDetail | null, Error> {
  const api = useBackendApi();

  return useQuery<BeGroupDetail | null, Error>({
    queryKey: [ApiQueryKeys.GROUP_DETAILS, id ?? ""],
    queryFn: (): Promise<BeGroupDetail | null> =>
      api.get<BeGroupDetail>(`/groups/${encodeURIComponent(id as string)}`),
    enabled: !!id && (options.enabled ?? true),
    staleTime: 60_000,
  });
}
