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

export const CHAT_SENDER_USER = "user";
export const CHAT_SENDER_BOT = "bot";

export const NOVERA_DISPLAY_NAME = "Novera";
export const NOVERA_ANALYZING_PLACEHOLDER_TEXT =
  "Novera is analyzing your request...";
export const NOVERA_INITIAL_WELCOME_TEXT =
  "Hi! I'm Novera, your AI support assistant. I'm here to help you resolve your issue quickly. Can you describe the problem you're experiencing?";
// Id of the static welcome message; real bot replies have other ids. Used to
// distinguish "the assistant has actually responded" from the seeded greeting.
export const NOVERA_WELCOME_MESSAGE_ID = "1";

export const CHAT_TYPING_INTERVAL_MS = 20;
export const CHAT_TYPING_CHARS_PER_TICK = 6;

// How often NoveraChatPage pings its own WebSocket while a live-engineer
// chat is active (see its heartbeat effect). Must stay comfortably under
// backend-v2's own read-idle timeout (wsIdleTimeout, 5 minutes in
// websocket.go) -- once escalated, a customer's messages route over REST
// (see sendViaHumanChat), not this socket, so without a heartbeat the
// connection can sit fully silent for the whole session and get closed
// (and unregistered from PushEvent's delivery map) purely for looking
// idle, even though the chat itself is very much still going.
export const HUMAN_CHAT_WS_HEARTBEAT_INTERVAL_MS = 60_000;

export const VISIBILITY_EVENT_LISTENER = "visibilitychange";

export const CHAT_MAX_CHARS = 4000;

export const TOKEN_WARNING_SESSION_LIMIT_REACHED = "session_limit_reached";
