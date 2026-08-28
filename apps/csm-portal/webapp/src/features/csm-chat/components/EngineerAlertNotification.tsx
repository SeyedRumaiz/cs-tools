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
  Paper,
  Stack,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useCallback, useState, type JSX, type KeyboardEvent } from "react";
import { useIdTokenClaims } from "@hooks/useIdTokenClaims";
import { useChatAlertsStream } from "@features/csm-chat/api/useChatAlertsStream";
import { useAcceptChatSession } from "@features/csm-chat/api/useAcceptChatSession";
import { useSendChatMessage } from "@features/csm-chat/api/useSendChatMessage";
import { useCompleteChatSession } from "@features/csm-chat/api/useCompleteChatSession";
import type { ChatAlertEvent } from "@features/csm-chat/types/chatAlerts";

type PendingAlert = {
  caseId: string;
  conversationId: string;
  projectId?: string;
  subject?: string;
  customerEmail?: string;
  customerName?: string;
  message?: string;
};

type LiveChatMessage = {
  id: string;
  from: "customer" | "engineer";
  text: string;
};

type ActiveSession = {
  caseId: string;
  conversationId: string;
  customerName?: string;
  messages: LiveChatMessage[];
};

/**
 * App-wide floating widget for the live-engineer-chat escalation feature —
 * see csm-portal/backend's internal/handler/chat.go for the full design.
 * Mounted once in AuthGuard.tsx (alongside IdleTimeoutProvider) so it's
 * visible on every page for any signed-in engineer, independent of which
 * route they're on.
 *
 * Holds exactly one pending alert and one active session at a time — a
 * deliberate simplification matching this feature's other accepted
 * shortcuts (see the backend doc comment: no queue, no claim/lock table).
 * Any escalation beyond the one currently shown is silently ignored by
 * handleAlert until the current one clears; at this project's current
 * scale (a handful of engineers) that is an acceptable limitation, not a
 * bug — a fuller "chat inbox" is a natural follow-up if this ever matters.
 *
 * Renders nothing (returns null) whenever there is neither a pending alert
 * nor an active session, so it never occupies space or blocks a click when
 * idle.
 */
export default function EngineerAlertNotification(): JSX.Element | null {
  const myEmail = useIdTokenClaims()?.email;
  const [pending, setPending] = useState<PendingAlert | null>(null);
  const [session, setSession] = useState<ActiveSession | null>(null);
  const [messageDraft, setMessageDraft] = useState("");

  const acceptMutation = useAcceptChatSession();
  const sendMutation = useSendChatMessage();
  const completeMutation = useCompleteChatSession();

  const handleAlert = useCallback(
    (event: ChatAlertEvent) => {
      switch (event.type) {
        case "customer_escalation": {
          if (!event.caseId || !event.conversationId) return;
          // Ignore new escalations while already showing one, or while a
          // session is live — see this component's own doc comment.
          if (session) return;
          setPending((current) => {
            if (current) return current;
            return {
              caseId: event.caseId as string,
              conversationId: event.conversationId as string,
              projectId: event.projectId,
              subject: event.subject,
              customerEmail: event.customerEmail,
              customerName: event.customerName,
              message: event.message,
            };
          });
          break;
        }
        case "session_accepted": {
          // Someone else took this case — our own accept already
          // transitions us to the active session locally (see
          // handleAccept), so only clear when a *different* engineer's
          // email comes back.
          setPending((current) =>
            current &&
            current.caseId === event.caseId &&
            event.engineerEmail !== myEmail
              ? null
              : current,
          );
          break;
        }
        case "customer_message": {
          setSession((current) => {
            if (!current || current.caseId !== event.caseId) return current;
            return {
              ...current,
              messages: [
                ...current.messages,
                {
                  id: `customer-${event.timestamp}-${current.messages.length}`,
                  from: "customer",
                  text: event.message ?? "",
                },
              ],
            };
          });
          break;
        }
        case "session_closed": {
          setSession((current) =>
            current && current.caseId === event.caseId ? null : current,
          );
          setPending((current) =>
            current && current.caseId === event.caseId ? null : current,
          );
          break;
        }
        default:
          break;
      }
    },
    [session, myEmail],
  );

  // Always subscribed (enabled: true) for any signed-in engineer — there is
  // no per-page opt-in, since an escalation can arrive while an engineer is
  // anywhere in the app. A no-op when CSM_PORTAL_CHAT_STREAM_BASE_URL isn't
  // configured — see useChatAlertsStream's own doc comment.
  useChatAlertsStream(true, handleAlert);

  const handleAccept = useCallback(async (): Promise<void> => {
    if (!pending) return;
    const { caseId, conversationId, customerName } = pending;
    try {
      await acceptMutation.mutateAsync({ caseId, conversationId });
      setSession({ caseId, conversationId, customerName, messages: [] });
      setPending(null);
    } catch {
      // Leave the alert visible so the engineer can retry, or another
      // engineer picks it up (their session_accepted clears it above).
    }
  }, [pending, acceptMutation]);

  const handleDismiss = useCallback((): void => {
    setPending(null);
  }, []);

  const handleSend = useCallback(async (): Promise<void> => {
    const text = messageDraft.trim();
    if (!text || !session) return;
    setMessageDraft("");
    const { caseId, conversationId } = session;
    setSession((current) =>
      current
        ? {
            ...current,
            messages: [
              ...current.messages,
              { id: `engineer-${Date.now()}`, from: "engineer", text },
            ],
          }
        : current,
    );
    try {
      await sendMutation.mutateAsync({ caseId, conversationId, message: text });
    } catch {
      // Best-effort optimistic send — a failure just means the customer
      // never saw this one; the engineer can retype it.
    }
  }, [messageDraft, session, sendMutation]);

  const handleComplete = useCallback(async (): Promise<void> => {
    if (!session) return;
    const { caseId, conversationId } = session;
    setSession(null);
    try {
      await completeMutation.mutateAsync({ caseId, conversationId });
    } catch {
      // Best-effort — the widget has already cleared locally either way.
    }
  }, [session, completeMutation]);

  const handleDraftKeyDown = useCallback(
    (e: KeyboardEvent<HTMLDivElement>): void => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        void handleSend();
      }
    },
    [handleSend],
  );

  if (!pending && !session) return null;

  return (
    <Paper
      elevation={4}
      sx={{
        position: "fixed",
        bottom: 24,
        right: 24,
        width: 340,
        maxWidth: "calc(100vw - 48px)",
        zIndex: 1400,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
      }}
    >
      {pending && (
        <Box sx={{ p: 2 }}>
          <Stack direction="row" alignItems="flex-start" justifyContent="space-between">
            <Typography variant="subtitle2" fontWeight={600}>
              Live engineer requested
            </Typography>
            <IconButton
              size="small"
              aria-label="Dismiss"
              onClick={handleDismiss}
              disabled={acceptMutation.isPending}
            >
              <Typography component="span" sx={{ fontSize: "1rem", lineHeight: 1 }}>
                &times;
              </Typography>
            </IconButton>
          </Stack>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
            {pending.customerName || pending.customerEmail || "A customer"} is
            asking to talk to a live engineer.
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
              onClick={() => void handleAccept()}
              disabled={acceptMutation.isPending}
              startIcon={
                acceptMutation.isPending ? (
                  <CircularProgress size={14} color="inherit" />
                ) : undefined
              }
              sx={{ textTransform: "none" }}
            >
              Accept
            </Button>
            <Button
              variant="text"
              size="small"
              onClick={handleDismiss}
              disabled={acceptMutation.isPending}
              sx={{ textTransform: "none" }}
            >
              Dismiss
            </Button>
          </Stack>
        </Box>
      )}

      {session && (
        <>
          <Box sx={{ p: 1.5, borderBottom: 1, borderColor: "divider" }}>
            <Stack direction="row" alignItems="center" justifyContent="space-between">
              <Typography variant="subtitle2" fontWeight={600} noWrap sx={{ pr: 1 }}>
                {session.customerName || "Live chat"}
              </Typography>
              <Button
                size="small"
                variant="outlined"
                color="inherit"
                onClick={() => void handleComplete()}
                disabled={completeMutation.isPending}
                sx={{ textTransform: "none" }}
              >
                End session
              </Button>
            </Stack>
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
                      bgcolor:
                        m.from === "engineer" ? "primary.main" : "action.hover",
                      color:
                        m.from === "engineer"
                          ? "primary.contrastText"
                          : "text.primary",
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
                value={messageDraft}
                onChange={(e) => setMessageDraft(e.target.value)}
                onKeyDown={handleDraftKeyDown}
                multiline
                maxRows={3}
              />
              <Button
                variant="contained"
                size="small"
                onClick={() => void handleSend()}
                disabled={!messageDraft.trim() || sendMutation.isPending}
                sx={{ textTransform: "none" }}
              >
                Send
              </Button>
            </Stack>
          </Box>
        </>
      )}
    </Paper>
  );
}
