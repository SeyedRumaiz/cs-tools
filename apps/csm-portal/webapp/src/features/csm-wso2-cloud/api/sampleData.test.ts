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
    const [org] = await api.searchOrganizations("initech");
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
