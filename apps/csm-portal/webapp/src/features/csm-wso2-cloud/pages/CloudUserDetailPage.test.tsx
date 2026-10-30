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
import CloudUserDetailPage from "./CloudUserDetailPage";

vi.mock("@hooks/useIdTokenClaims", () => ({
  useIdTokenClaims: () => ({ email: "engineer@example.com" }),
}));

function renderPage(userId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/wso2-cloud/users/${userId}`]}>
        <Routes>
          <Route path="/wso2-cloud/users/:userId" element={<CloudUserDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CloudUserDetailPage", () => {
  it("offers Unlock, not Lock, for a locked user", async () => {
    renderPage("u-1002");

    expect(await screen.findByRole("heading", { name: "john.smith@example.com" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Unlock" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Lock" })).not.toBeInTheDocument();
  });

  it("raises a request instead of changing the user, and says it is waiting", async () => {
    renderPage("u-1001");
    fireEvent.click(await screen.findByRole("button", { name: "Disable" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Nothing changes until it is approved/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Request disable" }));

    expect(await screen.findByText(/Request raised/)).toBeInTheDocument();
    expect(await screen.findByText(/Waiting for approval: Disable user/)).toBeInTheDocument();
    // Still active: approval is what disables the account.
    expect(await screen.findByRole("button", { name: "Disable" })).toBeInTheDocument();
  });

  it("says why when the same request is already waiting", async () => {
    await createRequest(
      {
        type: "lock_user",
        target: { kind: "user", id: "u-1003", label: "alex.lee@example.org" },
        summary: "Lock alex.lee@example.org",
      },
      "someone@example.com",
    );
    renderPage("u-1003");
    fireEvent.click(await screen.findByRole("button", { name: "Lock" }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Request lock" }));

    expect(
      await within(dialog).findByText(/already has a pending lock user request/),
    ).toBeInTheDocument();
  });

  it("says so when there is no such user", async () => {
    renderPage("u-missing");
    expect(await screen.findByText("User not found")).toBeInTheDocument();
  });
});
