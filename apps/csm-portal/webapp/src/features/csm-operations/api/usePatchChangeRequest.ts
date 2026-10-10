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
  useMutation,
  useQueryClient,
  type UseMutationResult,
} from "@tanstack/react-query";
import { ApiQueryKeys } from "@constants/apiConstants";
import { useBackendApi } from "@api/backend/client";
import type {
  BePatchChangeRequestPayload,
  BePatchChangeRequestResponse,
} from "@api/backend/types";

export interface PatchChangeRequestInput {
  id: string;
  patch: BePatchChangeRequestPayload;
}

/**
 * Update a change request via `PATCH /change-requests/{id}` (ServiceNow data
 * source only). The BE requires at least one field in `patch`.
 *
 * The detail, any cached list/search, and the Approvals tab are all
 * invalidated on both success *and* error (`onSettled`, not just
 * `onSuccess`): a state-changing patch (e.g. `state`) can commit upstream and
 * still be reported back as an error — a response-parsing failure on a slim
 * success receipt has done exactly this. Trusting a cached "unchanged" record
 * after such an error is what lets a state-gated action (like Move to
 * Assess) look re-enabled when the record has actually already moved on;
 * refetching after every attempt keeps the next pre-flight check honest
 * regardless of which side of that ambiguity a given failure falls on.
 *
 * Approvals used to never change as a side effect of this PATCH, so nothing
 * invalidated that query from here — found live as a real bug once entering
 * Assess started auto-provisioning approvers server-side: the Approvals tab
 * kept showing "No approval stages recorded" right after a successful Move
 * to Assess, even though the new approvers existed in the database, until a
 * full page reload refetched it independently. Invalidated unconditionally
 * (not just when `patch.state` looks like "assess") rather than trying to
 * guess from the patch body whether this particular call could have
 * affected it — safe to be wrong in the harmless direction, since an
 * unaffected Approvals tab just refetches the same data it already had.
 */
export function usePatchChangeRequest(): UseMutationResult<
  BePatchChangeRequestResponse,
  Error,
  PatchChangeRequestInput
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<
    BePatchChangeRequestResponse,
    Error,
    PatchChangeRequestInput
  >({
    mutationFn: (input): Promise<BePatchChangeRequestResponse> =>
      api.patch<BePatchChangeRequestPayload, BePatchChangeRequestResponse>(
        `/change-requests/${encodeURIComponent(input.id)}`,
        input.patch,
      ),
    onSettled: (_data, _error, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [ApiQueryKeys.CHANGE_REQUEST_DETAILS, variables.id],
      });
      void queryClient.invalidateQueries({
        queryKey: [ApiQueryKeys.CHANGE_REQUESTS],
      });
      void queryClient.invalidateQueries({
        queryKey: [ApiQueryKeys.CHANGE_REQUEST_APPROVALS, variables.id],
      });
    },
  });
}
