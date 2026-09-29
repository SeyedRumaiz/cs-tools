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

export interface ConvertChatToCaseInput {
  caseId: string;
}

export interface ConvertChatToCaseResult {
  entityCaseId: string;
}

/**
 * Converts a live-engineer-chat session into a real case: POST
 * /chat/sessions/{caseId}/convert-to-case, no request body needed since
 * chat-routing-service already has the original escalation's details.
 * Unlike useCompleteChatSession, a rejection is surfaced to the caller
 * rather than swallowed, since a failure may mean a case was created but
 * ending the chat session failed server-side.
 */
export function useConvertChatToCase(): UseMutationResult<
  ConvertChatToCaseResult,
  Error,
  ConvertChatToCaseInput
> {
  const api = useBackendApi();
  const queryClient = useQueryClient();

  return useMutation<ConvertChatToCaseResult, Error, ConvertChatToCaseInput>({
    mutationFn: ({ caseId }) =>
      api.post(`/chat/sessions/${encodeURIComponent(caseId)}/convert-to-case`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ENGINEER_STATUS_QUERY_KEY });
    },
  });
}
