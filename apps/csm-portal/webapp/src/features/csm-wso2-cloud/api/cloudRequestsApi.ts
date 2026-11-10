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

/**
 * The WSO2 Cloud requests, from the portal backend's `/wso2-cloud-requests`
 * routes (stored by entity-service). Plain functions over a `BackendApi`, so
 * they can be tested without React; the hooks in this folder bind them to the
 * session.
 *
 * The backend decides who may do what: raising and cancelling (the requester,
 * or an admin), approving and rejecting (the WSO2 Cloud approver role, never
 * the requester), one pending request per type and target. Its refusals carry
 * a message written for the user, which `BackendApiError.message` holds.
 */
import type { BackendApi } from "@api/backend/client";
import {
  CLOUD_REQUEST_TYPE_LABELS,
  CLOUD_TIER_LABELS,
  type CloudOrganizationLimits,
  type CloudRequest,
  type CloudRequestStatus,
  type CloudRequestType,
  type CloudTier,
  type NewCloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

const BASE = "/wso2-cloud-requests";

/** The upstream's page cap. */
const PAGE_LIMIT = 50;
/** Stops a paging loop that never ends (a wrong total); 2,000 requests. */
const MAX_PAGES = 40;

/** A request as the backend sends it. */
export interface BeCloudRequest {
  id: string;
  /** WCR0000001: shown to people, never reused. */
  number: string;
  type: CloudRequestType;
  /** The organisation's UUID or the user's SCIM id. */
  targetId: string;
  /** The organisation's handle or the user's email, as it was when raised. */
  targetLabel: string;
  reason: string | null;
  customerConsent: string | null;
  tier: CloudTier | null;
  limits: CloudOrganizationLimits | null;
  previousTier: CloudTier | null;
  previousLimits: CloudOrganizationLimits | null;
  status: CloudRequestStatus;
  requestedBy: string;
  requestedOn: string;
  resolvedBy: string | null;
  resolvedOn: string | null;
  remark: string | null;
}

interface BeSearchCloudRequestsBody {
  filters: Record<string, never>;
  pagination: { limit: number; offset: number };
}

interface BeSearchCloudRequestsResponse {
  requests: BeCloudRequest[] | null;
  total: number;
}

interface BeCreateCloudRequestBody {
  type: CloudRequestType;
  targetId: string;
  targetLabel: string;
  reason?: string;
  customerConsent?: string;
  tier?: CloudTier;
  limits?: CloudOrganizationLimits;
  previousTier?: CloudTier;
  previousLimits?: CloudOrganizationLimits;
}

/** One line saying what a request changes, e.g. "Free → Enterprise". */
export function describeCloudRequest(
  type: CloudRequestType,
  label: string,
  tier?: CloudTier,
  previousTier?: CloudTier,
): string {
  if (type === "upgrade_organization") {
    if (!tier) return `Upgrade ${label}`;
    if (!previousTier) return `Upgrade to ${CLOUD_TIER_LABELS[tier]}`;
    if (previousTier === tier) return `Limits change on ${CLOUD_TIER_LABELS[tier]}`;
    return `${CLOUD_TIER_LABELS[previousTier]} → ${CLOUD_TIER_LABELS[tier]}`;
  }
  if (type === "delete_organization") return `Delete ${label}`;
  return `${CLOUD_REQUEST_TYPE_LABELS[type]} ${label}`;
}

const isUserRequest = (type: CloudRequestType) => type.endsWith("_user");

/** Maps the backend's record onto the frontend's own shape. */
export function toCloudRequest(be: BeCloudRequest): CloudRequest {
  const tier = be.tier ?? undefined;
  const previousTier = be.previousTier ?? undefined;
  return {
    id: be.id,
    number: be.number,
    type: be.type,
    target: {
      kind: isUserRequest(be.type) ? "user" : "organization",
      id: be.targetId,
      label: be.targetLabel,
    },
    summary: describeCloudRequest(be.type, be.targetLabel, tier, previousTier),
    reason: be.reason ?? undefined,
    customerConsent: be.customerConsent ?? undefined,
    tier,
    limits: be.limits ?? undefined,
    previousTier,
    previousLimits: be.previousLimits ?? undefined,
    requestedBy: be.requestedBy,
    requestedOn: be.requestedOn,
    status: be.status,
    resolvedBy: be.resolvedBy ?? undefined,
    resolvedOn: be.resolvedOn ?? undefined,
    remark: be.remark ?? undefined,
  };
}

/** Every request, newest first (the backend's order), paged through to the end. */
export async function listCloudRequests(api: BackendApi): Promise<CloudRequest[]> {
  const all: BeCloudRequest[] = [];
  for (let page = 0; page < MAX_PAGES; page++) {
    const res = await api.post<BeSearchCloudRequestsBody, BeSearchCloudRequestsResponse>(`${BASE}/search`, {
      filters: {},
      pagination: { limit: PAGE_LIMIT, offset: page * PAGE_LIMIT },
    });
    const rows = res.requests ?? [];
    all.push(...rows);
    if (rows.length < PAGE_LIMIT || all.length >= res.total) break;
  }
  return all.map(toCloudRequest);
}

/** One request, or `null` when there is none with that id (it may have been cancelled). */
export async function getCloudRequest(api: BackendApi, id: string): Promise<CloudRequest | null> {
  const be = await api.get<BeCloudRequest>(`${BASE}/${encodeURIComponent(id)}`);
  return be ? toCloudRequest(be) : null;
}

/** Raises a request. The requester is the signed-in user, taken from the token by the backend. */
export async function createCloudRequest(api: BackendApi, input: NewCloudRequest): Promise<CloudRequest> {
  const body: BeCreateCloudRequestBody = {
    type: input.type,
    targetId: input.target.id,
    targetLabel: input.target.label,
  };
  const reason = input.reason?.trim();
  if (reason) body.reason = reason;
  const consent = input.customerConsent?.trim();
  if (consent) body.customerConsent = consent;
  if (input.tier) body.tier = input.tier;
  if (input.limits) body.limits = input.limits;
  if (input.previousTier) body.previousTier = input.previousTier;
  if (input.previousLimits) body.previousLimits = input.previousLimits;
  const be = await api.post<BeCreateCloudRequestBody, BeCloudRequest>(BASE, body);
  return toCloudRequest(be);
}

/**
 * Approves or rejects a pending request. An approval is applied in WSO2 Cloud
 * by the backend before it is recorded; if it cannot be, the request stays
 * pending and the error says why.
 */
export async function resolveCloudRequest(
  api: BackendApi,
  id: string,
  status: Exclude<CloudRequestStatus, "pending">,
  remark?: string,
): Promise<CloudRequest> {
  const trimmed = remark?.trim();
  const be = await api.post<{ status: string; remark?: string }, BeCloudRequest>(
    `${BASE}/${encodeURIComponent(id)}/resolve`,
    trimmed ? { status, remark: trimmed } : { status },
  );
  return toCloudRequest(be);
}

/** Withdraws a pending request (its requester, or an admin). */
export async function cancelCloudRequest(api: BackendApi, id: string): Promise<void> {
  await api.delete(`${BASE}/${encodeURIComponent(id)}`);
}
