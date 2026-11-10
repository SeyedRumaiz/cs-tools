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
import type { JSX } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SEEDED, fakeCloudBackend } from "@features/csm-wso2-cloud/test/fakeCloudRequestsBackend";
import CloudRequestDetailPage from "./CloudRequestDetailPage";

vi.mock("@api/backend/client", async () => {
  const { fakeCloudBackend } = await import("@features/csm-wso2-cloud/test/fakeCloudRequestsBackend");
  return { useBackendApi: () => fakeCloudBackend.api };
});
const access = vi.hoisted(() => ({ isAdmin: false }));
vi.mock("@context/current-user/usePortalAccess", () => ({ usePortalAccess: () => access }));
vi.mock("@hooks/useIdTokenClaims", () => ({
  useIdTokenClaims: () => ({ email: "engineer@example.com" }),
}));

function LocationProbe(): JSX.Element {
  const location = useLocation();
  return <p>at {`${location.pathname}${location.search}`}</p>;
}

function renderPage(requestId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={[
          {
            pathname: `/wso2-cloud/requests/${requestId}`,
            state: { from: "/wso2-cloud/requests?tab=mine" },
          },
        ]}
      >
        <Routes>
          <Route path="/wso2-cloud/requests/:requestId" element={<CloudRequestDetailPage />} />
          <Route path="/wso2-cloud/requests" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function rowFor(label: string): HTMLElement {
  const row = screen.getByRole("cell", { name: label }).closest("tr");
  if (!row) throw new Error(`no row for ${label}`);
  return row;
}

describe("CloudRequestDetailPage", () => {
  beforeEach(() => {
    fakeCloudBackend.reset();
    access.isAdmin = false;
  });

  it("shows an upgrade's tier and limits before and after", async () => {
    renderPage(SEEDED.pendingUpgrade);

    expect(await screen.findByRole("heading", { name: "WCR0000003" })).toBeInTheDocument();
    expect(screen.getByText("What changes when approved")).toBeInTheDocument();

    const tier = rowFor("Tier");
    expect(within(tier).getByText("Free")).toBeInTheDocument();
    expect(within(tier).getByText("Enterprise")).toBeInTheDocument();
    expect(within(tier).getByText("changed")).toBeInTheDocument();

    const mau = rowFor("Monthly active users");
    expect(within(mau).getByText("1,000")).toBeInTheDocument();
    expect(within(mau).getByText("50,000")).toBeInTheDocument();
  });

  it("shows who rejected a request and why, with nothing left to decide", async () => {
    renderPage(SEEDED.rejectedUnlock);

    expect(await screen.findByText("Rejected by")).toBeInTheDocument();
    expect(screen.getByText("pat.morgan@example.com")).toBeInTheDocument();
    expect(screen.getByText(/clears on its own after the lockout period/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
  });

  it("lets the requester cancel it, then goes back", async () => {
    const request = fakeCloudBackend.raise({
      type: "enable_user",
      targetId: "u-1001",
      targetLabel: "jane.doe@example.com",
    });
    renderPage(request.id);

    expect(await screen.findByText("The user can access the system again.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel request" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel request" }));

    expect(await screen.findByText("at /wso2-cloud/requests?tab=mine")).toBeInTheDocument();
  });

  it("offers no Cancel on someone else's request", async () => {
    renderPage(SEEDED.pendingUpgrade);

    expect(await screen.findByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel request" })).not.toBeInTheDocument();
  });

  it("lets an admin cancel someone else's request", async () => {
    access.isAdmin = true;
    fakeCloudBackend.isAdmin = true;
    renderPage(SEEDED.pendingUpgrade);

    fireEvent.click(await screen.findByRole("button", { name: "Cancel request" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel request" }));

    expect(await screen.findByText("at /wso2-cloud/requests?tab=mine")).toBeInTheDocument();
    expect(fakeCloudBackend.requests.some((r) => r.id === SEEDED.pendingUpgrade)).toBe(false);
  });

  it("shows why the backend refuses an approval of your own request", async () => {
    const own = fakeCloudBackend.raise({ type: "enable_user", targetId: "u-1001", targetLabel: "jane.doe@example.com" });
    renderPage(own.id);

    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText("You cannot approve a request you raised.")).toBeInTheDocument();
    expect(fakeCloudBackend.requests.find((r) => r.id === own.id)?.status).toBe("pending");
  });

  it("says so when the request does not exist", async () => {
    renderPage("00000000-0000-4000-8000-000000009999");
    expect(await screen.findByText("Request not found")).toBeInTheDocument();
  });
});
