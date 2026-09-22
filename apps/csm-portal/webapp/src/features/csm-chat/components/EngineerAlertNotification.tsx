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

import { Box, Button, CircularProgress, IconButton, LinearProgress, Paper, Stack, Typography } from "@wso2/oxygen-ui";
import type { JSX } from "react";
import { useNavigate } from "react-router";
import { useChatSessions, type PendingAlert } from "@context/chat-sessions/ChatSessionsContext";

/**
 * App-wide floating widget for brand-new, not-yet-accepted live-engineer-chat
 * requests -- see csm-portal/backend's internal/handler/chat.go for the full
 * design. Mounted once in AuthGuard.tsx so it's visible on every page for any
 * signed-in engineer, independent of which route they're on: a pending
 * request carries a short accept/decline countdown and must never depend on
 * the engineer happening to be on a particular page to see it.
 *
 * Once a request is accepted, it's no longer rendered here -- it moves to
 * the full-page chat workspace at /chat (see ChatWorkspacePage.tsx), sized
 * like the customer's own chat page rather than a corner popup. Both this
 * widget and that page read the same pending/active case state through
 * useChatSessions() (ChatSessionsContext.tsx), which also owns the single
 * SSE subscription and every accept/decline/send/complete/convert call --
 * this component only renders pending cards and reports clicks.
 *
 * Renders nothing (returns null) whenever there are no pending requests, so
 * it never occupies space or blocks a click when idle.
 */
export default function EngineerAlertNotification(): JSX.Element | null {
  const navigate = useNavigate();
  const {
    pendingEntries,
    pendingTimeoutSeconds,
    remainingSecondsFor,
    isAccepting,
    isDeclining,
    accept,
    dismiss,
  } = useChatSessions();

  if (pendingEntries.length === 0) return null;

  const handleAccept = async (alert: PendingAlert): Promise<void> => {
    const accepted = await accept(alert);
    // Take the engineer straight to the conversation rather than making
    // them find the new tab themselves -- see ChatWorkspacePage.tsx.
    if (accepted) navigate("/chat");
  };

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
                  onClick={() => void dismiss(pending)}
                  disabled={isAccepting || isDeclining}
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
                  disabled={isAccepting}
                  startIcon={isAccepting ? <CircularProgress size={14} color="inherit" /> : undefined}
                  sx={{ textTransform: "none" }}
                >
                  Accept
                </Button>
                <Button
                  variant="text"
                  size="small"
                  onClick={() => void dismiss(pending)}
                  disabled={isAccepting || isDeclining}
                  sx={{ textTransform: "none" }}
                >
                  Dismiss
                </Button>
              </Stack>
            </Box>
          </Paper>
        );
      })}
    </Stack>
  );
}
