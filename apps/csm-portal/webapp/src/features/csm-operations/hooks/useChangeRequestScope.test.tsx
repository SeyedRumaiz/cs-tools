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

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ScopeDeployment } from "@features/csm-operations/api/useChangeRequestScopeLookups";
import type { BeCustomerContact } from "@api/backend/types";

// The lookup is the only thing mocked: the cascade under test is the hook's own.
// `lookupDeployments` / `lookupContacts` are what the "backend" currently returns
// per project; a test can change them between renders to model data arriving late.
let lookupDeployments: Record<string, ScopeDeployment[]> = {};
let lookupContacts: Record<string, BeCustomerContact[]> = {};
vi.mock("@features/csm-operations/api/useChangeRequestScopeLookups", () => ({
  useChangeRequestScopeLookups: (projectId: string | undefined) => ({
    deployments: projectId ? (lookupDeployments[projectId] ?? []) : [],
    customerContacts: projectId ? (lookupContacts[projectId] ?? []) : [],
    contactsReady: !!projectId && projectId in lookupContacts,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
}));

import { useChangeRequestScope } from "@features/csm-operations/hooks/useChangeRequestScope";

const dep = (id: string, products: Array<[string, string]> | undefined): ScopeDeployment => ({
  id,
  label: `Deployment ${id}`,
  products: products?.map(([pid, label]) => ({ id: pid, label })),
});

beforeEach(() => {
  lookupDeployments = {
    p1: [
      dep("d1", [["x1", "APIM"], ["x2", "IS"]]),
      dep("d2", [["x3", "APIM staging"]]),
      dep("d3", [["x4", "Choreo"]]),
    ],
    p2: [dep("d9", [["x9", "Other"]])],
  };
  lookupContacts = {
    p1: [{ id: "c1", name: "Alice Aaron" }, { id: "c2", name: "Bob Bell" }],
    p2: [{ id: "c9", name: "Carol Cook" }],
    p3: [],
  };
});

describe("useChangeRequestScope", () => {
  it("starts empty with nothing selectable below the project", () => {
    const { result } = renderHook(() => useChangeRequestScope());
    expect(result.current).toMatchObject({
      projectId: "",
      deploymentIds: [],
      deploymentProductIds: [],
      deploymentOptions: [],
      customerContacts: [],
      customerContactsReady: false,
      productsReady: true,
    });
    // No environments concept any more: a deployment carries its own type.
    expect(Object.keys(result.current)).not.toContain("environmentIds");
    expect(Object.keys(result.current)).not.toContain("environmentOptions");
  });

  it("offers the picked project's deployments", () => {
    const { result } = renderHook(() => useChangeRequestScope());
    act(() => result.current.setProject("p1", "Project One"));
    expect(result.current.projectLabel).toBe("Project One");
    expect(result.current.deploymentOptions.map((d) => d.id)).toEqual(["d1", "d2", "d3"]);
  });

  it("derives the deployment products of the chosen deployments", () => {
    const { result } = renderHook(() => useChangeRequestScope());
    act(() => result.current.setProject("p1"));
    act(() => result.current.setDeployments(["d1"]));
    expect(result.current.deploymentProductIds).toEqual(["x1", "x2"]);

    act(() => result.current.setDeployments(["d1", "d2"]));
    expect(result.current.deploymentProductIds).toEqual(["x1", "x2", "x3"]);

    act(() => result.current.setDeployments(["d2"]));
    expect(result.current.deploymentProductIds).toEqual(["x3"]);
  });

  it("clears the deployments and products when the project changes", () => {
    const { result } = renderHook(() => useChangeRequestScope());
    act(() => result.current.setProject("p1"));
    act(() => result.current.setDeployments(["d1"]));
    act(() => result.current.setProject("p2", "Project Two"));
    expect(result.current).toMatchObject({
      projectId: "p2",
      projectLabel: "Project Two",
      deploymentIds: [],
      deploymentProductIds: [],
    });
    expect(result.current.deploymentOptions.map((d) => d.id)).toEqual(["d9"]);
  });

  it("clears everything when the project is cleared", () => {
    const { result } = renderHook(() => useChangeRequestScope());
    act(() => result.current.setProject("p1", "Project One"));
    act(() => result.current.setDeployments(["d1"]));
    act(() => result.current.setProject(""));
    expect(result.current).toMatchObject({ projectId: "", projectLabel: "", deploymentIds: [], customerContacts: [] });
  });

  it("leaves everything alone when the same project is picked again", () => {
    const { result } = renderHook(() => useChangeRequestScope());
    act(() => result.current.setProject("p1"));
    act(() => result.current.setDeployments(["d1"]));
    act(() => result.current.setProject("p1"));
    expect(result.current.deploymentIds).toEqual(["d1"]);
  });

  describe("the read-only customer group (the project's registered contacts)", () => {
    it("is empty and not ready until a project is chosen", () => {
      const { result } = renderHook(() => useChangeRequestScope());
      expect(result.current.customerContacts).toEqual([]);
      expect(result.current.customerContactsReady).toBe(false);
    });

    it("is the chosen project's contacts, and nothing else", () => {
      const { result } = renderHook(() => useChangeRequestScope());
      act(() => result.current.setProject("p1"));
      expect(result.current.customerContactsReady).toBe(true);
      expect(result.current.customerContacts.map((c) => c.name)).toEqual(["Alice Aaron", "Bob Bell"]);
    });

    it("re-derives when the project changes: another customer's contacts never carry over", () => {
      const { result } = renderHook(() => useChangeRequestScope());
      act(() => result.current.setProject("p1"));
      act(() => result.current.setProject("p2"));
      expect(result.current.customerContacts.map((c) => c.name)).toEqual(["Carol Cook"]);
    });

    it("is known-empty for a project with no registered contacts", () => {
      const { result } = renderHook(() => useChangeRequestScope());
      act(() => result.current.setProject("p3"));
      expect(result.current.customerContactsReady).toBe(true);
      expect(result.current.customerContacts).toEqual([]);
    });

    it("goes away with the project", () => {
      const { result } = renderHook(() => useChangeRequestScope());
      act(() => result.current.setProject("p1"));
      act(() => result.current.setProject(""));
      expect(result.current.customerContacts).toEqual([]);
      expect(result.current.customerContactsReady).toBe(false);
    });

    it("is not part of the seed: a draft or clone carrying a stale group id has nothing to restore", () => {
      const { result } = renderHook(() =>
        useChangeRequestScope({ projectId: "p2", customerGroupId: "stale-group" } as never),
      );
      expect(result.current.customerContacts.map((c) => c.name)).toEqual(["Carol Cook"]);
      expect(Object.keys(result.current)).not.toContain("customerGroupId");
    });
  });

  describe("seeded (draft / edit dialog)", () => {
    const seed = {
      projectId: "p1",
      projectLabel: "Project One",
      deployments: [{ id: "d1", label: "Deployment d1" }, { id: "d2", label: "Deployment d2" }],
      deploymentProducts: [{ id: "x1", label: "APIM" }],
    };

    it("keeps the seeded deployments", () => {
      const { result } = renderHook(() => useChangeRequestScope(seed));
      expect(result.current.deploymentIds).toEqual(["d1", "d2"]);
    });

    it("shows the seeded names for ids the lookup has not resolved", () => {
      lookupDeployments = {};
      const { result } = renderHook(() => useChangeRequestScope(seed));
      expect(result.current.deploymentLabels).toMatchObject({ d1: "Deployment d1", d2: "Deployment d2" });
    });

    it("stands the seeded products in until the lookup has settled, and does not claim they are ready", () => {
      // Products unknown (still loading) for the chosen deployments.
      lookupDeployments = { p1: [dep("d1", undefined), dep("d2", undefined)] };
      const { result } = renderHook(() => useChangeRequestScope(seed));
      expect(result.current.deploymentProductIds).toEqual(["x1"]);
      expect(result.current.productsReady).toBe(false);
    });

    it("switches to the freshly derived products once they settle", () => {
      lookupDeployments = { p1: [dep("d1", undefined), dep("d2", undefined)] };
      const { result, rerender } = renderHook(() => useChangeRequestScope(seed));
      lookupDeployments = {
        p1: [dep("d1", [["x1", "APIM"], ["x2", "IS"]]), dep("d2", [["x3", "APIM staging"]])],
      };
      rerender();
      expect(result.current.deploymentProductIds).toEqual(["x1", "x2", "x3"]);
      expect(result.current.productsReady).toBe(true);
    });

    it("shows the project's contacts for a seeded project right away", () => {
      const { result } = renderHook(() => useChangeRequestScope(seed));
      expect(result.current.customerContacts.map((c) => c.name)).toEqual(["Alice Aaron", "Bob Bell"]);
    });
  });
});
