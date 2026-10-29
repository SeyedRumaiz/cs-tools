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

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { resolveRequest } from "@features/csm-wso2-cloud/api/sampleData";
import { WSO2_CLOUD_QUERY_KEY } from "@features/csm-wso2-cloud/api/queryKeys";
import { useIdTokenClaims } from "@hooks/useIdTokenClaims";
import type { CloudRequest } from "@features/csm-wso2-cloud/types/wso2Cloud";

interface ResolveInput {
  id: string;
  status: "approved" | "rejected";
}

/**
 * Approves or rejects a pending request. An approval changes the organisation
 * or user it targets, so every cached WSO2 Cloud query is refreshed, not only
 * the request lists.
 */
export function usePatchCloudRequest() {
  const queryClient = useQueryClient();
  const email = useIdTokenClaims()?.email ?? "you";
  return useMutation<CloudRequest | null, Error, ResolveInput>({
    mutationFn: ({ id, status }) => resolveRequest(id, status, email),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [WSO2_CLOUD_QUERY_KEY] }),
  });
}
