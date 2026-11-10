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

import { describe, expect, it, vi } from "vitest";
import type { BackendApi } from "@api/backend/client";
import {
  type BeCloudRequest,
  cancelCloudRequest,
  createCloudRequest,
  describeCloudRequest,
  getCloudRequest,
  listCloudRequests,
  resolveCloudRequest,
  toCloudRequest,
} from "@features/csm-wso2-cloud/api/cloudRequestsApi";

const ID = "11111111-2222-3333-4444-555555555555";

function be(overrides: Partial<BeCloudRequest> = {}): BeCloudRequest {
  return {
    id: ID,
    number: "WCR0000001",
    type: "lock_user",
    targetId: "scim-1",
    targetLabel: "jane@example.com",
    reason: "Suspicious sign-ins.",
    customerConsent: null,
    tier: null,
    limits: null,
    previousTier: null,
    previousLimits: null,
    status: "pending",
    requestedBy: "engineer@example.com",
    requestedOn: "2026-11-09T10:00:00Z",
    resolvedBy: null,
    resolvedOn: null,
    remark: null,
    ...overrides,
  };
}

function api(overrides: Partial<Record<"get" | "post" | "delete", ReturnType<typeof vi.fn>>>): BackendApi {
  return { get: vi.fn(), post: vi.fn(), delete: vi.fn(), ...overrides } as unknown as BackendApi;
}

describe("toCloudRequest", () => {
  it("maps a user request, leaving out what is absent", () => {
    const r = toCloudRequest(be());
    expect(r).toMatchObject({
      id: ID,
      number: "WCR0000001",
      target: { kind: "user", id: "scim-1", label: "jane@example.com" },
      summary: "Lock user jane@example.com",
      reason: "Suspicious sign-ins.",
      status: "pending",
    });
    expect(r.tier).toBeUndefined();
    expect(r.resolvedBy).toBeUndefined();
  });

  it("maps an upgrade with its tiers and limits", () => {
    const limits = { monthlyActiveUsers: 5, serviceProviders: 1, identityProviders: 1, subOrganizations: 0, supportLevel: "standard" as const };
    const r = toCloudRequest(
      be({ type: "upgrade_organization", targetId: "org-1", targetLabel: "acme", reason: null, tier: "enterprise", previousTier: "free", limits }),
    );
    expect(r.target.kind).toBe("organization");
    expect(r.summary).toBe("Free → Enterprise");
    expect(r.limits).toEqual(limits);
  });
});

describe("describeCloudRequest", () => {
  it("names the change", () => {
    expect(describeCloudRequest("upgrade_organization", "acme", "payg", "payg")).toBe("Limits change on Pay as you go");
    expect(describeCloudRequest("upgrade_organization", "acme", "enterprise")).toBe("Upgrade to Enterprise");
    expect(describeCloudRequest("delete_organization", "acme")).toBe("Delete acme");
    expect(describeCloudRequest("unlock_user", "jane@example.com")).toBe("Unlock user jane@example.com");
  });
});

describe("listCloudRequests", () => {
  it("pages through every request", async () => {
    const first = Array.from({ length: 50 }, (_, i) => be({ id: `a-${i}` }));
    const post = vi
      .fn()
      .mockResolvedValueOnce({ requests: first, total: 51 })
      .mockResolvedValueOnce({ requests: [be({ id: "last" })], total: 51 });
    const all = await listCloudRequests(api({ post }));
    expect(all).toHaveLength(51);
    expect(post).toHaveBeenNthCalledWith(1, "/wso2-cloud-requests/search", { filters: {}, pagination: { limit: 50, offset: 0 } });
    expect(post).toHaveBeenNthCalledWith(2, "/wso2-cloud-requests/search", { filters: {}, pagination: { limit: 50, offset: 50 } });
  });

  it("stops on a short page and treats a null list as empty", async () => {
    const post = vi.fn().mockResolvedValue({ requests: null, total: 0 });
    expect(await listCloudRequests(api({ post }))).toEqual([]);
    expect(post).toHaveBeenCalledTimes(1);
  });
});

describe("the single-request calls", () => {
  it("reads one, or null when there is none", async () => {
    const get = vi.fn().mockResolvedValueOnce(be()).mockResolvedValueOnce(null);
    expect((await getCloudRequest(api({ get }), ID))?.number).toBe("WCR0000001");
    expect(await getCloudRequest(api({ get }), ID)).toBeNull();
    expect(get).toHaveBeenCalledWith(`/wso2-cloud-requests/${ID}`);
  });

  it("raises a request with only what was given, trimmed", async () => {
    const post = vi.fn().mockResolvedValue(be());
    await createCloudRequest(api({ post }), {
      type: "lock_user",
      target: { kind: "user", id: "scim-1", label: "jane@example.com" },
      reason: "  Suspicious sign-ins.  ",
      customerConsent: "   ",
    });
    expect(post).toHaveBeenCalledWith("/wso2-cloud-requests", {
      type: "lock_user",
      targetId: "scim-1",
      targetLabel: "jane@example.com",
      reason: "Suspicious sign-ins.",
    });
  });

  it("resolves, sending a remark only when there is one", async () => {
    const post = vi.fn().mockResolvedValue(be({ status: "approved" }));
    await resolveCloudRequest(api({ post }), ID, "approved");
    expect(post).toHaveBeenLastCalledWith(`/wso2-cloud-requests/${ID}/resolve`, { status: "approved" });
    await resolveCloudRequest(api({ post }), ID, "rejected", " No contract. ");
    expect(post).toHaveBeenLastCalledWith(`/wso2-cloud-requests/${ID}/resolve`, { status: "rejected", remark: "No contract." });
  });

  it("cancels", async () => {
    const del = vi.fn().mockResolvedValue(undefined);
    await cancelCloudRequest(api({ delete: del }), ID);
    expect(del).toHaveBeenCalledWith(`/wso2-cloud-requests/${ID}`);
  });
});
