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
  CLOUD_REGIONS,
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudBillingPeriod,
  type CloudPlansByProduct,
  type CloudRegion,
  type CloudSalesforceAccount,
  type CloudSalesforceStatus,
  type CloudUpgradeKind,
  type CloudRequest,
  type CloudRequestStatus,
  type CloudRequestType,
  type CloudSubscription,
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
  /** The upgrade fields: null (notifyCustomer false) for every other type. */
  product: string | null;
  subscriptionId: string | null;
  planCode: string | null;
  previousPlanCode: string | null;
  billingPeriod: CloudBillingPeriod | null;
  notifyCustomer: boolean;
  billingAccountId: string | null;
  /** Upgrades only; null on one raised before it existed (treated as internal). */
  upgradeKind?: CloudUpgradeKind | null;
  /** A customer upgrade's Salesforce Account Id (18 characters) and region. */
  salesforceAccountId?: string | null;
  region?: string | null;
  /** An approved customer upgrade's Salesforce link: null until tried. */
  salesforceLinkStatus?: "linked" | "failed" | null;
  salesforceLinkError?: string | null;
  salesforceLinkedOn?: string | null;
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
  product?: string;
  subscriptionId?: string;
  planCode?: string;
  previousPlanCode?: string;
  billingPeriod?: CloudBillingPeriod;
  notifyCustomer?: boolean;
  upgradeKind?: CloudUpgradeKind;
  salesforceAccountId?: string;
  region?: CloudRegion;
}

/** One line saying what a request changes, e.g. "Upgrade identity to identity-enterprise". */
export function describeCloudRequest(
  type: CloudRequestType,
  label: string,
  planCode?: string,
  product?: string,
): string {
  if (type === "upgrade_organization") {
    if (!planCode) return `Upgrade ${label}`;
    return `Upgrade ${product ?? label} to ${planCode}`;
  }
  if (type === "delete_organization") return `Delete ${label}`;
  return `${CLOUD_REQUEST_TYPE_LABELS[type]} ${label}`;
}

const isUserRequest = (type: CloudRequestType) => type.endsWith("_user");

/** Maps the backend's record onto the frontend's own shape. */
export function toCloudRequest(be: BeCloudRequest): CloudRequest {
  const upgrade =
    be.type === "upgrade_organization" && be.subscriptionId && be.planCode
      ? {
          product: be.product ?? undefined,
          subscriptionId: be.subscriptionId,
          planCode: be.planCode,
          previousPlanCode: be.previousPlanCode ?? undefined,
          billingPeriod: be.billingPeriod ?? undefined,
          notifyCustomer: be.notifyCustomer,
          billingAccountId: be.billingAccountId ?? undefined,
          kind: be.upgradeKind ?? undefined,
          salesforceAccountId: be.salesforceAccountId ?? undefined,
          region: be.region ?? undefined,
          salesforceLink: be.salesforceLinkStatus
            ? {
                status: be.salesforceLinkStatus,
                error: be.salesforceLinkError ?? undefined,
                linkedOn: be.salesforceLinkedOn ?? undefined,
              }
            : undefined,
        }
      : undefined;
  return {
    id: be.id,
    number: be.number,
    type: be.type,
    target: {
      kind: isUserRequest(be.type) ? "user" : "organization",
      id: be.targetId,
      label: be.targetLabel,
    },
    summary: describeCloudRequest(be.type, be.targetLabel, upgrade?.planCode, upgrade?.product),
    reason: be.reason ?? undefined,
    customerConsent: be.customerConsent ?? undefined,
    upgrade,
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
  const upgrade = input.upgrade;
  if (upgrade) {
    body.product = upgrade.product;
    body.subscriptionId = upgrade.subscriptionId;
    body.planCode = upgrade.planCode;
    if (upgrade.previousPlanCode) body.previousPlanCode = upgrade.previousPlanCode;
    if (upgrade.billingPeriod) body.billingPeriod = upgrade.billingPeriod;
    if (upgrade.notifyCustomer) body.notifyCustomer = true;
    body.upgradeKind = upgrade.kind;
    body.region = upgrade.region;
    if (upgrade.kind === "customer") {
      body.salesforceAccountId = upgrade.salesforceAccountId?.trim();
    }
  }
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

/**
 * An organisation's product subscriptions in WSO2 Cloud, so an upgrade can name
 * the one it moves. The backend answers 503 when WSO2 Cloud is not connected.
 */
export async function listCloudSubscriptions(
  api: BackendApi,
  organizationId: string,
  region?: CloudRegion,
): Promise<CloudSubscription[]> {
  const query = region ? `?region=${encodeURIComponent(region)}` : "";
  const res = await api.get<{ subscriptions: CloudSubscription[] | null }>(
    `/wso2-cloud/organizations/${encodeURIComponent(organizationId)}/subscriptions${query}`,
  );
  return res?.subscriptions ?? [];
}

/**
 * The WSO2 Cloud regions this environment can reach (one billing API each): one on
 * Dev and Stage, us and eu in production, none when WSO2 Cloud is not connected.
 * Codes the portal does not know are left out.
 */
export async function listCloudRegions(api: BackendApi): Promise<CloudRegion[]> {
  const res = await api.get<{ regions: string[] | null }>("/wso2-cloud/regions");
  return (res?.regions ?? []).filter((r): r is CloudRegion => (CLOUD_REGIONS as readonly string[]).includes(r));
}

/** The Enterprise plans an upgrade can ask for, per product (configured on the backend). */
export async function listCloudPlans(api: BackendApi): Promise<CloudPlansByProduct> {
  const res = await api.get<{ plans: CloudPlansByProduct | null }>("/wso2-cloud/plans");
  return res?.plans ?? {};
}

/** A Salesforce Account Id: the 001 key prefix, 15 or 18 letters and digits. */
export const SALESFORCE_ACCOUNT_ID_PATTERN = /^001[A-Za-z0-9]{12}(?:[A-Za-z0-9]{3})?$/;

interface BeSalesforceAccount {
  id: string;
  name: string | null;
  hasActiveSubscription: boolean;
  activeOpportunities: { id: string; name: string | null; endDate: string | null }[] | null;
}

function toSalesforceAccount(be: BeSalesforceAccount): CloudSalesforceAccount {
  return {
    id: be.id,
    name: be.name ?? undefined,
    hasActiveSubscription: be.hasActiveSubscription,
    activeOpportunities: (be.activeOpportunities ?? []).map((o) => ({
      id: o.id,
      name: o.name ?? undefined,
      endDate: o.endDate ?? undefined,
    })),
  };
}

/**
 * What Salesforce knows about an organisation: its record and the account it
 * is under, with that account's active subscriptions. The backend answers 503
 * when Salesforce is not connected.
 */
export async function getOrganizationSalesforce(api: BackendApi, organizationId: string): Promise<CloudSalesforceStatus> {
  const res = await api.get<{
    record: { id: string; handle: string | null; region: string | null } | null;
    account: BeSalesforceAccount | null;
  }>(`/wso2-cloud/organizations/${encodeURIComponent(organizationId)}/salesforce`);
  return {
    record: res?.record
      ? { id: res.record.id, handle: res.record.handle ?? undefined, region: res.record.region ?? undefined }
      : null,
    account: res?.account ? toSalesforceAccount(res.account) : null,
  };
}

/** A Salesforce account and whether it has an active subscription (404 when there is none). */
export async function getSalesforceAccount(api: BackendApi, accountId: string): Promise<CloudSalesforceAccount | null> {
  const res = await api.get<BeSalesforceAccount>(`/wso2-cloud/salesforce-accounts/${encodeURIComponent(accountId)}`);
  return res ? toSalesforceAccount(res) : null;
}

/**
 * Links an approved customer upgrade's organisation to its account in
 * Salesforce again, after the link failed. Answers with the request and the
 * link's outcome.
 */
export async function linkCloudRequestSalesforce(api: BackendApi, id: string): Promise<CloudRequest> {
  const be = await api.put<Record<string, never>, BeCloudRequest>(`${BASE}/${encodeURIComponent(id)}/salesforce-link`, {});
  return toCloudRequest(be);
}
