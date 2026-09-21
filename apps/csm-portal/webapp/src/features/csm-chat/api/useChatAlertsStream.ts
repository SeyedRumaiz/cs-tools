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

import { useEffect, useRef } from "react";
import EventSourcePolyfill from "@sanity/eventsource";
import { apiConfig } from "@config/apiConfig";
import { useAuthTokens } from "@hooks/useAuthTokens";
import { useLogger } from "@hooks/useLogger";
import type { ChatAlertEvent } from "@features/csm-chat/types/chatAlerts";

/** Base delay before the first reconnect attempt after the stream errors out or drops. */
const RECONNECT_BASE_DELAY_MS = 3_000;
/** Reconnect delay never grows past this, no matter how many consecutive failures. */
const RECONNECT_MAX_DELAY_MS = 30_000;

/**
 * Exponential backoff with full jitter — see useCaseActivityStream.ts's own
 * copy of this helper for the full rationale (kept duplicated rather than
 * shared: the two hooks connect to unrelated streams and have no other
 * reason to be coupled).
 */
function reconnectDelay(attempt: number): number {
  const capped = Math.min(RECONNECT_MAX_DELAY_MS, RECONNECT_BASE_DELAY_MS * 2 ** attempt);
  return Math.random() * capped;
}

/**
 * Opens a live Server-Sent Events connection to csm-portal-backend's
 * GET /chat/alerts/stream and calls onAlert for every `chat_alert` event it
 * receives, for as long as this connection stays open.
 *
 * Unlike useCaseActivityStream.ts's GET /cases/{id}/activities/stream (a
 * separate, optional Choreo endpoint backed by its own dedicated :9092
 * listener), /chat/alerts/stream is registered on csm-portal/backend's main
 * API listener alongside its browser-facing routes (see that backend's
 * cmd/server/main.go) — this feature never stands up a separate 9094/9095
 * listener or config key, so this hook connects to the same
 * apiConfig.backendUrl every other REST call in this app uses.
 *
 * Structurally this borrows useCaseActivityStream.ts's reconnect/backoff/
 * token-refresh machinery (copied rather than shared — the two streams are
 * unrelated and evolving them independently is simpler than a shared
 * abstraction over two different auth/event shapes). See that hook's own
 * doc comment for why this needs @sanity/eventsource rather than the
 * browser's native EventSource (custom auth headers) and why tokens are
 * re-fetched on every reconnect rather than relying on the polyfill's own
 * built-in retry.
 *
 * onAlert is read through a ref, not a hook dependency — it can be a fresh
 * closure every render (e.g. an inline arrow function in
 * EngineerAlertNotification) without tearing down and reopening the
 * connection each time; only `enabled` toggling, or the token/logger
 * identity changing, does that.
 */
export function useChatAlertsStream(
  enabled: boolean,
  onAlert: (event: ChatAlertEvent) => void,
): void {
  const getTokens = useAuthTokens();
  const logger = useLogger();
  const onAlertRef = useRef(onAlert);
  useEffect(() => {
    onAlertRef.current = onAlert;
  }, [onAlert]);

  useEffect(() => {
    if (!enabled) return;

    let cancelled = false;
    let source: EventSourcePolyfill | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;

    const scheduleReconnect = (): void => {
      const delay = reconnectDelay(attempt);
      attempt += 1;
      reconnectTimer = setTimeout(() => void connect(), delay);
    };

    const connect = async (): Promise<void> => {
      let token: string | undefined;
      let idToken: string | undefined;
      try {
        ({ token, idToken } = await getTokens());
      } catch (error) {
        logger.debug(
          "[chat-alerts-stream] failed to get tokens",
          error instanceof Error ? error.message : "Unknown token error",
        );
      }
      if (cancelled) return;
      if (!token || !idToken) {
        scheduleReconnect();
        return;
      }

      const url = `${apiConfig.backendUrl}/chat/alerts/stream`;
      source = new EventSourcePolyfill(url, {
        headers: {
          "x-jwt-assertion": token,
          "x-user-id-token": idToken,
        },
      });

      // A successful connection resets the backoff — only *consecutive*
      // failures should back off, not the cumulative count over the
      // component's whole lifetime.
      source.addEventListener("open", () => {
        attempt = 0;
      });

      source.addEventListener("chat_alert", (e) => {
        const messageEvent = e as MessageEvent<string>;
        try {
          const payload = JSON.parse(messageEvent.data) as ChatAlertEvent;
          onAlertRef.current(payload);
        } catch {
          logger.debug("[chat-alerts-stream] failed to parse chat_alert payload");
        }
      });

      source.addEventListener("error", () => {
        logger.debug("[chat-alerts-stream] connection error, reconnecting");
        source?.close();
        if (!cancelled) {
          scheduleReconnect();
        }
      });
    };

    void connect();

    return () => {
      cancelled = true;
      clearTimeout(reconnectTimer);
      source?.close();
    };
  }, [enabled, getTokens, logger]);
}
