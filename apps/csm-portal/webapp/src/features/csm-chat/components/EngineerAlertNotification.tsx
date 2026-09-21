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
  Button,
  CircularProgress,
  IconButton,
  LinearProgress,
  Paper,
  Stack,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useCallback, useEffect, useState, type JSX, type KeyboardEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { BackendApiError } from "@api/backend/client";
import { useIdTokenClaims } from "@hooks/useIdTokenClaims";
import { useChatAlertsStream } from "@features/csm-chat/api/useChatAlertsStream";
import { DEFAULT_PENDING_TIMEOUT_SECONDS } from "@features/csm-chat/api/useEngineerStatus";
import { useAcceptChatSession } from "@features/csm-chat/api/useAcceptChatSession";
import { useSendChatMessage } from "@features/csm-chat/api/useSendChatMessage";
import { useCompleteChatSession } from "@features/csm-chat/api/useCompleteChatSession";
import { useDeclineChatSession } from "@features/csm-chat/api/useDeclineChatSession";
import { useConvertChatToCase } from "@features/csm-chat/api/useConvertChatToCase";
import {
  ENGINEER_STATUS_QUERY_KEY,
  useGetEngineerStatus,
  type EngineerPresence,
} from "@features/csm-chat/api/useEngineerStatus";
import type { ChatAlertEvent } from "@features/csm-chat/types/chatAlerts";

type LiveChatMessage = {
  id: string;
  from: "customer" | "engineer";
  text: string;
};

// A pending alert (assigned, not yet accepted) or an already-accepted
// active session, keyed by caseId in casesByCaseId below -- an engineer can
// hold several of either kind at once (up to their own configurable
// capacity), so this widget renders a stack of cards rather than at most
// one of each. A single discriminated union keyed by caseId (rather than
// two separate maps) means a given case is never simultaneously "pending"
// in one map and "active" in another after an Accept -- there is exactly
// one entry per case, and accepting it just replaces its kind in place.
type PendingAlert = {
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
  // accept-countdown below.
  assignedAt: string;
};

type ActiveSession = {
  kind: "session";
  caseId: string;
  conversationId: string;
  customerName?: string;
  messages: LiveChatMessage[];
};

type CaseEntry = PendingAlert | ActiveSession;

/**
 * App-wide floating widget for the live-engineer-chat escalation feature —
 * see csm-portal/backend's internal/handler/chat.go for the full design.
 * Mounted once in AuthGuard.tsx so it's visible on every page for any
 * signed-in engineer, independent of which route they're on.
 *
 * Renders a stack of cards -- one per case this engineer currently holds,
 * pending or accepted alike (see casesByCaseId) -- since an engineer's
 * concurrent-chat capacity can be more than one. Renders nothing (returns
 * null) whenever there are no cases at all, so it never occupies space or
 * blocks a click when idle.
 */
export default function EngineerAlertNotification(): JSX.Element | null {
  const myEmail = useIdTokenClaims()?.email;
  const [casesByCaseId, setCasesByCaseId] = useState<Record<string, CaseEntry>>({});
  const [draftByCaseId, setDraftByCaseId] = useState<Record<string, string>>({});
  // Per-case error text for a failed "Convert to Case" attempt, shown
  // inline instead of silently clearing the session.
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
  // to drive each card's accept-countdown below -- see PendingAlert.
  // assignedAt and presence.pendingTimeoutSeconds (chat-routing-service's
  // configured PENDING_TIMEOUT_SECONDS). Purely a UI countdown: the real
  // timeout is enforced server-side on its own poll cadence (see that
  // service's SweepExpiredPending and csm-portal/backend's
  // StartTimeoutSweeper), so this can briefly read a few seconds past zero
  // before the case_timed_out/session_accepted event for it actually
  // arrives and clears it.
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

  // Rehydrates every case lost after a refresh or a remount of this
  // component (it's mounted once in AuthGuard, but a full page reload
  // still wipes casesByCaseId, which lives only in this component's own
  // state — see the doc comment on this whole component). Adds one entry
  // per case in presence.cases that isn't already tracked locally --
  // pending cases (assigned, not yet accepted) become pending alerts,
  // already-accepted cases go straight into an active session with an
  // empty message history (any messages exchanged before the reload are
  // still in the case's comment history server-side, just not replayed
  // into this local transcript). This is what gets an engineer un-stuck
  // who is genuinely still holding one or more cases server-side with
  // nothing left in the UI to act on.
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
          // assigned this case to us — we hold it server-side from this
          // instant, before Accept is even clicked. The capacity/case-list
          // query has no way to know that on its own (nothing pushes to
          // it), so invalidate it here rather than leaving it stuck
          // showing a stale load count.
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
          // Someone else took this case — our own accept already
          // transitions us to the active session locally (see
          // handleAccept), so only clear when a *different* engineer's
          // email comes back.
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

  // Always subscribed (enabled: true) for any signed-in engineer — there is
  // no per-page opt-in, since an escalation can arrive while an engineer is
  // anywhere in the app.
  useChatAlertsStream(true, handleAlert);

  const handleAccept = useCallback(
    async (alert: PendingAlert): Promise<void> => {
      const { caseId, conversationId, customerName } = alert;
      try {
        await acceptMutation.mutateAsync({ caseId, conversationId });
        setCasesByCaseId((current) => ({
          ...current,
          [caseId]: { kind: "session", caseId, conversationId, customerName, messages: [] },
        }));
      } catch (err) {
        // A 409 means the routing service's Accept check found this case
        // isn't pending-for-this-engineer anymore (see HandleAcceptSession)
        // — it was declined, reassigned, or already accepted elsewhere
        // while this alert sat on screen. Nothing to retry there, so clear
        // it rather than leaving a stuck "Accept" button that will only
        // ever fail again. Any other failure (network blip, routing
        // service briefly down) leaves the alert visible so the engineer
        // can retry, or another engineer's session_accepted clears it
        // above.
        if (err instanceof BackendApiError && err.status === 409) {
          setCasesByCaseId((current) => {
            const next = { ...current };
            delete next[caseId];
            return next;
          });
        }
      }
    },
    [acceptMutation],
  );

  // Unlike handleComplete (clears immediately, decline is best-effort after),
  // this awaits the decline call BEFORE clearing: escalations are routed to
  // exactly one engineer, so dismissing without telling the routing service
  // would otherwise strand the customer with nobody else ever seeing their
  // request (see useDeclineChatSession's own doc comment). The widget still
  // clears locally even if the call fails — no worse than today's un-routed
  // dismiss.
  const handleDismiss = useCallback(
    async (alert: PendingAlert): Promise<void> => {
      try {
        await declineMutation.mutateAsync({
          caseId: alert.caseId,
          conversationId: alert.conversationId,
        });
      } catch {
        // Best-effort — still clear locally below either way.
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

  const handleDraftChange = useCallback((caseId: string, text: string): void => {
    setDraftByCaseId((current) => ({ ...current, [caseId]: text }));
  }, []);

  const handleSend = useCallback(
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
        // Best-effort optimistic send — a failure just means the customer
        // never saw this one; the engineer can retype it.
      }
    },
    [draftByCaseId, sendMutation],
  );

  const handleComplete = useCallback(
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
        // Best-effort — the widget has already cleared locally either way.
      }
    },
    [completeMutation, clearCachedCase],
  );

  // Converts this session into a real case. Unlike handleComplete, this
  // does NOT clear the session locally until the call succeeds, since a
  // failure may mean a case was created but ending the chat failed
  // server-side -- the engineer needs to see and act on that manually.
  const handleConvertToCase = useCallback(
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

  const handleDraftKeyDown = useCallback(
    (session: ActiveSession) =>
      (e: KeyboardEvent<HTMLDivElement>): void => {
        if (e.key === "Enter" && !e.shiftKey) {
          e.preventDefault();
          void handleSend(session);
        }
      },
    [handleSend],
  );

  if (entries.length === 0) return null;

  return (
    <Stack
      spacing={1.5}
      sx={{
        position: "fixed",
        bottom: 24,
        right: 24,
        width: 340,
        maxWidth: "calc(100vw - 48px)",
        maxHeight: "calc(100vh - 48px)",
        overflowY: "auto",
        zIndex: 1400,
      }}
    >
      {pendingEntries.map((pending) => {
        const remainingSeconds = remainingSecondsFor(pending.assignedAt);
        return (
          <Paper key={pending.caseId} elevation={4} sx={{ overflow: "hidden" }}>
            <Box sx={{ p: 2 }}>
              <Stack direction="row" alignItems="flex-start" justifyContent="space-between">
                <Typography variant="subtitle2" fontWeight={600}>
                  Live engineer requested
                </Typography>
                <IconButton
                  size="small"
                  aria-label="Dismiss"
                  onClick={() => void handleDismiss(pending)}
                  disabled={acceptMutation.isPending || declineMutation.isPending}
                >
                  <Typography component="span" sx={{ fontSize: "1rem", lineHeight: 1 }}>
                    &times;
                  </Typography>
                </IconButton>
              </Stack>
              <Box sx={{ mt: 1 }}>
                <LinearProgress
                  variant="determinate"
                  value={Math.min(100, (remainingSeconds / pendingTimeoutSeconds) * 100)}
                  color={remainingSeconds <= 10 ? "warning" : "primary"}
                  sx={{ height: 4, borderRadius: 2 }}
                />
                <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: "block" }}>
                  {remainingSeconds > 0
                    ? `Auto-reassigns in ${remainingSeconds}s if not accepted`
                    : "Reassigning any moment…"}
                </Typography>
              </Box>
              <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                {pending.customerName || pending.customerEmail || "A customer"} is asking to
                talk to a live engineer.
              </Typography>
              {pending.message && (
                <Typography
                  variant="body2"
                  sx={{
                    mt: 1,
                    p: 1,
                    bgcolor: "action.hover",
                    borderRadius: 1,
                    whiteSpace: "pre-wrap",
                    overflowWrap: "anywhere",
                  }}
                >
                  {pending.message}
                </Typography>
              )}
              <Stack direction="row" spacing={1} sx={{ mt: 1.5 }}>
                <Button
                  variant="contained"
                  color="primary"
                  size="small"
                  onClick={() => void handleAccept(pending)}
                  disabled={acceptMutation.isPending}
                  startIcon={
                    acceptMutation.isPending ? <CircularProgress size={14} color="inherit" /> : undefined
                  }
                  sx={{ textTransform: "none" }}
                >
                  Accept
                </Button>
                <Button
                  variant="text"
                  size="small"
                  onClick={() => void handleDismiss(pending)}
                  disabled={acceptMutation.isPending || declineMutation.isPending}
                  sx={{ textTransform: "none" }}
                >
                  Dismiss
                </Button>
              </Stack>
            </Box>
          </Paper>
        );
      })}

      {sessionEntries.map((session) => (
        <Paper key={session.caseId} elevation={4} sx={{ display: "flex", flexDirection: "column", overflow: "hidden" }}>
          <Box sx={{ p: 1.5, borderBottom: 1, borderColor: "divider" }}>
            <Stack direction="row" alignItems="center" justifyContent="space-between" spacing={1}>
              <Typography variant="subtitle2" fontWeight={600} noWrap sx={{ pr: 1 }}>
                {session.customerName || "Live chat"}
              </Typography>
              <Stack direction="row" spacing={1} flexShrink={0}>
                <Button
                  size="small"
                  variant="outlined"
                  color="primary"
                  onClick={() => void handleConvertToCase(session)}
                  disabled={convertMutation.isPending || completeMutation.isPending}
                  startIcon={
                    convertMutation.isPending ? <CircularProgress size={14} color="inherit" /> : undefined
                  }
                  sx={{ textTransform: "none" }}
                >
                  Convert to Case
                </Button>
                <Button
                  size="small"
                  variant="outlined"
                  color="inherit"
                  onClick={() => void handleComplete(session)}
                  disabled={completeMutation.isPending || convertMutation.isPending}
                  sx={{ textTransform: "none" }}
                >
                  End session
                </Button>
              </Stack>
            </Stack>
            {convertErrorByCaseId[session.caseId] && (
              <Typography variant="caption" color="error" sx={{ mt: 0.5, display: "block" }}>
                {convertErrorByCaseId[session.caseId]}
              </Typography>
            )}
          </Box>
          <Box
            sx={{
              maxHeight: 260,
              overflowY: "auto",
              p: 1.5,
              display: "flex",
              flexDirection: "column",
              gap: 1,
            }}
          >
            {session.messages.length === 0 ? (
              <Typography variant="caption" color="text.secondary">
                No messages yet — say hello.
              </Typography>
            ) : (
              session.messages.map((m) => (
                <Box
                  key={m.id}
                  sx={{
                    alignSelf: m.from === "engineer" ? "flex-end" : "flex-start",
                    maxWidth: "85%",
                  }}
                >
                  <Typography
                    variant="body2"
                    sx={{
                      p: 1,
                      borderRadius: 1,
                      whiteSpace: "pre-wrap",
                      overflowWrap: "anywhere",
                      bgcolor: m.from === "engineer" ? "primary.main" : "action.hover",
                      color: m.from === "engineer" ? "primary.contrastText" : "text.primary",
                    }}
                  >
                    {m.text}
                  </Typography>
                </Box>
              ))
            )}
          </Box>
          <Box sx={{ p: 1.5, borderTop: 1, borderColor: "divider" }}>
            <Stack direction="row" spacing={1}>
              <TextField
                size="small"
                fullWidth
                placeholder="Type a message..."
                value={draftByCaseId[session.caseId] ?? ""}
                onChange={(e) => handleDraftChange(session.caseId, e.target.value)}
                onKeyDown={handleDraftKeyDown(session)}
                multiline
                maxRows={3}
              />
              <Button
                variant="contained"
                size="small"
                onClick={() => void handleSend(session)}
                disabled={!(draftByCaseId[session.caseId] ?? "").trim() || sendMutation.isPending}
                sx={{ textTransform: "none" }}
              >
                Send
              </Button>
            </Stack>
          </Box>
        </Paper>
      ))}
    </Stack>
  );
}
