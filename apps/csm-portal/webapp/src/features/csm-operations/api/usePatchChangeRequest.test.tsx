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

import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";

const patchMock = vi.fn();
const invalidateQueriesMock = vi.fn();

// The real client reads runtime config at module load, which isn't present
// under vitest; stub it (same approach as useDecideChangeRequestApproval.test.tsx).
vi.mock("@api/backend/client", () => ({
  BackendApiError: class BackendApiError extends Error {},
  useBackendApi: () => ({ patch: patchMock }),
}));

import { usePatchChangeRequest } from "@features/csm-operations/api/usePatchChangeRequest";

/**
 * Query-client wrapper for `renderHook`, with `invalidateQueries` swapped for a
 * spy — these tests assert that the mutation invalidates cached detail and list
 * data on settle (success *and* error), so the call itself is the thing under
 * test. Retries are off so an error case settles on the first rejection.
 */
function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  queryClient.invalidateQueries = invalidateQueriesMock.mockImplementation(
    () => Promise.resolve(),
  );
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("usePatchChangeRequest", () => {
  beforeEach(() => {
    patchMock.mockReset();
    invalidateQueriesMock.mockReset();
  });

  it("PATCHes /change-requests/{id} with the given patch", async () => {
    patchMock.mockResolvedValue({
      message: "ok",
      changeRequest: { id: "cr-1", state: "assess" },
    });
    const { result } = renderHook(() => usePatchChangeRequest(), { wrapper });

    act(() => {
      result.current.mutate({ id: "cr-1", patch: { requestApproval: true } });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(patchMock).toHaveBeenCalledWith("/change-requests/cr-1", {
      requestApproval: true,
    });
  });

  it("invalidates the CR's detail, the list, and its approvals on success", async () => {
    patchMock.mockResolvedValue({
      message: "ok",
      changeRequest: { id: "cr-1", state: "assess" },
    });
    const { result } = renderHook(() => usePatchChangeRequest(), { wrapper });

    act(() => {
      result.current.mutate({ id: "cr-1", patch: { requestApproval: true } });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(invalidateQueriesMock).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["change-request-details", "cr-1"] }),
    );
    expect(invalidateQueriesMock).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["change-requests"] }),
    );
    // Entering Assess can auto-provision approvers server-side (the assigned
    // team's own members), so the Approvals tab's cache has to be
    // invalidated too -- otherwise it keeps showing "no approval stages"
    // until an unrelated full page reload refetches it independently.
    expect(invalidateQueriesMock).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["change-request-approvals", "cr-1"] }),
    );
  });

  // The regression this covers: a state-changing patch (requestApproval) can
  // commit upstream and still come back as an error here — a response-parse
  // failure on a slim success receipt has done exactly this. If the cached
  // detail isn't refetched after such an error, the next render still shows
  // the record's *old* state, so a state-gated action (like Request
  // approval) looks available again even though it has already happened —
  // exactly the "state changed to Assess despite the reported failure, retry
  // rejected" sequence reported against this transition. Invalidating on
  // error too keeps the next read honest regardless of which side of that
  // ambiguity a given failure falls on.
  it("also invalidates the CR's detail and the list on error, so a stale cached state can't make a completed action look available again", async () => {
    const upstreamError = new Error("Invalid state transition");
    patchMock.mockRejectedValue(upstreamError);
    const { result } = renderHook(() => usePatchChangeRequest(), { wrapper });

    act(() => {
      result.current.mutate({ id: "cr-1", patch: { requestApproval: true } });
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBe(upstreamError);
    expect(invalidateQueriesMock).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["change-request-details", "cr-1"] }),
    );
    expect(invalidateQueriesMock).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["change-requests"] }),
    );
    expect(invalidateQueriesMock).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["change-request-approvals", "cr-1"] }),
    );
  });
});
