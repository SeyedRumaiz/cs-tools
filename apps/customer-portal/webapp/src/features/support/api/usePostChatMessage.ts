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

// Sends a customer chat message once a live engineer has accepted the
// session (see the `engineer_assigned` case in NoveraChatPage's onEvent
// switch). Deliberately NOT sent over the AI chat WebSocket, even though
// the connection is already open — a human-attended message is persisted as
// a case comment by csm-portal/backend, not as a conversation comment the
// way an AI-chat message is (see backend-v2's HandleSendMessage doc
// comment), so it needs its own REST call.
export type ChatMessageApiError = Error & { status: number };

export type PostChatMessageVariables = {
  conversationId: string;
  /** The case this live session belongs to — from usePostChatEscalation's response. */
  caseId: string;
  message: string;
};

export type PostChatMessageResponse = {
  message: string;
};

export function usePostChatMessage(projectId: string) {
  const authFetch = useAuthApiClient();

  return useMutation<
    PostChatMessageResponse,
    ChatMessageApiError,
    PostChatMessageVariables
  >({
    mutationFn: async ({ conversationId, caseId, message }) => {
      const baseUrl = window.config?.CUSTOMER_PORTAL_BACKEND_BASE_URL;
      if (!baseUrl) {
        throw new Error("CUSTOMER_PORTAL_BACKEND_BASE_URL is not configured");
      }
      const response = await authFetch(
        `${baseUrl}/projects/${projectId}/support/chat/${conversationId}/message`,
        {
          method: "POST",
          body: JSON.stringify({ caseId, message }),
        },
      );
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as {
          message?: string;
        };
        const err = new Error(
          body?.message ?? `Failed to send message: ${response.statusText}`,
        ) as ChatMessageApiError;
        err.status = response.status;
        throw err;
      }
      return response.json() as Promise<PostChatMessageResponse>;
    },
  });
}
