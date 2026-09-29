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

import { useMutation } from "@tanstack/react-query";
import { useAuthApiClient } from "@/hooks/useAuthApiClient";

// Not to be confused with usePostCaseEscalation.ts — that hook raises the
// severity of an already-existing case (a different, unrelated feature).
// This one starts a brand-new live-engineer-chat session from underneath a
// Novera reply, backed by backend-v2's own case-creation logic (see that
// backend's internal/handler/chat.go doc comment).
export type ChatEscalationApiError = Error & { status: number };

/**
 * One message from the customer's AI-chatbot (Novera) conversation, already
 * visible in this page at the moment "Chat with an Engineer" was clicked —
 * see NoveraChatPage's handleEscalateToEngineer, which builds these from its
 * own messages[] state. Mirrors backend-v2's escalatePriorMessage /
 * chat-routing-service's router.PriorMessage field-for-field.
 */
export type EscalationPriorMessage = {
  role: "customer" | "assistant";
  content: string;
  /** RFC 3339, optional. */
  createdAt?: string;
};

export type PostChatEscalationVariables = {
  /** The conversation this escalation was raised from — links the created case back to this chat. */
  conversationId: string;
  /** The customer's own opening text, shown to the engineer as context. Optional — the backend falls back to a generic message. */
  message?: string;
  /** Display label only, shown in the engineer's alert UI — never used for authorization/attribution. */
  customerName?: string;
  /**
   * The conversation history already visible on this page, in chronological
   * order, so the engineer who picks this up sees the same context the
   * customer already gave the AI instead of starting cold. Optional —
   * omitted or empty just means the engineer's chat starts without it.
   */
  priorMessages?: EscalationPriorMessage[];
};

export type PostChatEscalationResponse = {
  caseId: string;
  message: string;
};

/**
 * Escalates the given conversation to a live engineer: creates a case
 * (backend-v2 resolves the deployment/deployed-product itself — see the
 * package's own doc comment) and notifies csm-portal/backend so a connected
 * engineer sees the alert in real time. The engineer's acceptance itself
 * arrives later as an `engineer_assigned` event on the existing chat
 * WebSocket (see useChatWebSocket / NoveraChatPage's onEvent switch), not
 * as part of this call's response.
 */
export function usePostChatEscalation(projectId: string) {
  const authFetch = useAuthApiClient();

  return useMutation<
    PostChatEscalationResponse,
    ChatEscalationApiError,
    PostChatEscalationVariables
  >({
    mutationFn: async (payload) => {
      const baseUrl = window.config?.CUSTOMER_PORTAL_BACKEND_BASE_URL;
      if (!baseUrl) {
        throw new Error("CUSTOMER_PORTAL_BACKEND_BASE_URL is not configured");
      }
      const response = await authFetch(
        `${baseUrl}/projects/${projectId}/support/chat/escalate`,
        {
          method: "POST",
          body: JSON.stringify(payload),
        },
      );
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as {
          message?: string;
        };
        const err = new Error(
          body?.message ?? `Escalation failed: ${response.statusText}`,
        ) as ChatEscalationApiError;
        err.status = response.status;
        throw err;
      }
      return response.json() as Promise<PostChatEscalationResponse>;
    },
  });
}
