/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { pagingMember } from "../test/fixtures";

const api = {
  get: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  postEmpty: vi.fn(),
};
vi.mock("@api/backend/client", () => ({ useBackendApi: () => api }));
vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));

import {
  PAGING_TEST_POLL_MAX_MS,
  PAGING_TEST_POLL_MS,
  pagingPollInterval,
  useDeletePagingContact,
  usePutPagingContact,
  useTestPagingContact,
} from "./usePagingChain";

const USER = "33333333-3333-3333-3333-333333333333";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const spy = vi.spyOn(qc, "invalidateQueries");
  const W = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  return { W, spy };
}

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
});

describe("paging contact hooks", () => {
  it("PUTs the number to the person's paging contact and redraws the chain and readiness", async () => {
    api.put.mockResolvedValue({ masked: "+94•••••123" });
    const { W, spy } = wrapper();
    const { result } = renderHook(() => usePutPagingContact(), { wrapper: W });
    await act(() => result.current.mutateAsync({ userId: USER, phone: "+94771234123" }));
    expect(api.put).toHaveBeenCalledWith(`/team-schedule/paging-contacts/${USER}`, { phone: "+94771234123" });
    expect(spy).toHaveBeenCalledWith({ queryKey: ["team-schedule", "paging-chain"] });
    expect(spy).toHaveBeenCalledWith({ queryKey: ["team-schedule", "paging-readiness"] });
  });

  it("DELETEs the paging contact", async () => {
    api.del.mockResolvedValue(null);
    const { W } = wrapper();
    const { result } = renderHook(() => useDeletePagingContact(), { wrapper: W });
    await act(() => result.current.mutateAsync(USER));
    expect(api.del).toHaveBeenCalledWith(`/team-schedule/paging-contacts/${USER}`);
  });

  it("POSTs a test call", async () => {
    api.postEmpty.mockResolvedValue({ lastTestStatus: "pending" });
    const { W } = wrapper();
    const { result } = renderHook(() => useTestPagingContact(), { wrapper: W });
    await act(() => result.current.mutateAsync(USER));
    expect(api.postEmpty).toHaveBeenCalledWith(`/team-schedule/paging-contacts/${USER}/test`);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });
});

describe("pagingPollInterval", () => {
  const t0 = Date.parse("2026-10-08T09:00:00Z");
  const pending = (lastTestAt?: string) =>
    pagingMember({
      membershipId: "a",
      userId: USER,
      pagingPhone: { masked: "+94•••••123", setBy: "x", setAt: "y", lastTestStatus: "pending", lastTestAt },
    });

  it("polls every ten seconds while a test call is pending", () => {
    expect(pagingPollInterval([pending("2026-10-08T09:00:00Z")], t0 + 30_000, new Map())).toBe(PAGING_TEST_POLL_MS);
  });

  it("stops once nothing is pending", () => {
    const done = pagingMember({ membershipId: "a", pagingPhone: { masked: "m", setBy: "x", setAt: "y", lastTestStatus: "completed" } });
    expect(pagingPollInterval([done, pagingMember({ membershipId: "b" })], t0, new Map())).toBe(false);
  });

  it("stops after three minutes even while still pending", () => {
    expect(pagingPollInterval([pending("2026-10-08T09:00:00Z")], t0 + PAGING_TEST_POLL_MAX_MS, new Map())).toBe(false);
  });

  it("times a call with no lastTestAt from when it was first seen pending", () => {
    const seen = new Map<string, number>();
    expect(pagingPollInterval([pending()], t0, seen)).toBe(PAGING_TEST_POLL_MS);
    expect(pagingPollInterval([pending()], t0 + 60_000, seen)).toBe(PAGING_TEST_POLL_MS);
    expect(pagingPollInterval([pending()], t0 + PAGING_TEST_POLL_MAX_MS, seen)).toBe(false);
    // Settled: forgotten, so the next test call is timed afresh.
    expect(pagingPollInterval([], t0 + PAGING_TEST_POLL_MAX_MS, seen)).toBe(false);
    expect(seen.size).toBe(0);
  });
});
