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

export interface AcceptChatSessionInput {
  caseId: string;
  conversationId: string;
}

/**
 * Accepts a live-engineer-chat session: POST /chat/sessions/{caseId}/accept
 * (see csm-portal/backend's internal/handler/chat.go HandleAcceptSession).
 * Confirms the case to the calling engineer server-side (that one
 * conversation moves OPEN -> ACTIVE, see router.Router.Accept) and notifies
 * both the other connected engineers and the customer's chat. Deliberately
 * does not touch chat_status -- any other concurrent case this engineer
 * holds is unaffected.
 *
 * Invalidates the status/case-list query on success, matching
 * useCompleteChatSession/useDeclineChatSession/useSetEngineerStatus's own
 * pattern -- without this, this case kept showing as pending (its cached
 * value from when the alert first arrived, see EngineerAlertNotification's
 * handleAlert) for the entire chat, since nothing ever told it to refetch
 * the now-accepted state this call itself just caused server-side.
 */
export function useAcceptChatSession(): UseMutationResult<
  { message: string },
  Error,
  AcceptChatSessionInput
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<{ message: string }, Error, AcceptChatSessionInput>({
    mutationFn: ({ caseId, conversationId }) =>
      api.post(`/chat/sessions/${encodeURIComponent(caseId)}/accept`, {
        conversationId,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
    },
  });
}
