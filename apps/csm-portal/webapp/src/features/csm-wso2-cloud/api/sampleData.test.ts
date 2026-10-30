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

// The module keeps its records between calls, so each test loads a fresh copy.
async function load() {
  vi.resetModules();
  return import("@features/csm-wso2-cloud/api/sampleData");
}

describe("resolving a request in the sample data", () => {
  it("locks the user once a lock request is approved", async () => {
    const api = await load();
    const request = await api.createRequest(
      {
        type: "lock_user",
        target: { kind: "user", id: "u-1001", label: "jane.doe@example.com" },
        summary: "Lock jane.doe@example.com",
      },
      "engineer@example.com",
    );

    expect((await api.getUser("u-1001"))?.locked).toBe(false);
    await api.resolveRequest(request.id, "approved", "approver@example.com");
    expect((await api.getUser("u-1001"))?.locked).toBe(true);
  });

  it("keeps the reason a request was rejected", async () => {
    const api = await load();
    const request = await api.createRequest(
      {
        type: "delete_user",
        target: { kind: "user", id: "u-1003", label: "alex.lee@example.org" },
        summary: "Delete alex.lee@example.org",
      },
      "engineer@example.com",
    );

    const rejected = await api.resolveRequest(
      request.id,
      "rejected",
      "approver@example.com",
      "  The customer still uses this account.  ",
    );
    expect(rejected?.remark).toBe("The customer still uses this account.");
    expect(await api.getUser("u-1003")).not.toBeNull();
  });

  it("leaves the user as is when the request is rejected", async () => {
    const api = await load();
    const request = await api.createRequest(
      {
        type: "disable_user",
        target: { kind: "user", id: "u-1001", label: "jane.doe@example.com" },
        summary: "Disable jane.doe@example.com",
      },
      "engineer@example.com",
    );

    await api.resolveRequest(request.id, "rejected", "approver@example.com");
    expect((await api.getUser("u-1001"))?.disabled).toBe(false);
  });

  it("applies the tier and limits of an approved upgrade", async () => {
    const api = await load();
    const [org] = await api.searchOrganizations("acme");
    const limits = { ...org.limits, monthlyActiveUsers: 50000 };
    const request = await api.createRequest(
      {
        type: "upgrade_organization",
        target: { kind: "organization", id: org.id, label: org.handle },
        summary: "Free → Enterprise",
        tier: "enterprise",
        limits,
      },
      "engineer@example.com",
    );

    await api.resolveRequest(request.id, "approved", "approver@example.com");
    const upgraded = await api.getOrganization(org.id);
    expect(upgraded?.tier).toBe("enterprise");
    expect(upgraded?.limits).toEqual(limits);
  });
});

describe("cancelling a request in the sample data", () => {
  const lock = {
    type: "lock_user" as const,
    target: { kind: "user" as const, id: "u-1001", label: "jane.doe@example.com" },
    summary: "Lock jane.doe@example.com",
  };

  it("removes a pending request its requester cancels", async () => {
    const api = await load();
    const request = await api.createRequest(lock, "engineer@example.com");

    await api.cancelRequest(request.id, "engineer@example.com");
    expect((await api.listRequests()).some((r) => r.id === request.id)).toBe(false);
  });

  it("refuses anyone but the requester", async () => {
    const api = await load();
    const request = await api.createRequest(lock, "engineer@example.com");

    await expect(api.cancelRequest(request.id, "someone@example.com")).rejects.toThrow(
      "Only the person who raised a request can cancel it.",
    );
  });

  it("refuses a request that has already been decided", async () => {
    const api = await load();
    const request = await api.createRequest(lock, "engineer@example.com");
    await api.resolveRequest(request.id, "approved", "approver@example.com");

    await expect(api.cancelRequest(request.id, "engineer@example.com")).rejects.toThrow(
      "has already been approved",
    );
  });

  it("never hands a cancelled request's number to a new one", async () => {
    const api = await load();
    const first = await api.createRequest(lock, "engineer@example.com");
    await api.cancelRequest(first.id, "engineer@example.com");
    const second = await api.createRequest(lock, "engineer@example.com");

    expect(second.id).not.toBe(first.id);
  });
});

describe("raising a request in the sample data", () => {
  const disable = {
    type: "disable_user" as const,
    target: { kind: "user" as const, id: "u-1003", label: "alex.lee@example.org" },
    summary: "Disable alex.lee@example.org",
  };

  it("refuses a second pending request of the same type for the same target", async () => {
    const api = await load();
    const first = await api.createRequest(disable, "engineer@example.com");

    await expect(api.createRequest(disable, "someone@example.com")).rejects.toThrow(
      `alex.lee@example.org already has a pending disable user request (${first.id})`,
    );
  });

  it("allows a different type for the same target", async () => {
    const api = await load();
    await api.createRequest(disable, "engineer@example.com");

    await expect(
      api.createRequest({ ...disable, type: "lock_user", summary: "Lock alex.lee@example.org" }, "engineer@example.com"),
    ).resolves.toMatchObject({ status: "pending" });
  });

  it("allows it again once the first is decided or cancelled", async () => {
    const api = await load();
    const first = await api.createRequest(disable, "engineer@example.com");
    await api.resolveRequest(first.id, "rejected", "approver@example.com");
    const second = await api.createRequest(disable, "engineer@example.com");
    await api.cancelRequest(second.id, "engineer@example.com");

    await expect(api.createRequest(disable, "engineer@example.com")).resolves.toMatchObject({
      status: "pending",
    });
  });
});

describe("an approval that cannot be applied", () => {
  it("stays pending when its user was deleted in the meantime", async () => {
    const api = await load();
    const lock = await api.createRequest(
      {
        type: "lock_user",
        target: { kind: "user", id: "u-1002", label: "john.smith@example.com" },
        summary: "Lock john.smith@example.com",
      },
      "engineer@example.com",
    );
    const remove = await api.createRequest(
      {
        type: "delete_user",
        target: { kind: "user", id: "u-1002", label: "john.smith@example.com" },
        summary: "Delete john.smith@example.com",
      },
      "engineer@example.com",
    );
    await api.resolveRequest(remove.id, "approved", "approver@example.com");

    await expect(api.resolveRequest(lock.id, "approved", "approver@example.com")).rejects.toThrow(
      "the user john.smith@example.com no longer exists",
    );
    const after = (await api.listRequests()).find((r) => r.id === lock.id);
    expect(after?.status).toBe("pending");
    expect(after?.resolvedBy).toBeUndefined();

    // Rejecting it is still possible, and is the way to clear it.
    await expect(
      api.resolveRequest(lock.id, "rejected", "approver@example.com", "User was deleted."),
    ).resolves.toMatchObject({ status: "rejected" });
  });

  it("refuses to decide a request twice", async () => {
    const api = await load();
    await expect(api.resolveRequest("REQ-0001", "rejected", "approver@example.com")).rejects.toThrow(
      "REQ-0001 has already been approved.",
    );
  });
});
