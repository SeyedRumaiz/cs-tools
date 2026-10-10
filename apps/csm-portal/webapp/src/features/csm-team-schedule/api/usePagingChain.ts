/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useRef } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";
import { useBackendApi } from "@api/backend/client";
import type {
  PagingChainMember,
  PagingChainResponse,
  PagingMemberChange,
  PagingPhone,
  PagingReadinessResponse,
} from "../types";
import { PAGING_TEST_POLL_MAX_MS, PAGING_TEST_POLL_MS } from "../utils/pagingPhone";

/**
 * Case Paging: the system pages people about an incident tier by tier until
 * someone acknowledges. These read and change who is on each tier -- existing
 * team memberships, no role of their own -- and check whether every chain has
 * somebody to page over the coming days.
 */

const pagingKey = (family: string) => ["team-schedule", "paging-chain", family] as const;
const readinessKey = (days: number) => ["team-schedule", "paging-readiness", days] as const;

const NO_PERMISSIONS = { responderTeams: [], teamLeadTeams: [], americasTeamLead: false, heads: false };

export { PAGING_TEST_POLL_MS, PAGING_TEST_POLL_MAX_MS };

/**
 * The chain's refetch interval: every PAGING_TEST_POLL_MS while any member's
 * test call is pending and started under PAGING_TEST_POLL_MAX_MS ago, else
 * none. A call's start is its `lastTestAt`, or failing that when this page
 * first saw it pending (`firstSeen`, keyed by userId, kept by the caller).
 */
export function pagingPollInterval(
  members: readonly PagingChainMember[],
  now: number,
  firstSeen: Map<string, number>,
): number | false {
  let poll = false;
  const pendingIds = new Set<string>();
  for (const m of members) {
    if (m.pagingPhone?.lastTestStatus !== "pending") continue;
    pendingIds.add(m.userId);
    let started = m.pagingPhone.lastTestAt ? Date.parse(m.pagingPhone.lastTestAt) : NaN;
    if (Number.isNaN(started)) {
      started = firstSeen.get(m.userId) ?? now;
      firstSeen.set(m.userId, started);
    }
    if (now - started < PAGING_TEST_POLL_MAX_MS) poll = true;
  }
  for (const id of [...firstSeen.keys()]) {
    if (!pendingIds.has(id)) firstSeen.delete(id);
  }
  return poll ? PAGING_TEST_POLL_MS : false;
}

export function useGetPagingChain(
  family: string,
  enabled = true,
): UseQueryResult<PagingChainResponse, Error> {
  const api = useBackendApi();
  const firstSeen = useRef(new Map<string, number>());
  return useQuery<PagingChainResponse, Error>({
    queryKey: pagingKey(family),
    queryFn: async () =>
      (await api.get<PagingChainResponse>(
        `/team-schedule/paging-chain?family=${encodeURIComponent(family)}`,
      )) ?? { family, members: [], count: 0, canEdit: NO_PERMISSIONS },
    enabled,
    staleTime: 60 * 1000,
    // A test call's result arrives on the chain a minute or so later.
    refetchInterval: (query) =>
      pagingPollInterval(query.state.data?.members ?? [], Date.now(), firstSeen.current),
  });
}

export function usePatchPagingMember(): UseMutationResult<
  PagingChainMember,
  Error,
  PagingMemberChange
> {
  const api = useBackendApi();
  const qc = useQueryClient();
  return useMutation<PagingChainMember, Error, PagingMemberChange>({
    mutationFn: ({ membershipId, ...body }) =>
      api.patch<Omit<PagingMemberChange, "membershipId">, PagingChainMember>(
        `/team-schedule/paging-chain/members/${encodeURIComponent(membershipId)}`,
        body,
      ),
    // One change can move others (a slot taken off its holder, a head
    // replaced), so redraw from the server rather than guess -- and the
    // readiness check reads the same memberships.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["team-schedule", "paging-chain"] });
      void qc.invalidateQueries({ queryKey: ["team-schedule", "paging-readiness"] });
    },
  });
}

/** Whether each chain has someone to page on every one of the next `days`. */
export function useGetPagingReadiness(
  days: number,
  enabled = true,
): UseQueryResult<PagingReadinessResponse, Error> {
  const api = useBackendApi();
  return useQuery<PagingReadinessResponse, Error>({
    queryKey: readinessKey(days),
    queryFn: async () =>
      (await api.get<PagingReadinessResponse>(`/team-schedule/paging-readiness?days=${days}`)) ?? {
        generatedAt: "",
        from: "",
        to: "",
        chains: [],
      },
    enabled,
    staleTime: 60 * 1000,
  });
}

/** Redraw everything that reads a person's numbers. */
function invalidatePaging(qc: ReturnType<typeof useQueryClient>): void {
  void qc.invalidateQueries({ queryKey: ["team-schedule", "paging-chain"] });
  void qc.invalidateQueries({ queryKey: ["team-schedule", "paging-readiness"] });
}

const contactPath = (userId: string) => `/team-schedule/paging-contacts/${encodeURIComponent(userId)}`;

/** Set a person's paging-only number (E.164). */
export function usePutPagingContact(): UseMutationResult<PagingPhone, Error, { userId: string; phone: string }> {
  const api = useBackendApi();
  const qc = useQueryClient();
  return useMutation<PagingPhone, Error, { userId: string; phone: string }>({
    mutationFn: ({ userId, phone }) => api.put<{ phone: string }, PagingPhone>(contactPath(userId), { phone }),
    onSettled: () => invalidatePaging(qc),
  });
}

/** Remove a person's paging-only number. */
export function useDeletePagingContact(): UseMutationResult<void, Error, string> {
  const api = useBackendApi();
  const qc = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: async (userId) => {
      await api.del<unknown>(contactPath(userId));
    },
    onSettled: () => invalidatePaging(qc),
  });
}

/** Place a test call to a person's paging number. The result lands on the
 *  chain later, which polls for it (see pagingPollInterval). */
export function useTestPagingContact(): UseMutationResult<{ lastTestStatus: string }, Error, string> {
  const api = useBackendApi();
  const qc = useQueryClient();
  return useMutation<{ lastTestStatus: string }, Error, string>({
    mutationFn: (userId) => api.postEmpty<{ lastTestStatus: string }>(`${contactPath(userId)}/test`),
    onSettled: () => invalidatePaging(qc),
  });
}
