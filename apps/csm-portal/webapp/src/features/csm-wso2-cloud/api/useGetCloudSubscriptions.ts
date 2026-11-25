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

import { useQuery } from "@tanstack/react-query";
import { useBackendApi } from "@api/backend/client";
import { listCloudSubscriptions } from "@features/csm-wso2-cloud/api/cloudRequestsApi";
import { WSO2_CLOUD_QUERY_KEY } from "@features/csm-wso2-cloud/api/queryKeys";
import type { CloudRegion, CloudSubscription } from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * An organisation's product subscriptions in a WSO2 Cloud region (each region has its own
 * billing API). Waits until `enabled`: the page holds it back until a region is chosen,
 * where this environment has any. Not retried: a 503 means WSO2 Cloud is not connected,
 * a 404 that the organisation is not in that region.
 */
export function useGetCloudSubscriptions(organizationId: string | undefined, region?: CloudRegion, enabled = true) {
  const api = useBackendApi();
  return useQuery<CloudSubscription[], Error>({
    queryKey: [WSO2_CLOUD_QUERY_KEY, "subscriptions", organizationId, region ?? null],
    queryFn: () => listCloudSubscriptions(api, organizationId as string, region),
    enabled: !!organizationId && enabled,
    retry: false,
  });
}
