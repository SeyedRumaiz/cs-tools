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

import type { PriorMessage } from "@features/csm-chat/types/chatAlerts";
import type { LiveChatMessage } from "./ChatSessionsContext";

/**
 * The transcript a just-accepted session starts with: the customer's earlier
 * AI-assistant conversation, then the message they opened the chat with.
 * The routing service stores both in this order, so the session matches
 * what the engineer sees after a page refresh.
 */
export function acceptedSessionMessages(
  caseId: string,
  priorMessages: PriorMessage[] | undefined,
  openingMessage: string | undefined,
): LiveChatMessage[] {
  const messages: LiveChatMessage[] = (priorMessages ?? []).map((m, i) => ({
    id: `prior-${caseId}-${i}`,
    from: m.role,
    text: m.content,
  }));
  if (openingMessage) {
    messages.push({ id: `opening-${caseId}`, from: "customer", text: openingMessage });
  }
  return messages;
}
