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

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { createRequest } from "@features/csm-wso2-cloud/api/sampleData";
import CloudRequestsPage from "./CloudRequestsPage";

vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));
vi.mock("@hooks/useIdTokenClaims", () => ({
  useIdTokenClaims: () => ({ email: "engineer@example.com" }),
}));

function renderPage(tab: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/wso2-cloud/requests?tab=${tab}`]}>
        <Routes>
          <Route path="/wso2-cloud/requests" element={<CloudRequestsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CloudRequestsPage", () => {
  it("cancels one of your own pending requests from My requests", async () => {
    const request = await createRequest(
      {
        type: "disable_user",
        target: { kind: "user", id: "u-1003", label: "alex.lee@example.org" },
        summary: "Disable alex.lee@example.org",
      },
      "engineer@example.com",
    );
    renderPage("mine");

    const row = (await screen.findByText(request.id)).closest("tr");
    if (!row) throw new Error("request row not rendered");
    fireEvent.click(within(row).getByRole("button", { name: "Cancel" }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel request" }));

    expect(await screen.findByText("You have not raised any requests.")).toBeInTheDocument();
  });
});
