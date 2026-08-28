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
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";
import { useBackendApi } from "@api/backend/client";

export type EngineerStatus = "AVAILABLE" | "BUSY" | "OFFLINE";

const ENGINEER_STATUS_QUERY_KEY = ["engineer-status"] as const;

/**
 * Reads the authenticated engineer's current live-chat-routing presence:
 * GET /engineers/me/status (see csm-portal/backend's internal/handler/
 * chat.go HandleGetPresence, which proxies the standalone chat-routing-
 * service's own default of OFFLINE for an engineer it has never seen a
 * presence update from).
 *
 * This backend returns 502 whenever the standalone chat-routing-service
 * itself is unreachable (e.g. not started yet) — a real, expected-during-
 * development condition rather than a transient blip, so this query
 * swallows that failure and reports OFFLINE (the same default the routing
 * service itself would report for an unseen engineer) instead of
 * surfacing an error state. `retry: false` matters just as much as the
 * catch here: without it, react-query's default exponential-backoff retry
 * re-hits a down dependency on every mount/refetch, which is what was
 * flooding the console with repeated 502s.
 */
export function useGetEngineerStatus(): UseQueryResult<EngineerStatus, Error> {
  const api = useBackendApi();

  return useQuery<EngineerStatus, Error>({
    queryKey: ENGINEER_STATUS_QUERY_KEY,
    queryFn: async () => {
      try {
        const result = await api.get<{ status: EngineerStatus }>(
          "/engineers/me/status",
        );
        return result?.status ?? "OFFLINE";
      } catch {
        return "OFFLINE";
      }
    },
    retry: false,
    staleTime: 30_000,
  });
}

/**
 * Sets the authenticated engineer's presence: POST /engineers/me/status
 * (see HandleSetPresence). A rejection is surfaced to the caller (not
 * best-effort) — unlike most of this feature's side-channel calls, the
 * engineer needs to know their requested status change did not actually
 * take effect. If this immediately hands the engineer a queued case (going
 * AVAILABLE with a non-empty queue), that delivery arrives independently
 * over the existing SSE alert stream as a normal "customer_escalation"
 * event (see EngineerAlertNotification) — this hook does not need to
 * special-case it.
 */
export function useSetEngineerStatus(): UseMutationResult<
  { applied: boolean; pendingOffline?: boolean },
  Error,
  EngineerStatus
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<
    { applied: boolean; pendingOffline?: boolean },
    Error,
    EngineerStatus
  >({
    mutationFn: (status) => api.post("/engineers/me/status", { status }),
    onSuccess: (_result, status) => {
      queryClient.setQueryData(ENGINEER_STATUS_QUERY_KEY, status);
    },
  });
}
