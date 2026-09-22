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

/* eslint-disable react-refresh/only-export-components -- Provider component and its useXxx hook are colocated per the repo's context idiom (fast-refresh DX only) */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type JSX,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { BackendApiError } from "@api/backend/client";
import { useIdTokenClaims } from "@hooks/useIdTokenClaims";
import { useChatAlertsStream } from "@features/csm-chat/api/useChatAlertsStream";
import { useAcceptChatSession } from "@features/csm-chat/api/useAcceptChatSession";
import { useSendChatMessage } from "@features/csm-chat/api/useSendChatMessage";
import { useCompleteChatSession } from "@features/csm-chat/api/useCompleteChatSession";
import { useDeclineChatSession } from "@features/csm-chat/api/useDeclineChatSession";
import { useConvertChatToCase } from "@features/csm-chat/api/useConvertChatToCase";
import {
  DEFAULT_PENDING_TIMEOUT_SECONDS,
  ENGINEER_STATUS_QUERY_KEY,
  useGetEngineerStatus,
  type EngineerPresence,
} from "@features/csm-chat/api/useEngineerStatus";
import type { ChatAlertEvent } from "@features/csm-chat/types/chatAlerts";

export type LiveChatMessage = {
  id: string;
  from: "customer" | "engineer";
  text: string;
};

// A pending alert (assigned, not yet accepted) or an already-accepted active
// session, keyed by caseId in casesByCaseId below -- an engineer can hold
// several of either kind at once (up to their own configurable capacity). A
// single discriminated union keyed by caseId (rather than two separate maps)
// means a given case is never simultaneously "pending" in one map and
// "active" in another after an Accept -- there is exactly one entry per
// case, and accepting it just replaces its kind in place.
export type PendingAlert = {
  kind: "pending";
  caseId: string;
  conversationId: string;
  projectId?: string;
  subject?: string;
  customerEmail?: string;
  customerName?: string;
  message?: string;
  // ISO 8601 -- when this engineer was assigned this case (from the SSE
  // event's own timestamp for a fresh assignment, or from GetPresence's
  // per-case assignedAt when rehydrating after a refresh). Drives the
  // accept-countdown in EngineerAlertNotification.
  assignedAt: string;
};

export type ActiveSession = {
  kind: "session";
  caseId: string;
  conversationId: string;
  customerName?: string;
  messages: LiveChatMessage[];
};

export type CaseEntry = PendingAlert | ActiveSession;

export interface ChatSessions {
  pendingEntries: PendingAlert[];
  sessionEntries: ActiveSession[];
  draftByCaseId: Record<string, string>;
  convertErrorByCaseId: Record<string, string>;
  // chat-routing-service's configured PENDING_TIMEOUT_SECONDS (always
  // present once presence has loaded once; falls back to
  // DEFAULT_PENDING_TIMEOUT_SECONDS until then).
  pendingTimeoutSeconds: number;
  remainingSecondsFor: (assignedAt: string) => number;
  isAccepting: boolean;
  isDeclining: boolean;
  isSending: boolean;
  isCompleting: boolean;
  isConverting: boolean;
  // Resolves to true if the case became an active session, false if it was
  // removed instead (a 409 -- reassigned/already accepted elsewhere -- or
  // any other failure, which just leaves the alert visible to retry). Never
  // rejects: callers don't need their own try/catch.
  accept: (alert: PendingAlert) => Promise<boolean>;
  dismiss: (alert: PendingAlert) => Promise<void>;
  setDraft: (caseId: string, text: string) => void;
  sendMessage: (session: ActiveSession) => Promise<void>;
  complete: (session: ActiveSession) => Promise<void>;
  convertToCase: (session: ActiveSession) => Promise<void>;
}

const ChatSessionsCtx = createContext<ChatSessions | null>(null);

/**
 * Owns every piece of state and every server call behind the
 * live-engineer-chat feature's engineer side: the one SSE subscription
 * (`useChatAlertsStream`), the case-by-case pending/active state, and every
 * accept/decline/send/complete/convert mutation. Both the small floating
 * pending-alert card (`EngineerAlertNotification`) and the full-page active
 * chat workspace (`ChatWorkspacePage`) read and act through `useChatSessions()`
 * rather than each keeping their own copy -- there is exactly one SSE
 * connection and one source of truth for what an engineer is currently
 * holding, regardless of which of those two UIs is on screen.
 *
 * Mounted in AuthGuard.tsx's AuthorizedAppShell, wrapping both AppLayout and
 * EngineerAlertNotification -- they're siblings there, not parent/child, so
 * the provider has to sit above both for either to reach it. (AppLayout
 * separately wraps its own content in CaseTabsProvider, which solves the
 * same "must survive route navigation" requirement for an unrelated feature
 * -- open case-detail tabs -- but that one only needs to cover AppLayout's
 * own rendered content, not anything outside it.)
 */
export function ChatSessionsProvider({ children }: { children: ReactNode }): JSX.Element {
  const myEmail = useIdTokenClaims()?.email;
  const [casesByCaseId, setCasesByCaseId] = useState<Record<string, CaseEntry>>({});
  const [draftByCaseId, setDraftByCaseId] = useState<Record<string, string>>({});
  // Per-case error text for a failed "Convert to Case" attempt, shown inline
  // instead of silently clearing the session.
  const [convertErrorByCaseId, setConvertErrorByCaseId] = useState<Record<string, string>>({});

  const acceptMutation = useAcceptChatSession();
  const sendMutation = useSendChatMessage();
  const completeMutation = useCompleteChatSession();
  const declineMutation = useDeclineChatSession();
  const convertMutation = useConvertChatToCase();
  const queryClient = useQueryClient();
  const { data: presence } = useGetEngineerStatus();

  const entries = Object.values(casesByCaseId);
  const pendingEntries = entries.filter((e): e is PendingAlert => e.kind === "pending");
  const sessionEntries = entries.filter((e): e is ActiveSession => e.kind === "session");

  // Ticks once a second, only while at least one pending alert is showing,
  // to drive the accept-countdown -- see PendingAlert.assignedAt and
  // presence.pendingTimeoutSeconds (chat-routing-service's configured
  // PENDING_TIMEOUT_SECONDS). Purely a UI countdown: the real timeout is
  // enforced server-side on its own poll cadence (see that service's
  // SweepExpiredPending and csm-portal/backend's StartTimeoutSweeper), so
  // this can briefly read a few seconds past zero before the
  // case_timed_out/session_accepted event for it actually arrives and
  // clears it.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (pendingEntries.length === 0) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [pendingEntries.length]);

  const pendingTimeoutSeconds = presence?.pendingTimeoutSeconds ?? DEFAULT_PENDING_TIMEOUT_SECONDS;
  const remainingSecondsFor = useCallback(
    (assignedAt: string): number =>
      Math.max(0, pendingTimeoutSeconds - Math.floor((now - new Date(assignedAt).getTime()) / 1000)),
    [now, pendingTimeoutSeconds],
  );

  // Removes one case from the cached presence's `cases` array immediately,
  // rather than waiting for the mutation's own invalidateQueries: without
  // this, a stale refetch can resurrect a card that was just dismissed.
  const clearCachedCase = useCallback(
    (caseId: string): void => {
      queryClient.setQueryData<EngineerPresence | undefined>(ENGINEER_STATUS_QUERY_KEY, (prev) =>
        prev ? { ...prev, cases: prev.cases.filter((c) => c.caseId !== caseId) } : prev,
      );
    },
    [queryClient],
  );

  // Rehydrates every case lost after a refresh or a remount of this provider
  // (mounted once in AppLayout, but a full page reload still wipes
  // casesByCaseId, which lives only in this provider's own state). Adds one
  // entry per case in presence.cases that isn't already tracked locally --
  // pending cases (assigned, not yet accepted) become pending alerts,
  // already-accepted cases go straight into an active session with an empty
  // message history (any messages exchanged before the reload are still in
  // the case's comment history server-side, just not replayed into this
  // local transcript). This is what gets an engineer un-stuck who is
  // genuinely still holding one or more cases server-side with nothing left
  // in the UI to act on.
  useEffect(() => {
    if (!presence?.cases?.length) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- syncs local state to cases the server already holds for this engineer (post-refresh rehydration), not state derivable from props/render
    setCasesByCaseId((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const c of presence.cases) {
        if (prev[c.caseId]) continue;
        changed = true;
        next[c.caseId] = c.pending
          ? {
              kind: "pending",
              caseId: c.caseId,
              conversationId: c.conversationId,
              subject: c.subject,
              customerEmail: c.customerEmail,
              customerName: c.customerName,
              message: c.message,
              assignedAt: c.assignedAt,
            }
          : {
              kind: "session",
              caseId: c.caseId,
              conversationId: c.conversationId,
              customerName: c.customerName,
              messages: [],
            };
      }
      return changed ? next : prev;
    });
  }, [presence]);

  const handleAlert = useCallback(
    (event: ChatAlertEvent) => {
      switch (event.type) {
        case "customer_escalation": {
          if (!event.caseId || !event.conversationId) return;
          // Receiving this event at all means the routing service just
          // assigned this case to us -- we hold it server-side from this
          // instant, before Accept is even clicked. The capacity/case-list
          // query has no way to know that on its own (nothing pushes to
          // it), so invalidate it here rather than leaving it stuck showing
          // a stale load count.
          queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
          setCasesByCaseId((current) => {
            if (current[event.caseId as string]) return current;
            return {
              ...current,
              [event.caseId as string]: {
                kind: "pending",
                caseId: event.caseId as string,
                conversationId: event.conversationId as string,
                projectId: event.projectId,
                subject: event.subject,
                customerEmail: event.customerEmail,
                customerName: event.customerName,
                message: event.message,
                assignedAt: event.timestamp,
              },
            };
          });
          break;
        }
        case "session_accepted": {
          // Someone else took this case -- our own accept already
          // transitions us to the active session locally (see accept
          // below), so only clear when a *different* engineer's email
          // comes back.
          if (!event.caseId || event.engineerEmail === myEmail) break;
          setCasesByCaseId((current) => {
            const entry = current[event.caseId as string];
            if (!entry || entry.kind !== "pending") return current;
            const next = { ...current };
            delete next[event.caseId as string];
            return next;
          });
          break;
        }
        case "customer_message": {
          if (!event.caseId) return;
          setCasesByCaseId((current) => {
            const entry = current[event.caseId as string];
            if (!entry || entry.kind !== "session") return current;
            return {
              ...current,
              [event.caseId as string]: {
                ...entry,
                messages: [
                  ...entry.messages,
                  {
                    id: `customer-${event.timestamp}-${entry.messages.length}`,
                    from: "customer",
                    text: event.message ?? "",
                  },
                ],
              },
            };
          });
          break;
        }
        case "session_closed": {
          if (!event.caseId) return;
          setCasesByCaseId((current) => {
            if (!current[event.caseId as string]) return current;
            const next = { ...current };
            delete next[event.caseId as string];
            return next;
          });
          break;
        }
        case "case_timed_out": {
          // We never accepted this one in time -- chat-routing-service
          // already reassigned/requeued it (see that service's
          // SweepExpiredPending). Only ever applies to a still-pending
          // alert, never an active session; any other concurrent case we
          // hold is unaffected, so this only ever removes this one entry.
          if (!event.caseId) return;
          setCasesByCaseId((current) => {
            const entry = current[event.caseId as string];
            if (!entry || entry.kind !== "pending") return current;
            const next = { ...current };
            delete next[event.caseId as string];
            return next;
          });
          queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
          break;
        }
        default:
          break;
      }
    },
    [myEmail, queryClient],
  );

  // Always subscribed (enabled: true) for any signed-in engineer -- there is
  // no per-page opt-in, since an escalation can arrive while an engineer is
  // anywhere in the app. This is the ONE subscription for the whole
  // provider tree -- do not add a second useChatAlertsStream call anywhere
  // else (see this file's own doc comment).
  useChatAlertsStream(true, handleAlert);

  const accept = useCallback(
    async (alert: PendingAlert): Promise<boolean> => {
      const { caseId, conversationId, customerName } = alert;
      try {
        await acceptMutation.mutateAsync({ caseId, conversationId });
        setCasesByCaseId((current) => ({
          ...current,
          [caseId]: { kind: "session", caseId, conversationId, customerName, messages: [] },
        }));
        return true;
      } catch (err) {
        // A 409 means the routing service's Accept check found this case
        // isn't pending-for-this-engineer anymore (see HandleAcceptSession)
        // -- it was declined, reassigned, or already accepted elsewhere
        // while this alert sat on screen. Nothing to retry there, so clear
        // it rather than leaving a stuck "Accept" button that will only
        // ever fail again. Any other failure (network blip, routing service
        // briefly down) leaves the alert visible so the engineer can retry,
        // or another engineer's session_accepted clears it above.
        if (err instanceof BackendApiError && err.status === 409) {
          setCasesByCaseId((current) => {
            const next = { ...current };
            delete next[caseId];
            return next;
          });
        }
        return false;
      }
    },
    [acceptMutation],
  );

  // Unlike complete (clears immediately, decline is best-effort after),
  // this awaits the decline call BEFORE clearing: escalations are routed to
  // exactly one engineer, so dismissing without telling the routing service
  // would otherwise strand the customer with nobody else ever seeing their
  // request (see useDeclineChatSession's own doc comment). State still
  // clears locally even if the call fails -- no worse than an un-routed
  // dismiss.
  const dismiss = useCallback(
    async (alert: PendingAlert): Promise<void> => {
      try {
        await declineMutation.mutateAsync({
          caseId: alert.caseId,
          conversationId: alert.conversationId,
        });
      } catch {
        // Best-effort -- still clear locally below either way.
      }
      setCasesByCaseId((current) => {
        const next = { ...current };
        delete next[alert.caseId];
        return next;
      });
      clearCachedCase(alert.caseId);
    },
    [declineMutation, clearCachedCase],
  );

  const setDraft = useCallback((caseId: string, text: string): void => {
    setDraftByCaseId((current) => ({ ...current, [caseId]: text }));
  }, []);

  const sendMessage = useCallback(
    async (session: ActiveSession): Promise<void> => {
      const text = (draftByCaseId[session.caseId] ?? "").trim();
      if (!text) return;
      const { caseId, conversationId } = session;
      setDraftByCaseId((current) => ({ ...current, [caseId]: "" }));
      setCasesByCaseId((current) => {
        const entry = current[caseId];
        if (!entry || entry.kind !== "session") return current;
        return {
          ...current,
          [caseId]: {
            ...entry,
            messages: [...entry.messages, { id: `engineer-${Date.now()}`, from: "engineer", text }],
          },
        };
      });
      try {
        await sendMutation.mutateAsync({ caseId, conversationId, message: text });
      } catch {
        // Best-effort optimistic send -- a failure just means the customer
        // never saw this one; the engineer can retype it.
      }
    },
    [draftByCaseId, sendMutation],
  );

  const complete = useCallback(
    async (session: ActiveSession): Promise<void> => {
      const { caseId, conversationId } = session;
      setCasesByCaseId((current) => {
        const next = { ...current };
        delete next[caseId];
        return next;
      });
      clearCachedCase(caseId);
      try {
        await completeMutation.mutateAsync({ caseId, conversationId });
      } catch {
        // Best-effort -- state has already cleared locally either way.
      }
    },
    [completeMutation, clearCachedCase],
  );

  // Converts this session into a real case. Unlike complete, this does NOT
  // clear the session locally until the call succeeds, since a failure may
  // mean a case was created but ending the chat failed server-side -- the
  // engineer needs to see and act on that manually.
  const convertToCase = useCallback(
    async (session: ActiveSession): Promise<void> => {
      const { caseId } = session;
      setConvertErrorByCaseId((current) => {
        if (!(caseId in current)) return current;
        const next = { ...current };
        delete next[caseId];
        return next;
      });
      try {
        await convertMutation.mutateAsync({ caseId });
        setCasesByCaseId((current) => {
          const next = { ...current };
          delete next[caseId];
          return next;
        });
        clearCachedCase(caseId);
      } catch (err) {
        const message =
          err instanceof BackendApiError && err.message
            ? err.message
            : "Failed to create a case for this chat. Please try again.";
        setConvertErrorByCaseId((current) => ({ ...current, [caseId]: message }));
      }
    },
    [convertMutation, clearCachedCase],
  );

  const value = useMemo<ChatSessions>(
    () => ({
      pendingEntries,
      sessionEntries,
      draftByCaseId,
      convertErrorByCaseId,
      pendingTimeoutSeconds,
      remainingSecondsFor,
      isAccepting: acceptMutation.isPending,
      isDeclining: declineMutation.isPending,
      isSending: sendMutation.isPending,
      isCompleting: completeMutation.isPending,
      isConverting: convertMutation.isPending,
      accept,
      dismiss,
      setDraft,
      sendMessage,
      complete,
      convertToCase,
    }),
    [
      pendingEntries,
      sessionEntries,
      draftByCaseId,
      convertErrorByCaseId,
      pendingTimeoutSeconds,
      remainingSecondsFor,
      acceptMutation.isPending,
      declineMutation.isPending,
      sendMutation.isPending,
      completeMutation.isPending,
      convertMutation.isPending,
      accept,
      dismiss,
      setDraft,
      sendMessage,
      complete,
      convertToCase,
    ],
  );

  return <ChatSessionsCtx.Provider value={value}>{children}</ChatSessionsCtx.Provider>;
}

export function useChatSessions(): ChatSessions {
  const ctx = useContext(ChatSessionsCtx);
  if (!ctx) {
    throw new Error("useChatSessions must be used within a ChatSessionsProvider");
  }
  return ctx;
}
