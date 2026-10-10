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
import { useBackendApi } from "@api/backend/client";
import type { BeGrantableRole, BeGrantableRolesResponse } from "@api/backend/types";

/**
 * The portal roles `AddUserDialog.tsx` may grant a new user via SCIM
 * (`GET /roles/grantable`) — admin-only on the backend (`PermAdmin`, the same
 * gate `POST /users` itself sits behind), so `enabled` should stay tied to
 * the dialog actually being open rather than fetched eagerly on every page
 * load. Each entry's `key` is a stable portal role key, never the real
 * identity-provider role name/id — see `grantableRoleLabels.ts` for the
 * display label each one maps to.
 */
export function useGetGrantableRoles(enabled: boolean): UseQueryResult<BeGrantableRole[], Error> {
  const api = useBackendApi();

  return useQuery<BeGrantableRole[], Error>({
    queryKey: ["csm-users-grantable-roles"],
    queryFn: async (): Promise<BeGrantableRole[]> => {
      const res = await api.get<BeGrantableRolesResponse>("/roles/grantable");
      return res?.roles ?? [];
    },
    enabled,
    staleTime: 5 * 60_000,
  });
}
