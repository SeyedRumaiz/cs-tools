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
import {
  getOrganizationSalesforce,
  getSalesforceAccount,
  SALESFORCE_ACCOUNT_ID_PATTERN,
} from "@features/csm-wso2-cloud/api/cloudRequestsApi";
import { WSO2_CLOUD_QUERY_KEY } from "@features/csm-wso2-cloud/api/queryKeys";
import type { CloudSalesforceAccount, CloudSalesforceStatus } from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * What Salesforce knows about an organisation: its record and the account it is
 * under. Not retried: a 503 means Salesforce is not connected.
 */
export function useGetCloudOrganizationSalesforce(organizationId: string | undefined) {
  const api = useBackendApi();
  return useQuery<CloudSalesforceStatus, Error>({
    queryKey: [WSO2_CLOUD_QUERY_KEY, "salesforce-organization", organizationId],
    queryFn: () => getOrganizationSalesforce(api, organizationId as string),
    enabled: !!organizationId,
    retry: false,
  });
}

/**
 * A Salesforce account and whether it has an active subscription; null when
 * Salesforce has no account with that id. Asked only for a well-formed id.
 */
export function useGetSalesforceAccount(accountId: string | undefined) {
  const api = useBackendApi();
  const id = accountId?.trim();
  return useQuery<CloudSalesforceAccount | null, Error>({
    queryKey: [WSO2_CLOUD_QUERY_KEY, "salesforce-account", id],
    queryFn: () => getSalesforceAccount(api, id as string),
    enabled: !!id && SALESFORCE_ACCOUNT_ID_PATTERN.test(id),
    retry: false,
  });
}
