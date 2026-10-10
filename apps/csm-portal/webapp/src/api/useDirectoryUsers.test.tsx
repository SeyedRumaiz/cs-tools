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

const postMock = vi.fn();

// The real client reads runtime config at module load, which isn't present
// under vitest (same approach as useSearchGroups.test.tsx).
vi.mock("@api/backend/client", () => ({
  useBackendApi: () => ({ post: postMock }),
}));

import { useDirectoryUsers } from "@api/useDirectoryUsers";
import { INTERNAL_USER_ROLES } from "@features/csm-users/types/csmUsers";

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("useDirectoryUsers", () => {
  beforeEach(() => {
    postMock.mockReset();
    postMock.mockResolvedValue({
      users: [{ name: "Jane Doe", email: "jane.doe@example.com" }],
    });
  });

  it("asks the backend for active internal users only, never the unfiltered directory", async () => {
    const { result } = renderHook(() => useDirectoryUsers(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(postMock).toHaveBeenCalledWith(
      "/users/search",
      expect.objectContaining({
        filters: { roleIds: INTERNAL_USER_ROLES, active: true },
      }),
    );
    expect(result.current.data).toEqual([{ name: "Jane Doe", email: "jane.doe@example.com" }]);
  });
});
