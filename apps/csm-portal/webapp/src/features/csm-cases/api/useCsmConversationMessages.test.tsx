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
import type { BeComment } from "@api/backend/types";

const getMock = vi.fn();

vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));
vi.mock("@api/backend/client", () => ({
  useBackendApi: () => ({ get: getMock }),
}));

import { useGetCsmConversationMessages } from "@features/csm-cases/api/useCsmConversationMessages";

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

function beComment(id: string, createdOn: string, createdBy: BeComment["createdBy"]): BeComment {
  return { id, type: "comment", content: `content ${id}`, createdOn, createdBy };
}

describe("useGetCsmConversationMessages", () => {
  beforeEach(() => {
    getMock.mockReset();
  });

  // Regression: entity-service's SearchComments returns conversation
  // messages `created_on DESC, id ASC` -- a random-UUID tie-break with no
  // relation to who sent the message first. The backend response here
  // deliberately returns Novera's reply ahead of the user's question (the
  // real tied-timestamp order it sends) to prove the hook re-sorts rather
  // than trusting the wire order.
  it("sorts the user's question before Novera's reply when they share a timestamp", async () => {
    const ts = "2026-06-24T16:19:34Z";
    getMock.mockResolvedValueOnce({
      comments: [
        beComment("reply-1", ts, { id: null, email: "novera@bot", name: "Novera" }),
        beComment("question-1", ts, { id: null, email: "jane.doe@example.com", name: "Jane Doe" }),
      ],
      total: 2,
      limit: 50,
      offset: 0,
      hasMore: false,
    });

    const { result } = renderHook(() => useGetCsmConversationMessages("conv-1"), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data?.map((c) => c.id)).toEqual(["question-1", "reply-1"]);
  });

  it("never calls the API when there is no conversation id", () => {
    // enabled: false -- the query never runs, so it stays pending rather
    // than reaching isSuccess. Nothing to await: a disabled query doesn't
    // fetch on the next tick either.
    const { result } = renderHook(() => useGetCsmConversationMessages(null), { wrapper });

    expect(result.current.data).toBeUndefined();
    expect(getMock).not.toHaveBeenCalled();
  });
});
