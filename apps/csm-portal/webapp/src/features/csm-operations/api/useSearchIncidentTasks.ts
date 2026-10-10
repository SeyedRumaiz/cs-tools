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
import type {
  BeIncidentTaskSearchPayload,
  BeIncidentTaskSearchResponse,
} from "@api/backend/types";

const INCIDENT_TASKS_LIMIT = 50;

export interface IncidentTaskRow {
  id: string;
  number?: string;
  subject: string;
  /** Raw state value (an incident_task_state_enum label on Postgres). */
  state?: string;
  stateLabel?: string;
  assignmentGroupName?: string;
  assignedToName?: string;
}

export interface IncidentTasksResult {
  tasks: IncidentTaskRow[];
  total: number;
}

/**
 * The incident tasks of one incident — the incident report task, and the
 * alert tasks "[WSO2 Cloud Ops] Post resolution tasks" opens on resolve.
 * Calls `POST /incident-tasks/search` with the generic `incidentId` filter.
 * Disabled until an incident id is provided.
 */
export function useSearchIncidentTasks(
  incidentId: string | undefined,
): UseQueryResult<IncidentTasksResult, Error> {
  const api = useBackendApi();

  return useQuery<IncidentTasksResult, Error>({
    queryKey: [ApiQueryKeys.CSM_INCIDENT_TASKS, incidentId ?? ""],
    queryFn: async (): Promise<IncidentTasksResult> => {
      const res = await api.post<BeIncidentTaskSearchPayload, BeIncidentTaskSearchResponse>(
        "/incident-tasks/search",
        {
          filters: {
            filters: [{ field: "incidentId", op: "in", values: [incidentId as string] }],
          },
          pagination: { offset: 0, limit: INCIDENT_TASKS_LIMIT },
        },
      );
      return {
        tasks: (res.incidentTasks ?? [])
          .filter((t): t is typeof t & { id: string } => !!t.id)
          .map((t) => ({
            id: t.id,
            number: t.number ?? undefined,
            subject: t.subject ?? "(no subject)",
            state: t.state ?? undefined,
            stateLabel: t.stateLabel ?? t.state ?? undefined,
            assignmentGroupName: t.assignmentGroup?.name ?? undefined,
            assignedToName: t.assignedTo?.name ?? undefined,
          })),
        total: res.total ?? 0,
      };
    },
    enabled: !!incidentId,
    staleTime: 30_000,
  });
}
