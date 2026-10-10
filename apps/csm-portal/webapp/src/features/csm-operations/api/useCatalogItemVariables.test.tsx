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

import type { JSX, ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const getMock = vi.fn();
vi.mock("@api/backend/client", () => ({ useBackendApi: () => ({ get: getMock }) }));
vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));

import { useCatalogItemVariables } from "./useCatalogItemVariables";

function wrapper({ children }: { children: ReactNode }): JSX.Element {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useCatalogItemVariables", () => {
  it("returns the variables sorted by order", async () => {
    getMock.mockResolvedValue({
      variables: [
        { id: "b", questionText: "B", order: 2 },
        { id: "c", questionText: "C" },
        { id: "a", questionText: "A", order: 1 },
      ],
    });
    const { result } = renderHook(() => useCatalogItemVariables("cat-1", "item-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((v) => v.id)).toEqual(["a", "b", "c"]);
  });

  // api.get resolves a GET 404 to null. That must surface as an error, not as
  // an item with no fields: the form would otherwise offer "no additional
  // fields" and let the request be created without its required answers.
  it("fails instead of returning no fields when the catalog item is not found", async () => {
    getMock.mockResolvedValue(null);
    const { result } = renderHook(() => useCatalogItemVariables("cat-1", "item-1"), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
    expect(result.current.error?.message).toBe("Could not load the request form for this catalog item.");
  });

  it("returns an empty list for an item that genuinely has no variables", async () => {
    getMock.mockResolvedValue({ variables: [] });
    const { result } = renderHook(() => useCatalogItemVariables("cat-1", "item-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([]);
  });
});
