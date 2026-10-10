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
import { isOnboardingStatusEnabled } from "@config/onboardingStatusConfig";
import type { BeProjectOnboardingStepsResponse } from "@api/backend/types";

/**
 * A project's customer-onboarding ledger, via
 * `GET /projects/{id}/onboarding-steps`: every recorded onboarding step of
 * each invited email, already grouped per membership and paged through by
 * the backend, plus `truncated` when the backend hit its own page cap.
 *
 * Disabled — no request at all — until a project id is provided AND the
 * `CSM_MIGRATION_ONBOARDING_STATUS_ENABLED` runtime flag is on (see
 * `onboardingStatusConfig.ts`). The backend registers the route under the
 * same flag, so calling it while off would only ever 404.
 */
export function useGetProjectOnboardingSteps(
  projectId: string | undefined,
): UseQueryResult<BeProjectOnboardingStepsResponse, Error> {
  const api = useBackendApi();
  const enabled = isOnboardingStatusEnabled();

  return useQuery<BeProjectOnboardingStepsResponse, Error>({
    queryKey: [ApiQueryKeys.PROJECT_ONBOARDING_STEPS, projectId ?? ""],
    queryFn: async (): Promise<BeProjectOnboardingStepsResponse> => {
      const res = await api.get<BeProjectOnboardingStepsResponse>(
        `/projects/${encodeURIComponent(projectId ?? "")}/onboarding-steps`,
      );
      return res ?? { memberships: [], total: 0, truncated: false };
    },
    enabled: !!projectId && enabled,
    staleTime: 60_000,
  });
}
