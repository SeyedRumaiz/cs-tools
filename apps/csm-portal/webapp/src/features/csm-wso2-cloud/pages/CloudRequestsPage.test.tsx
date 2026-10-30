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
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { createRequest, resolveRequest } from "@features/csm-wso2-cloud/api/sampleData";
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

  it("asks for a reason before rejecting, and shows it with the decision", async () => {
    const request = await createRequest(
      {
        type: "lock_user",
        target: { kind: "user", id: "u-1001", label: "jane.doe@example.com" },
        summary: "Lock jane.doe@example.com",
      },
      "someone@example.com",
    );
    renderPage("pending");

    const row = (await screen.findByText(request.id)).closest("tr");
    if (!row) throw new Error("request row not rendered");
    fireEvent.click(within(row).getByRole("button", { name: "Reject" }));

    const dialog = await screen.findByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Reject request" });
    expect(confirm).toBeDisabled();

    fireEvent.change(within(dialog).getByRole("textbox", { name: /Reason/ }), {
      target: { value: "Raised for the wrong account." },
    });
    fireEvent.click(confirm);

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByText(request.id)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Resolved" }));

    const resolved = (await screen.findByText(request.id)).closest("tr");
    if (!resolved) throw new Error("resolved row not rendered");
    expect(within(resolved).getByText("Rejected")).toBeInTheDocument();
    expect(within(resolved).getByText("Raised for the wrong account.")).toBeInTheDocument();
  });

  it("keeps an approval that could not be applied pending and says why", async () => {
    const target = { kind: "user" as const, id: "u-1002", label: "john.smith@example.com" };
    const lock = await createRequest(
      { type: "lock_user", target, summary: "Lock john.smith@example.com" },
      "someone@example.com",
    );
    const remove = await createRequest(
      { type: "delete_user", target, summary: "Delete john.smith@example.com" },
      "someone@example.com",
    );
    await resolveRequest(remove.id, "approved", "approver@example.com");
    renderPage("pending");

    const row = (await screen.findByText(lock.id)).closest("tr");
    if (!row) throw new Error("request row not rendered");
    fireEvent.click(within(row).getByRole("button", { name: "Approve" }));

    expect(
      await screen.findByText(/the user john.smith@example.com no longer exists/),
    ).toBeInTheDocument();
    expect(within(row).getByText("Pending")).toBeInTheDocument();
  });
});
