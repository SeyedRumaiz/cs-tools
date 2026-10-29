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

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { searchOrganizations } from "@features/csm-wso2-cloud/api/sampleData";
import { WSO2_CLOUD_QUERY_KEY } from "@features/csm-wso2-cloud/api/queryKeys";
import type { CloudOrganization } from "@features/csm-wso2-cloud/types/wso2Cloud";

/** Organisations matching a handle, name, id or owner email. Sample data for now. */
export function useSearchCloudOrganizations(query: string) {
  return useQuery<CloudOrganization[], Error>({
    queryKey: [WSO2_CLOUD_QUERY_KEY, "organizations", query.trim().toLowerCase()],
    queryFn: () => searchOrganizations(query),
    placeholderData: keepPreviousData,
  });
}
