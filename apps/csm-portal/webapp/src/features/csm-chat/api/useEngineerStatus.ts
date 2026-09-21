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

// AVAILABLE/BUSY/OFFLINE are all directly requestable -- chat_status is a
// plain manual toggle, independent of how many cases the engineer is
// actually holding. There is no PENDING here: "pending" (assigned, not yet
// accepted) is a per-case fact -- see EngineerCase.pending -- never a
// top-level engineer status.
export type EngineerStatus = "AVAILABLE" | "BUSY" | "OFFLINE";

// Mirrors chat-routing-service's router.CaseInfo / csm-portal/backend's
// routingclient.CaseInfo. Only the fields this feature's UI actually reads
// are declared.
export interface EngineerCaseInfo {
  caseId: string;
  conversationId: string;
  subject?: string;
  customerEmail?: string;
  customerName?: string;
  message?: string;
}

// Mirrors router.CaseStatus / routingclient.CaseStatus -- one case the
// engineer currently holds, pending or accepted alike (an engineer can hold
// more than one case at once, up to their own configurable capacity).
export interface EngineerCase extends EngineerCaseInfo {
  // False once Accept confirms this specific case.
  pending: boolean;
  // ISO 8601 -- when this case was assigned to this engineer (not when
  // created). Drives EngineerAlertNotification's per-case accept countdown
  // while pending.
  assignedAt: string;
}

export interface EngineerPresence {
  chatStatus: EngineerStatus;
  // How many of the cases below are still open (state OPEN or ACTIVE,
  // session not yet ended) -- i.e. cases.length, kept as its own field
  // since the backend already computes it and it's the number the header
  // dropdown displays (see EngineerStatusMenu).
  activeChats: number;
  // This engineer's configurable concurrent-chat capacity (see
  // cs_engineer_status.max_concurrent_chats) -- range 1-10, default 1.
  maxConcurrentChats: number;
  atCapacity: boolean;
  // Every case this engineer currently holds, pending or accepted alike --
  // lets EngineerAlertNotification rehydrate all of them after losing its
  // own local state (a refresh, a closed tab) instead of the engineer
  // being stuck with nothing left in the UI to act on.
  cases: EngineerCase[];
  // chat-routing-service's configured PENDING_TIMEOUT_SECONDS — always
  // present (a constant, not per-engineer state), used as the per-case
  // countdown's total duration.
  pendingTimeoutSeconds: number;
}

// Exported so other mutations that change presence server-side as a side
// effect (ending or declining a session) can invalidate it and pick up the
// resulting status, instead of leaving this query's cache stale.
export const ENGINEER_STATUS_QUERY_KEY = ["engineer-status"] as const;

// Fallback only — used for the brief window before this query has ever
// resolved (or if it fails and falls back to the OFFLINE default below).
// Once a real response comes back, its own pendingTimeoutSeconds always
// wins. Matches chat-routing-service's own PENDING_TIMEOUT_SECONDS default
// (see that service's .env.example) — keep the two in sync if that default
// ever changes.
export const DEFAULT_PENDING_TIMEOUT_SECONDS = 90;

const EMPTY_PRESENCE: EngineerPresence = {
  chatStatus: "OFFLINE",
  activeChats: 0,
  maxConcurrentChats: 1,
  atCapacity: false,
  cases: [],
  pendingTimeoutSeconds: DEFAULT_PENDING_TIMEOUT_SECONDS,
};

/**
 * Reads the authenticated engineer's current live-chat-routing presence:
 * GET /engineers/me/status (see csm-portal/backend's internal/handler/
 * chat.go HandleGetPresence, which proxies the standalone chat-routing-
 * service's own default of OFFLINE/capacity 1/no cases for an engineer it
 * has never seen a presence update from).
 *
 * This backend returns 502 whenever the standalone chat-routing-service
 * itself is unreachable (e.g. not started yet) — a real, expected-during-
 * development condition rather than a transient blip, so this query
 * swallows that failure and reports the same OFFLINE/empty default instead
 * of surfacing an error state. `retry: false` matters just as much as the
 * catch here: without it, react-query's default exponential-backoff retry
 * re-hits a down dependency on every mount/refetch, which is what was
 * flooding the console with repeated 502s.
 */
export function useGetEngineerStatus(): UseQueryResult<EngineerPresence, Error> {
  const api = useBackendApi();

  return useQuery<EngineerPresence, Error>({
    queryKey: ENGINEER_STATUS_QUERY_KEY,
    queryFn: async () => {
      try {
        const result = await api.get<Partial<EngineerPresence>>("/engineers/me/status");
        return {
          chatStatus: result?.chatStatus ?? "OFFLINE",
          activeChats: result?.activeChats ?? 0,
          maxConcurrentChats: result?.maxConcurrentChats ?? 1,
          atCapacity: result?.atCapacity ?? false,
          cases: result?.cases ?? [],
          pendingTimeoutSeconds: result?.pendingTimeoutSeconds ?? DEFAULT_PENDING_TIMEOUT_SECONDS,
        };
      } catch {
        return EMPTY_PRESENCE;
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
 * take effect.
 *
 * Deliberately invalidates rather than optimistically setting the cache to
 * the requested status: going AVAILABLE with a non-empty queue immediately
 * claims cases off it up to this engineer's own capacity (see
 * router.Router.SetPresence's queue-drain), so the requested status can be
 * right but the case list wrong the instant this resolves. The actual
 * delivery of each queue-drained case still arrives independently over the
 * SSE alert stream as a normal "customer_escalation" event (see
 * EngineerAlertNotification) — this hook does not need to special-case it,
 * just not lie about the cached presence in the meantime.
 */
export function useSetEngineerStatus(): UseMutationResult<
  { applied: boolean; assignedCases?: EngineerCaseInfo[] },
  Error,
  EngineerStatus
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<
    { applied: boolean; assignedCases?: EngineerCaseInfo[] },
    Error,
    EngineerStatus
  >({
    mutationFn: (status) => api.post("/engineers/me/status", { status }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
    },
  });
}

/**
 * Sets the authenticated engineer's own configurable concurrent-chat
 * capacity: PATCH /engineers/me/capacity (see csm-portal/backend's
 * internal/handler/chat.go HandleSetMaxConcurrentChats). Lets an engineer
 * raise their own limit (1-10) from EngineerStatusMenu's dropdown instead
 * of requiring a manual DB change.
 *
 * Lowering the limit below the engineer's current active-chat count never
 * drops an in-progress chat -- it only stops new work from routing to them
 * until they fall back under it (see that handler's own doc comment) -- so
 * this invalidates the same status query rather than needing any special
 * handling for a lowered value.
 */
export function useSetMaxConcurrentChats(): UseMutationResult<
  { applied: boolean },
  Error,
  number
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<{ applied: boolean }, Error, number>({
    mutationFn: (maxConcurrentChats) =>
      api.patch("/engineers/me/capacity", { maxConcurrentChats }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
    },
  });
}
