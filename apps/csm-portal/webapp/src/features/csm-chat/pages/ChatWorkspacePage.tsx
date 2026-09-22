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
  Chip,
  CircularProgress,
  Paper,
  Stack,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useEffect, useMemo, useRef, useState, type JSX, type KeyboardEvent } from "react";
import {
  useChatSessions,
  type ActiveSession,
} from "@context/chat-sessions/ChatSessionsContext";

/**
 * Full-page workspace for an engineer's accepted live chats -- the large
 * counterpart to the customer's own NoveraChatPage, replacing what used to
 * be a small inline card inside the floating EngineerAlertNotification
 * widget. Reachable from the sidebar's "Chat" nav item (see csmNavItems.ts)
 * at /chat. New, not-yet-accepted requests still show as that same small
 * floating card everywhere (including here) -- only ACCEPTED chats live on
 * this page. See ChatSessionsContext.tsx for the shared state/actions both
 * UIs read from.
 *
 * One tab per active session, since an engineer's concurrent-chat capacity
 * can be more than one (1-10). Tabs are a plain local `selectedCaseId`
 * state, not persisted -- the session list itself already rehydrates from
 * the server (useGetEngineerStatus) on load, same as the old widget did.
 */
export default function ChatWorkspacePage(): JSX.Element {
  const {
    sessionEntries,
    draftByCaseId,
    convertErrorByCaseId,
    isSending,
    isCompleting,
    isConverting,
    setDraft,
    sendMessage,
    complete,
    convertToCase,
  } = useChatSessions();

  const [selectedCaseId, setSelectedCaseId] = useState<string | null>(null);

  // Keeps the selection valid as sessions come and go: picks the first
  // session when nothing is selected yet, and falls back to whatever is
  // left (or null) the moment the selected one disappears (ended,
  // converted, or closed from the other side) -- an engineer switching
  // between two chats should never suddenly land on a stale/blank tab.
  useEffect(() => {
    if (sessionEntries.length === 0) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- syncs the selected tab to sessionEntries, which comes from ChatSessionsContext (an external source), not something derivable during this render
      setSelectedCaseId(null);
      return;
    }
    if (selectedCaseId && sessionEntries.some((s) => s.caseId === selectedCaseId)) return;
    setSelectedCaseId(sessionEntries[0].caseId);
  }, [sessionEntries, selectedCaseId]);

  const selectedSession = useMemo(
    () => sessionEntries.find((s) => s.caseId === selectedCaseId) ?? null,
    [sessionEntries, selectedCaseId],
  );

  const messagesEndRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ block: "end" });
  }, [selectedSession?.messages.length, selectedCaseId]);

  const handleDraftKeyDown =
    (session: ActiveSession) =>
    (e: KeyboardEvent<HTMLDivElement>): void => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        void sendMessage(session);
      }
    };

  if (sessionEntries.length === 0) {
    return (
      <Box
        sx={{
          height: "100%",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 1,
          color: "text.secondary",
        }}
      >
        <Typography variant="h6">No active chats</Typography>
        <Typography variant="body2">
          Accepted live-chat requests will open here. New requests still show as a
          pop-up card until you accept them.
        </Typography>
      </Box>
    );
  }

  return (
    <Box sx={{ height: "100%", display: "flex", flexDirection: "column" }}>
      <Stack
        direction="row"
        spacing={1}
        sx={{ mb: 1.5, flexWrap: "wrap", rowGap: 1 }}
        role="tablist"
        aria-label="Active chats"
      >
        {sessionEntries.map((session) => {
          const active = session.caseId === selectedCaseId;
          return (
            <Chip
              key={session.caseId}
              role="tab"
              aria-selected={active}
              label={session.customerName || "Live chat"}
              onClick={() => setSelectedCaseId(session.caseId)}
              variant={active ? "filled" : "outlined"}
              color={active ? "primary" : "default"}
              sx={{ fontWeight: active ? 600 : 400, maxWidth: 220 }}
            />
          );
        })}
      </Stack>

      {selectedSession && (
        <Paper
          elevation={1}
          sx={{
            flex: 1,
            minHeight: 0,
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
          }}
        >
          <Box sx={{ p: 2, borderBottom: 1, borderColor: "divider" }}>
            <Stack direction="row" alignItems="center" justifyContent="space-between" spacing={2}>
              <Typography variant="h6" noWrap sx={{ pr: 1 }}>
                {selectedSession.customerName || "Live chat"}
              </Typography>
              <Stack direction="row" spacing={1} flexShrink={0}>
                <Button
                  variant="outlined"
                  color="primary"
                  onClick={() => void convertToCase(selectedSession)}
                  disabled={isConverting || isCompleting}
                  startIcon={isConverting ? <CircularProgress size={16} color="inherit" /> : undefined}
                  sx={{ textTransform: "none" }}
                >
                  Convert to Case
                </Button>
                <Button
                  variant="outlined"
                  color="inherit"
                  onClick={() => void complete(selectedSession)}
                  disabled={isCompleting || isConverting}
                  sx={{ textTransform: "none" }}
                >
                  End session
                </Button>
              </Stack>
            </Stack>
            {convertErrorByCaseId[selectedSession.caseId] && (
              <Typography variant="caption" color="error" sx={{ mt: 0.5, display: "block" }}>
                {convertErrorByCaseId[selectedSession.caseId]}
              </Typography>
            )}
          </Box>

          <Box
            sx={{
              flex: 1,
              minHeight: 0,
              overflowY: "auto",
              p: 3,
              display: "flex",
              flexDirection: "column",
              gap: 1.5,
            }}
          >
            {selectedSession.messages.length === 0 ? (
              <Typography variant="body2" color="text.secondary">
                No messages yet — say hello.
              </Typography>
            ) : (
              selectedSession.messages.map((m) => (
                <Box
                  key={m.id}
                  sx={{
                    alignSelf: m.from === "engineer" ? "flex-end" : "flex-start",
                    maxWidth: "70%",
                  }}
                >
                  <Typography
                    variant="body1"
                    sx={{
                      p: 1.5,
                      borderRadius: 2,
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
            <div ref={messagesEndRef} />
          </Box>

          <Box sx={{ p: 2, borderTop: 1, borderColor: "divider" }}>
            <Stack direction="row" spacing={1.5} alignItems="flex-end">
              <TextField
                fullWidth
                placeholder="Type a message..."
                value={draftByCaseId[selectedSession.caseId] ?? ""}
                onChange={(e) => setDraft(selectedSession.caseId, e.target.value)}
                onKeyDown={handleDraftKeyDown(selectedSession)}
                multiline
                maxRows={6}
              />
              <Button
                variant="contained"
                onClick={() => void sendMessage(selectedSession)}
                disabled={!(draftByCaseId[selectedSession.caseId] ?? "").trim() || isSending}
                sx={{ textTransform: "none", height: 40 }}
              >
                Send
              </Button>
            </Stack>
          </Box>
        </Paper>
      )}
    </Box>
  );
}
