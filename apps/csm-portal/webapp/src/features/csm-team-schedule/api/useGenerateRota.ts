/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import { useBackendApi } from "@api/backend/client";

/** The one rota that can be generated. */
export const GENERATED_ROTA = "SRE_SAAS";

export interface GenerateRotaPayload {
  /** YYYY-MM. */
  month: string;
  dryRun?: boolean;
  regenerate?: boolean;
  /** YYYY-MM-DD, the first day written. */
  from?: string;
  /** Team key -> the people chosen to work that team's TZ3 for the month.
   *  A team left out keeps the usual nights. */
  nightCrew?: Record<string, string[]>;
}

export interface GeneratedRotaTurn {
  userId: string;
  name: string;
  teamKey: string;
  shiftCode: string;
  tier: string;
  rotaDate: string;
}

export interface GenerateRotaResult {
  rotaCode: string;
  month: string;
  from: string;
  dryRun: boolean;
  alreadyGenerated: boolean;
  summary: {
    planned: number;
    keptByHand: number;
    written: number;
    replaced: number;
    lieuPlanned: number;
    lieuWritten: number;
  };
  turns: GeneratedRotaTurn[];
  lieuLeave: { userId: string; name: string; teamKey: string; startsOn: string; endsOn: string }[];
  warnings: { date?: string; message: string }[];
}

/**
 * Work out (dryRun) or write a month of the SaaS rota from the availability
 * marked on the roster. Who may is the server's decision: a lead of Apollo or
 * Artemis, or an SRE rota admin.
 */
export function useGenerateRota(): UseMutationResult<GenerateRotaResult, Error, GenerateRotaPayload> {
  const api = useBackendApi();
  const qc = useQueryClient();
  return useMutation<GenerateRotaResult, Error, GenerateRotaPayload>({
    mutationFn: (payload) =>
      api.post<GenerateRotaPayload, GenerateRotaResult>(
        `/team-schedule/rotas/${GENERATED_ROTA}/generate`,
        payload,
      ),
    onSuccess: (result) => {
      if (result.dryRun) return;
      // The written month, and its lieu leave, are on the roster now.
      void qc.invalidateQueries({ queryKey: ["team-schedule", "assignments"] });
      void qc.invalidateQueries({ queryKey: ["team-schedule", "absences"] });
    },
  });
}
