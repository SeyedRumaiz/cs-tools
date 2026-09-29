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
  useRef,
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
import type { ChatAlertEvent, PriorMessage } from "@features/csm-chat/types/chatAlerts";

export type LiveChatMessage = {
  id: string;
  // "assistant" is Novera's own reply, replayed from the customer's prior
  // AI-chatbot transcript at escalation time (see PendingAlert.
  // priorMessages/ActiveSession.priorMessageCount below) -- it never
  // appears in a live post-escalation message, only in that seeded
  // history.
  from: "customer" | "engineer" | "assistant";
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
  // The customer's AI-chatbot (Novera) transcript snapshotted at
  // escalation time -- carried on the pending alert so accept() below can
  // seed the resulting ActiveSession's messages with it.
  priorMessages?: PriorMessage[];
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
  // How many of the entries at the front of `messages` are replayed prior
  // AI-chatbot history (seeded once, at accept() time) rather than part of
  // the live post-escalation conversation -- lets the chat UI draw a
  // divider between the two instead of presenting them as one
  // continuous thread. 0/undefined for a session rehydrated after a page
  // refresh (messages start empty either way -- see the rehydration effect
  // below's own doc comment).
  priorMessageCount?: number;
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

  // TEMPORARY diagnostic mirror for the "accepted but no chat appears"
  // investigation (2026-09) -- lets handleAlert log the pre-event local
  // state without a stale closure or adding casesByCaseId as a dependency
  // of handleAlert (which would churn the useChatAlertsStream subscription).
  // Remove once root-caused.
  const casesByCaseIdRef = useRef<Record<string, CaseEntry>>(casesByCaseId);
  useEffect(() => {
    casesByCaseIdRef.current = casesByCaseId;
  }, [casesByCaseId]);

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
  // already-accepted cases go straight into an active session seeded from
  // the same c.priorMessages field (see the "session" branch below for why
  // that's not just the pre-escalation snapshot for an already-accepted
  // case). This is what gets an engineer un-stuck who is genuinely still
  // holding one or more cases server-side with nothing left in the UI to
  // act on.
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
              priorMessages: c.priorMessages,
              assignedAt: c.assignedAt,
            }
          : {
              // Already-accepted sessions used to rehydrate with an empty
              // transcript -- reported live as "on refresh all the messages
              // disappeared". They don't need to: for an accepted case,
              // chat-routing-service's c.priorMessages is no longer just the
              // pre-escalation snapshot, it's commentsForWorkItem's live
              // read-back of chat_routing.comment (see that function's own
              // doc comment), which AddComment keeps appending to for every
              // message either side sends after acceptance too. So it's
              // really "the transcript so far", and gets mapped into
              // messages the same way accept() below seeds a fresh session.
              //
              // Known gap, not fixed here: commentsForWorkItem can only
              // tell "the Novera assistant" apart from "everyone else" (see
              // its own doc comment) -- it has no way to know a given live
              // message came from the engineer rather than the customer, so
              // any live engineer reply already sent before this refresh
              // replays as a left-aligned "customer" bubble instead of the
              // engineer's own. Fixing that needs commentsForWorkItem to
              // also compare created_by against the assigned engineer's own
              // email. Not losing the transcript at all is still a strict
              // improvement over today.
              kind: "session",
              caseId: c.caseId,
              conversationId: c.conversationId,
              customerName: c.customerName,
              messages: (c.priorMessages ?? []).map((m, i) => ({
                id: `prior-${c.caseId}-${i}`,
                from: m.role === "assistant" ? "assistant" : "customer",
                text: m.content,
              })),
              // The whole restored transcript is treated as "prior" content
              // rather than trying to guess where live chat resumes -- see
              // this branch's own doc comment above. ChatWorkspacePage's
              // "Live chat started" divider only renders when
              // priorMessageCount is strictly less than messages.length, so
              // this just means no divider shows on a rehydrated session,
              // not that anything is mislabeled.
              priorMessageCount: c.priorMessages?.length,
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
                priorMessages: event.priorMessages,
                assignedAt: event.timestamp,
              },
            };
          });
          break;
        }
        case "session_accepted": {
          // TEMPORARY diagnostic logging (2026-09 "accepted but no chat
          // appears" investigation) -- logs arrival time, the identity
          // comparison this handler gates on, and the local entry's kind at
          // the moment this event is processed. Remove once root-caused.
          // eslint-disable-next-line no-console
          console.log(
            `[ACCEPT-DEBUG] SSE session_accepted ARRIVED caseId=${event.caseId} engineerEmail=${JSON.stringify(event.engineerEmail)} myEmail=${JSON.stringify(myEmail)} emailsMatch=${event.engineerEmail === myEmail} beforeKind=${event.caseId ? casesByCaseIdRef.current[event.caseId]?.kind : undefined} t=${new Date().toISOString()}`,
          );
          // Someone else took this case -- our own accept already
          // transitions us to the active session locally (see accept
          // below), so only clear when a *different* engineer's email
          // comes back.
          if (!event.caseId || event.engineerEmail === myEmail) break;
          setCasesByCaseId((current) => {
            const entry = current[event.caseId as string];
            if (!entry || entry.kind !== "pending") return current;
            // eslint-disable-next-line no-console
            console.log(
              `[ACCEPT-DEBUG] SSE session_accepted DELETING pending entry caseId=${event.caseId} t=${new Date().toISOString()}`,
            );
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
          // TEMPORARY diagnostic logging (2026-09 investigation) -- this
          // handler deletes ANY entry (pending or session) unconditionally
          // by caseId, so it's the other candidate mechanism for a
          // just-accepted session vanishing. Remove once root-caused.
          // eslint-disable-next-line no-console
          console.log(
            `[ACCEPT-DEBUG] SSE session_closed ARRIVED caseId=${event.caseId} engineerEmail=${JSON.stringify(event.engineerEmail)} beforeKind=${event.caseId ? casesByCaseIdRef.current[event.caseId]?.kind : undefined} t=${new Date().toISOString()}`,
          );
          if (!event.caseId) return;
          setCasesByCaseId((current) => {
            if (!current[event.caseId as string]) return current;
            // eslint-disable-next-line no-console
            console.log(
              `[ACCEPT-DEBUG] SSE session_closed DELETING entry caseId=${event.caseId} kind=${current[event.caseId as string]?.kind} t=${new Date().toISOString()}`,
            );
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
      const { caseId, conversationId, customerName, priorMessages } = alert;
      // TEMPORARY diagnostic logging (2026-09 investigation). Remove once
      // root-caused.
      // eslint-disable-next-line no-console
      console.log(
        `[ACCEPT-DEBUG] accept() CALLED caseId=${caseId} beforeKind=${casesByCaseIdRef.current[caseId]?.kind} t=${new Date().toISOString()}`,
      );
      try {
        await acceptMutation.mutateAsync({ caseId, conversationId });
        // eslint-disable-next-line no-console
        console.log(
          `[ACCEPT-DEBUG] accept() mutateAsync RESOLVED caseId=${caseId} kindAtResolve=${casesByCaseIdRef.current[caseId]?.kind} t=${new Date().toISOString()}`,
        );
        // Seed the new session with the customer's prior AI-chatbot
        // (Novera) transcript, if any, so the engineer opens the chat
        // already knowing what the customer asked -- see PendingAlert.
        // priorMessages and ActiveSession.priorMessageCount. m.role is
        // already "customer" or "assistant" (see chatAlerts.ts's
        // PriorMessage) -- no more author-string heuristic needed.
        const seededMessages: LiveChatMessage[] = (priorMessages ?? []).map((m, i) => ({
          id: `prior-${caseId}-${i}`,
          from: m.role === "assistant" ? "assistant" : "customer",
          text: m.content,
        }));
        setCasesByCaseId((current) => {
          // eslint-disable-next-line no-console
          console.log(
            `[ACCEPT-DEBUG] accept() WRITING session entry caseId=${caseId} kindJustBeforeWrite=${current[caseId]?.kind} t=${new Date().toISOString()}`,
          );
          return {
            ...current,
            [caseId]: {
              kind: "session",
              caseId,
              conversationId,
              customerName,
              messages: seededMessages,
              priorMessageCount: seededMessages.length,
            },
          };
        });
        // eslint-disable-next-line no-console
        console.log(`[ACCEPT-DEBUG] accept() SUCCESS RETURN caseId=${caseId} t=${new Date().toISOString()}`);
        return true;
      } catch (err) {
        // eslint-disable-next-line no-console
        console.log(
          `[ACCEPT-DEBUG] accept() CAUGHT ERROR caseId=${caseId} status=${err instanceof BackendApiError ? err.status : "n/a"} err=${String(err)} t=${new Date().toISOString()}`,
        );
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
