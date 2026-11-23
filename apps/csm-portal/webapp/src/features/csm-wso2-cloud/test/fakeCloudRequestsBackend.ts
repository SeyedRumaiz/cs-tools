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
 * An in-memory stand-in for the portal backend's `/wso2-cloud-requests`
 * routes, for page tests. It applies the backend's rules and answers with its
 * words: one pending request per type and target, a reason to lock or
 * disable, a remark to reject, nobody approves their own request (except a
 * customer upgrade, which needs no approval and is applied when raised), only the
 * requester or an admin cancels, and an approval WSO2 Cloud cannot apply
 * leaves the request pending. It also serves the reads the upgrade form needs
 * (an organisation's subscriptions, the configured Enterprise plans, its
 * Salesforce record and a Salesforce account) and the Salesforce side of a
 * customer upgrade: an account with an active subscription to raise and to
 * approve one, and the link made after approval (or retried).
 *
 * Use it by mocking the client:
 *
 *   vi.mock("@api/backend/client", async () => {
 *     const { fakeCloudBackend } = await import("@features/csm-wso2-cloud/test/fakeCloudRequestsBackend");
 *     return { useBackendApi: () => fakeCloudBackend.api };
 *   });
 */
import type { BackendApi } from "@api/backend/client";
import type { BeCloudRequest } from "@features/csm-wso2-cloud/api/cloudRequestsApi";
import type { CloudPlansByProduct, CloudSubscription } from "@features/csm-wso2-cloud/types/wso2Cloud";

/** A refusal, shaped like `BackendApiError` where the pages read it. */
export class FakeBackendError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Ids of the requests every reset starts with. */
export const SEEDED = {
  pendingUpgrade: "00000000-0000-4000-8000-000000000003",
  rejectedUnlock: "00000000-0000-4000-8000-000000000002",
  approvedUpgrade: "00000000-0000-4000-8000-000000000001",
} as const;

const BASE = "/wso2-cloud-requests";

/** A Salesforce account as the backend sends it. */
export interface FakeSalesforceAccount {
  id: string;
  name: string | null;
  hasActiveSubscription: boolean;
  activeOpportunities: { id: string; name: string | null; endDate: string | null }[];
}

/** An account with an active subscription, and one without. */
export const SF_ACTIVE: FakeSalesforceAccount = {
  id: "001000000000001AAA",
  name: "Acme Corp",
  hasActiveSubscription: true,
  activeOpportunities: [{ id: "006000000000001AAA", name: "Acme support 2027", endDate: "2027-06-30" }],
};
export const SF_LAPSED: FakeSalesforceAccount = {
  id: "001000000000002AAA",
  name: "Initech",
  hasActiveSubscription: false,
  activeOpportunities: [],
};
const REASON_REQUIRED = new Set(["lock_user", "disable_user"]);

function seeded(): BeCloudRequest[] {
  const empty = {
    reason: null,
    customerConsent: null,
    product: null,
    subscriptionId: null,
    planCode: null,
    previousPlanCode: null,
    billingPeriod: null,
    notifyCustomer: false,
    billingAccountId: null,
    resolvedBy: null,
    resolvedOn: null,
    remark: null,
  };
  return [
    {
      ...empty,
      id: SEEDED.pendingUpgrade,
      number: "WCR0000003",
      type: "upgrade_organization",
      targetId: "org-initech",
      targetLabel: "initech",
      product: "identity",
      subscriptionId: "sub-initech-identity",
      planCode: "identity-enterprise",
      previousPlanCode: "identity-free",
      billingPeriod: "annual",
      notifyCustomer: true,
      status: "pending",
      requestedBy: "sam.taylor@example.com",
      requestedOn: "2026-10-27T09:00:00Z",
    },
    {
      ...empty,
      id: SEEDED.rejectedUnlock,
      number: "WCR0000002",
      type: "unlock_user",
      targetId: "u-1002",
      targetLabel: "john.smith@example.com",
      status: "rejected",
      requestedBy: "sam.taylor@example.com",
      requestedOn: "2026-10-20T13:30:00Z",
      resolvedBy: "pat.morgan@example.com",
      resolvedOn: "2026-10-21T08:00:00Z",
      remark: "The lock clears on its own after the lockout period.",
    },
    {
      ...empty,
      id: SEEDED.approvedUpgrade,
      number: "WCR0000001",
      type: "upgrade_organization",
      targetId: "org-globex",
      targetLabel: "globex",
      product: "identity",
      subscriptionId: "sub-globex-identity",
      planCode: "identity-enterprise",
      previousPlanCode: "identity-payg",
      billingAccountId: "ba-globex",
      status: "approved",
      requestedBy: "sam.taylor@example.com",
      requestedOn: "2026-09-01T10:00:00Z",
      resolvedBy: "pat.morgan@example.com",
      resolvedOn: "2026-09-03T09:15:00Z",
    },
  ];
}

function defaultSubscriptions(): CloudSubscription[] {
  return [
    { id: "sub-identity", product: "identity", planCode: "identity-payg", status: "active" },
    { id: "sub-integration", product: "integration", planCode: "integration-free", status: "suspended" },
  ];
}

function defaultPlans(): CloudPlansByProduct {
  return {
    identity: [
      { code: "identity-enterprise", label: "Enterprise" },
      { code: "identity-payg", label: "Pay as you go" },
    ],
  };
}

class FakeCloudBackend {
  requests: BeCloudRequest[] = seeded();
  /** The signed-in user, as the token says. */
  me = "engineer@example.com";
  isAdmin = false;
  /** When set, an approval cannot be applied in WSO2 Cloud and fails with this message. */
  applyError: string | null = null;
  /** Every organisation's subscriptions; null answers 503 (WSO2 Cloud not connected). */
  subscriptions: CloudSubscription[] | null = defaultSubscriptions();
  plans: CloudPlansByProduct = defaultPlans();
  /** False answers 503 to every Salesforce read and refuses a customer upgrade. */
  salesforceConnected = true;
  /** Salesforce accounts by their 18-character id. */
  salesforceAccounts: Record<string, FakeSalesforceAccount> = { [SF_ACTIVE.id]: SF_ACTIVE, [SF_LAPSED.id]: SF_LAPSED };
  /** Organisations' Salesforce records: the account each is under, and its region. */
  salesforceOrgs: Record<string, { accountId: string | null; region: string | null }> = {};
  /** When set, linking fails with this reason. */
  linkError: string | null = null;
  private seq = 3;

  reset(): void {
    this.requests = seeded();
    this.me = "engineer@example.com";
    this.isAdmin = false;
    this.applyError = null;
    this.subscriptions = defaultSubscriptions();
    this.plans = defaultPlans();
    this.salesforceConnected = true;
    this.salesforceAccounts = { [SF_ACTIVE.id]: SF_ACTIVE, [SF_LAPSED.id]: SF_LAPSED };
    this.salesforceOrgs = {};
    this.linkError = null;
    this.seq = 3;
  }

  /** The account with this id (15 or 18 characters), as Salesforce matches it. */
  private salesforceAccount(id: string): FakeSalesforceAccount | null {
    const found = Object.values(this.salesforceAccounts).find((a) => a.id === id || a.id.slice(0, 15) === id);
    return found ?? null;
  }

  private requireSalesforce(): void {
    if (!this.salesforceConnected) {
      throw new FakeBackendError(503, "Salesforce is not connected to this service, so a customer upgrade cannot be checked or linked");
    }
  }

  /** The account a customer upgrade names, which must have an active subscription. */
  private subscribedAccount(id: string | null | undefined): FakeSalesforceAccount {
    this.requireSalesforce();
    const account = id ? this.salesforceAccount(id) : null;
    if (!account) throw new FakeBackendError(400, "salesforceAccountId: Salesforce has no account with this id");
    if (!account.hasActiveSubscription) {
      throw new FakeBackendError(409, "this Salesforce account has no active subscription (an active opportunity), which a customer upgrade needs");
    }
    return account;
  }

  /** Raises a request as `requestedBy`, by the backend's own rules. */
  raise(body: Partial<BeCloudRequest> & Pick<BeCloudRequest, "type" | "targetId" | "targetLabel">, requestedBy = this.me): BeCloudRequest {
    if (REASON_REQUIRED.has(body.type) && !body.reason?.trim()) {
      throw new FakeBackendError(400, "reason is required to lock or disable a user");
    }
    if (body.type === "upgrade_organization" && body.planCode && !(this.plans[body.product ?? ""] ?? []).some((p) => p.code === body.planCode)) {
      throw new FakeBackendError(400, "planCode is not one of the Enterprise plans configured for this product.");
    }
    if (body.type === "upgrade_organization") {
      if (!body.upgradeKind) throw new FakeBackendError(400, "upgradeKind is required for an upgrade_organization request (customer or internal)");
      if (body.upgradeKind === "customer") {
        if (!body.region) throw new FakeBackendError(400, "region is required for a customer upgrade");
        body = { ...body, salesforceAccountId: this.subscribedAccount(body.salesforceAccountId).id };
      }
    }
    if (this.requests.some((r) => r.status === "pending" && r.type === body.type && r.targetId === body.targetId)) {
      throw new FakeBackendError(409, `a ${body.type.replaceAll("_", " ")} request for this target is already pending`);
    }
    this.seq += 1;
    const created: BeCloudRequest = {
      reason: null,
      customerConsent: null,
      product: null,
      subscriptionId: null,
      planCode: null,
      previousPlanCode: null,
      billingPeriod: null,
      notifyCustomer: false,
      billingAccountId: null,
      ...body,
      id: `00000000-0000-4000-8000-${String(this.seq).padStart(12, "0")}`,
      number: `WCR${String(this.seq).padStart(7, "0")}`,
      status: "pending",
      requestedBy,
      requestedOn: new Date().toISOString(),
      resolvedBy: null,
      resolvedOn: null,
      remark: null,
    };
    this.requests = [created, ...this.requests];
    return created;
  }

  private find(id: string): BeCloudRequest {
    const found = this.requests.find((r) => r.id === id);
    if (!found) throw new FakeBackendError(404, "The requested resource was not found!");
    return found;
  }

  private resolve(id: string, body: { status: string; remark?: string }): BeCloudRequest {
    const request = this.find(id);
    if (request.status !== "pending") throw new FakeBackendError(409, "This request is no longer pending.");
    if (body.status === "rejected" && !body.remark?.trim()) {
      throw new FakeBackendError(400, "remark is required to reject a request");
    }
    if (body.status === "approved") {
      // A customer upgrade needs no approval, so its requester may apply it.
      if (request.upgradeKind !== "customer" && request.requestedBy.toLowerCase() === this.me.toLowerCase()) {
        throw new FakeBackendError(403, "You cannot approve a request you raised.");
      }
      if (request.upgradeKind === "customer" && !this.salesforceAccount(request.salesforceAccountId ?? "")?.hasActiveSubscription) {
        throw new FakeBackendError(
          409,
          "The customer's Salesforce account no longer has an active subscription, so this upgrade cannot be approved. The request stays pending.",
        );
      }
      if (this.applyError) throw new FakeBackendError(503, this.applyError);
    }
    const resolved: BeCloudRequest = {
      ...request,
      status: body.status as BeCloudRequest["status"],
      resolvedBy: this.me,
      resolvedOn: new Date().toISOString(),
      remark: body.remark ?? null,
    };
    this.requests = this.requests.map((r) => (r.id === id ? resolved : r));
    // A customer upgrade is linked in Salesforce once approved.
    return resolved.status === "approved" && resolved.upgradeKind === "customer" ? this.link(id) : resolved;
  }

  private link(id: string): BeCloudRequest {
    const request = this.find(id);
    if (request.upgradeKind !== "customer") throw new FakeBackendError(400, "only a customer upgrade has a Salesforce account");
    if (request.status !== "approved") throw new FakeBackendError(409, `only an approved request is linked in Salesforce; this one is ${request.status}`);
    if (request.salesforceLinkStatus === "linked") return request;
    const linked: BeCloudRequest = this.linkError
      ? { ...request, salesforceLinkStatus: "failed", salesforceLinkError: this.linkError }
      : { ...request, salesforceLinkStatus: "linked", salesforceLinkError: null, salesforceLinkedOn: new Date().toISOString() };
    if (!this.linkError) {
      this.salesforceOrgs[request.targetId] = { accountId: request.salesforceAccountId ?? null, region: request.region ?? null };
    }
    this.requests = this.requests.map((r) => (r.id === id ? linked : r));
    return linked;
  }

  private cancel(id: string): void {
    const request = this.find(id);
    if (request.status !== "pending") throw new FakeBackendError(409, "This request is no longer pending.");
    if (request.requestedBy.toLowerCase() !== this.me.toLowerCase() && !this.isAdmin) {
      throw new FakeBackendError(403, "Only the person who raised this request, or an admin, can cancel it.");
    }
    this.requests = this.requests.filter((r) => r.id !== id);
  }

  readonly api = {
    get: async <T>(path: string): Promise<T | null> => {
      if (path === "/wso2-cloud/plans") return { plans: this.plans } as T;
      if (/^\/wso2-cloud\/organizations\/[^/]+\/subscriptions$/.test(path)) {
        if (this.subscriptions === null) {
          throw new FakeBackendError(503, "WSO2 Cloud is not connected yet, so the organization's subscriptions cannot be loaded.");
        }
        return { subscriptions: this.subscriptions } as T;
      }
      const orgSalesforce = path.match(/^\/wso2-cloud\/organizations\/([^/]+)\/salesforce$/);
      if (orgSalesforce) {
        this.requireSalesforce();
        const rec = this.salesforceOrgs[orgSalesforce[1]];
        if (!rec) return { record: null, account: null } as T;
        return {
          record: { id: `a0X-${orgSalesforce[1]}`, handle: null, region: rec.region },
          account: rec.accountId ? this.salesforceAccount(rec.accountId) : null,
        } as T;
      }
      const account = path.match(/^\/wso2-cloud\/salesforce-accounts\/([^/]+)$/);
      if (account) {
        this.requireSalesforce();
        return this.salesforceAccount(decodeURIComponent(account[1])) as T | null;
      }
      const id = path.slice(BASE.length + 1);
      return (this.requests.find((r) => r.id === id) ?? null) as T | null;
    },
    post: async <TReq, TRes>(path: string, body: TReq): Promise<TRes> => {
      if (path === BASE) {
        const created = this.raise(body as Parameters<FakeCloudBackend["raise"]>[0]);
        // A customer upgrade needs no approval: applied at once, as the requester. If it
        // cannot be applied the request stays pending and the error is the answer.
        if (created.upgradeKind === "customer") return this.resolve(created.id, { status: "approved" }) as TRes;
        return created as TRes;
      }
      if (path === `${BASE}/search`) {
        const { pagination } = body as { pagination: { limit: number; offset: number } };
        const page = this.requests.slice(pagination.offset, pagination.offset + pagination.limit);
        return { requests: page, total: this.requests.length } as TRes;
      }
      const resolve = path.match(/^\/wso2-cloud-requests\/([^/]+)\/resolve$/);
      if (resolve) return this.resolve(resolve[1], body as { status: string; remark?: string }) as TRes;
      throw new Error(`unexpected POST ${path}`);
    },
    put: async <TRes>(path: string): Promise<TRes> => {
      const link = path.match(/^\/wso2-cloud-requests\/([^/]+)\/salesforce-link$/);
      if (link) return this.link(link[1]) as TRes;
      throw new Error(`unexpected PUT ${path}`);
    },
    delete: async (path: string): Promise<void> => {
      this.cancel(path.slice(BASE.length + 1));
    },
  } as unknown as BackendApi;
}

export const fakeCloudBackend = new FakeCloudBackend();
