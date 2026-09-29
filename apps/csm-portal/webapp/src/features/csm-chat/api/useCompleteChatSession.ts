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

export interface CompleteChatSessionInput {
  caseId: string;
  conversationId: string;
}

/**
 * Ends a live-engineer-chat session: POST /chat/sessions/{caseId}/complete
 * (see csm-portal/backend's internal/handler/chat.go
 * HandleCompleteSession). Notifies other engineers and drops the customer
 * back to Novera-only chat. Deliberately does not change the case's own
 * state — resolving the underlying issue is still a separate, explicit step
 * on the normal case detail page.
 *
 * This also releases the engineer's routing-service capacity for this one
 * case server-side (chat_status itself is untouched, since it's a plain
 * manual toggle independent of case load) — but the response here doesn't
 * say whether that immediately backfilled another queued case, so the
 * status/case-list query is invalidated rather than optimistically set,
 * letting it refetch the real outcome instead of going stale.
 */
export function useCompleteChatSession(): UseMutationResult<
  { message: string },
  Error,
  CompleteChatSessionInput
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<{ message: string }, Error, CompleteChatSessionInput>({
    mutationFn: ({ caseId, conversationId }) =>
      api.post(`/chat/sessions/${encodeURIComponent(caseId)}/complete`, {
        conversationId,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
    },
  });
}
