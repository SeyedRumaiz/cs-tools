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
import { useBackendApi } from "@api/backend/client";
import { cancelCloudRequest } from "@features/csm-wso2-cloud/api/cloudRequestsApi";
import { WSO2_CLOUD_QUERY_KEY } from "@features/csm-wso2-cloud/api/queryKeys";

/** Cancels a pending request (the requester's own, or any for an admin). */
export function useDeleteCloudRequest() {
  const api = useBackendApi();
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => cancelCloudRequest(api, id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: [WSO2_CLOUD_QUERY_KEY, "requests"] }),
  });
}
