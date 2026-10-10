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

import type { EntityRefDto } from "./case.dto";
import type {
  IncidentDetailDto,
  IncidentPriority,
  IncidentSearchViewDto,
  IncidentState,
  IncidentWatchListItemDto,
  LinkedServiceRequestRefDto,
} from "./incident.dto";
import { parseOptionalBackendTimestamp } from "@utils/dateTime";

export interface IncidentSummary {
  id: string | null;
  number: string;
  subject: string;
  openedOn: Date | null;
  caller: EntityRefDto | null;
  priority: IncidentPriority | null;
  state: IncidentState | null;
  category: string | null;
  assignmentGroup: EntityRefDto | null;
  assignedTo: EntityRefDto | null;
  createdOn: Date | null;
  updatedOn: Date | null;
}

export interface IncidentDetail extends IncidentSummary {
  subcategory: string | null;
  service: EntityRefDto | null;
  serviceOffering: EntityRefDto | null;
  configurationItem: EntityRefDto | null;
  /** "Channel" in the UI — the DTO's `contactType`. */
  channel: string | null;
  impact: string | null;
  urgency: string | null;
  changeRequest: EntityRefDto | null;
  problem: EntityRefDto | null;
  causedBy: EntityRefDto | null;
  createdBy: string | null;
  additionalComments: string | null;
  workNotes: string | null;
  watchList: IncidentWatchListItemDto[];
  linkedServiceRequests: LinkedServiceRequestRefDto[];
}

export function toIncidentSummary(dto: IncidentSearchViewDto): IncidentSummary {
  return {
    id: dto.id,
    number: dto.number ?? "—",
    subject: dto.subject ?? "(No subject)",
    openedOn: parseOptionalBackendTimestamp(dto.openedOn),
    caller: dto.caller ?? null,
    priority: dto.priority,
    state: dto.state,
    category: dto.category,
    assignmentGroup: dto.assignmentGroup ?? null,
    assignedTo: dto.assignedTo ?? null,
    createdOn: parseOptionalBackendTimestamp(dto.createdOn),
    updatedOn: parseOptionalBackendTimestamp(dto.updatedOn),
  };
}

export function toIncidentDetail(dto: IncidentDetailDto): IncidentDetail {
  return {
    ...toIncidentSummary(dto),
    subcategory: dto.subcategory ?? null,
    service: dto.service ?? null,
    serviceOffering: dto.serviceOffering ?? null,
    configurationItem: dto.configurationItem ?? null,
    channel: dto.contactType ?? null,
    impact: dto.impact ?? null,
    urgency: dto.urgency ?? null,
    changeRequest: dto.changeRequest ?? null,
    problem: dto.problem ?? null,
    causedBy: dto.causedBy ?? null,
    createdBy: dto.createdBy ?? null,
    additionalComments: dto.additionalComments ?? null,
    workNotes: dto.workNotes ?? null,
    watchList: dto.watchList ?? [],
    linkedServiceRequests: dto.linkedServiceRequests ?? [],
  };
}
