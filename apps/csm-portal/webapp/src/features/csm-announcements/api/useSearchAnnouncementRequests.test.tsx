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

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const postMock = vi.fn();
vi.mock("@api/backend/client", () => ({
  useBackendApi: () => ({ post: postMock }),
}));

// Imported after the mock above so the module picks it up.
import { useSearchAnnouncementRequests } from "@features/csm-announcements/api/useSearchAnnouncementRequests";
import type { AnnouncementRequestState } from "@features/csm-announcements/types/announcementRequests";

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const EMPTY_RESPONSE = { requests: [], total: 0, limit: 10, offset: 0, hasMore: false };

async function searchBody(states: AnnouncementRequestState[], page = 0, pageSize = 10): Promise<unknown> {
  const { result } = renderHook(() => useSearchAnnouncementRequests(states, page, pageSize), { wrapper });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(postMock).toHaveBeenCalledTimes(1);
  expect(postMock.mock.calls[0][0]).toBe("/announcement-requests/search");
  return postMock.mock.calls[0][1];
}

beforeEach(() => {
  postMock.mockReset();
  postMock.mockResolvedValue(EMPTY_RESPONSE);
});

describe("useSearchAnnouncementRequests", () => {
  it("sends one state as the single `state` field, so it works against an entity-service without `states`", async () => {
    expect(await searchBody(["pending_approval"])).toEqual({
      state: "pending_approval",
      pagination: { offset: 0, limit: 10 },
    });
  });

  it("sends several states as a `states` list, and no `state`", async () => {
    expect(await searchBody(["draft", "approved"])).toEqual({
      states: ["approved", "draft"],
      pagination: { offset: 0, limit: 10 },
    });
  });

  it("sends neither field for an empty selection, meaning every state", async () => {
    expect(await searchBody([])).toEqual({ pagination: { offset: 0, limit: 10 } });
  });

  it("drops duplicate states, so a repeated entry is still the single-state shape", async () => {
    expect(await searchBody(["published", "published"])).toEqual({
      state: "published",
      pagination: { offset: 0, limit: 10 },
    });
  });

  it("turns page + pageSize into an offset", async () => {
    expect(await searchBody(["draft"], 2, 10)).toEqual({
      state: "draft",
      pagination: { offset: 20, limit: 10 },
    });
  });

  it("reuses the cached result when the same states are selected in a different order", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const sharedWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    const first = renderHook(() => useSearchAnnouncementRequests(["draft", "approved"], 0, 10), {
      wrapper: sharedWrapper,
    });
    await waitFor(() => expect(first.result.current.isSuccess).toBe(true));
    expect(postMock).toHaveBeenCalledTimes(1);

    const second = renderHook(() => useSearchAnnouncementRequests(["approved", "draft"], 0, 10), {
      wrapper: sharedWrapper,
    });
    await waitFor(() => expect(second.result.current.isSuccess).toBe(true));
    // Within staleTime and the same normalized key: no second request.
    expect(postMock).toHaveBeenCalledTimes(1);
  });
});
