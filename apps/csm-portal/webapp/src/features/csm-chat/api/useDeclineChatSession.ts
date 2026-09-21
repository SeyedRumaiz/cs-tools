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
  useMutation,
  useQueryClient,
  type UseMutationResult,
} from "@tanstack/react-query";
import { useBackendApi } from "@api/backend/client";
import { ENGINEER_STATUS_QUERY_KEY } from "./useEngineerStatus";

export interface DeclineChatSessionInput {
  caseId: string;
  conversationId: string;
}

/**
 * Declines a live-engineer-chat alert before accepting it: POST
 * /chat/sessions/{caseId}/decline (see csm-portal/backend's
 * internal/handler/chat.go HandleDeclineSession). Necessary now that
 * escalations are routed to exactly one engineer at a time instead of
 * broadcast to all of them (see that backend's chat-routing-service
 * integration) — without this call, dismissing an alert would silently
 * strand the customer with nobody else ever seeing their request. The
 * backend re-routes the case to another available engineer, or requeues
 * it if none are free; this call does not need to know which happened.
 *
 * Declining releases the SAME routing-service capacity accepting would
 * have (you hold this case, and it counts toward your concurrent-chat
 * capacity, the moment an escalation routes to you — before you've clicked
 * anything) — so this also invalidates the status/case-list query the same
 * way completing a session does, rather than leaving it stuck listing a
 * case that's no longer yours.
 */
export function useDeclineChatSession(): UseMutationResult<
  { message: string },
  Error,
  DeclineChatSessionInput
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<{ message: string }, Error, DeclineChatSessionInput>({
    mutationFn: ({ caseId, conversationId }) =>
      api.post(`/chat/sessions/${encodeURIComponent(caseId)}/decline`, {
        conversationId,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
    },
  });
}
