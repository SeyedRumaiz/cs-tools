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

import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { ApiQueryKeys } from "@constants/apiConstants";
import { useBackendApi } from "@api/backend/client";
import type { BeProductRepoMapping } from "@api/backend/types";

/**
 * Looks up the GitHub repository for a case product via
 * `GET /products/github-repo`. A 404 is "no mapping" (`null`), not an error,
 * so the dialog can keep Create disabled. Any other failure is an error.
 * The query stays off until the case has a real product name.
 */
export function useGetProductRepoMapping(
  productName: string | undefined,
): UseQueryResult<BeProductRepoMapping | null, Error> {
  const api = useBackendApi();
  const name = productName?.trim() ?? "";
  const enabled = name !== "" && name !== "—" && name !== "-";

  return useQuery<BeProductRepoMapping | null, Error>({
    queryKey: [ApiQueryKeys.CSM_PRODUCT_REPO_MAPPING, name],
    enabled,
    queryFn: async (): Promise<BeProductRepoMapping | null> => {
      return api.get<BeProductRepoMapping>(
        `/products/github-repo?name=${encodeURIComponent(name)}`,
      );
    },
    staleTime: 30_000,
  });
}
