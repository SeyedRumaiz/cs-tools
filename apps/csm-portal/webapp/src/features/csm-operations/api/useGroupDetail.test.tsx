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

import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { BeGroupDetail } from "@api/backend/types";

const getMock = vi.fn();

// The real client reads runtime config at module load, which isn't present
// under vitest; stub it (same approach as useDecideChangeRequestApproval.test.tsx).
vi.mock("@api/backend/client", () => ({
  BackendApiError: class BackendApiError extends Error {},
  useBackendApi: () => ({ get: getMock }),
}));

import { useGroupDetail } from "@features/csm-operations/api/useGroupDetail";

const GROUP: BeGroupDetail = {
  id: "22222222-2222-4222-8222-222222222222",
  name: "CAB Approval",
  description: "Change Advisory Board",
  email: "cab@example.com",
  manager: { id: "m1", name: "Mia Manager" },
  members: [{ id: "u1", name: "Alice", email: "alice@example.com", userType: "INTERNAL", role: "lead" }],
  total: 1,
};

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

function newClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

describe("useGroupDetail", () => {
  beforeEach(() => {
    getMock.mockReset();
  });

  it("GETs /groups/{id} (id URL-encoded) and returns the group with its members", async () => {
    getMock.mockResolvedValue(GROUP);
    const { result } = renderHook(() => useGroupDetail(GROUP.id), { wrapper: makeWrapper(newClient()) });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(getMock).toHaveBeenCalledWith(`/groups/${GROUP.id}`);
    expect(result.current.data).toEqual(GROUP);
  });

  it("keys the cache by group id under ApiQueryKeys.GROUP_DETAILS, so two groups never share an entry", async () => {
    getMock.mockResolvedValue(GROUP);
    const client = newClient();
    const { result } = renderHook(() => useGroupDetail(GROUP.id), { wrapper: makeWrapper(client) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(client.getQueryData(["group-details", GROUP.id])).toEqual(GROUP);
    expect(client.getQueryData(["group-details", "someone-else"])).toBeUndefined();
  });

  it("does not fetch without an id", () => {
    const { result } = renderHook(() => useGroupDetail(undefined), { wrapper: makeWrapper(newClient()) });
    expect(result.current.fetchStatus).toBe("idle");
    expect(getMock).not.toHaveBeenCalled();
  });

  it("does not fetch while disabled", () => {
    const { result } = renderHook(() => useGroupDetail(GROUP.id, { enabled: false }), {
      wrapper: makeWrapper(newClient()),
    });
    expect(result.current.fetchStatus).toBe("idle");
    expect(getMock).not.toHaveBeenCalled();
  });

  it("resolves to null for an unknown group (the backend's 404) instead of erroring", async () => {
    getMock.mockResolvedValue(null);
    const { result } = renderHook(() => useGroupDetail(GROUP.id), { wrapper: makeWrapper(newClient()) });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toBeNull();
    expect(result.current.isError).toBe(false);
  });

  it("surfaces a failed request as the query error (e.g. 403 for an external caller)", async () => {
    const failure = new Error("forbidden");
    getMock.mockRejectedValue(failure);
    const { result } = renderHook(() => useGroupDetail(GROUP.id), { wrapper: makeWrapper(newClient()) });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBe(failure);
  });

  it("serves a repeat open of the same group from cache (staleTime) without a second request", async () => {
    getMock.mockResolvedValue(GROUP);
    const client = newClient();
    const first = renderHook(() => useGroupDetail(GROUP.id), { wrapper: makeWrapper(client) });
    await waitFor(() => expect(first.result.current.isSuccess).toBe(true));
    first.unmount();

    const second = renderHook(() => useGroupDetail(GROUP.id), { wrapper: makeWrapper(client) });
    expect(second.result.current.data).toEqual(GROUP);
    expect(getMock).toHaveBeenCalledTimes(1);
  });
});
