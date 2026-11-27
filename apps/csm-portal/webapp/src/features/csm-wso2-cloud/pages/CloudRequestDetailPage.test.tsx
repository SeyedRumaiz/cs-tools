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
import type { JSX } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SEEDED, SF_ACTIVE, fakeCloudBackend } from "@features/csm-wso2-cloud/test/fakeCloudRequestsBackend";
import { WSO2_CLOUD_REQUESTS_REFRESH_MS } from "@features/csm-wso2-cloud/api/queryKeys";
import {
  NOT_CARRIED_OUT_APPROVED,
  NOT_CARRIED_OUT_PENDING,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
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

  it("shows the subscription an upgrade moves and its plan before and after", async () => {
    renderPage(SEEDED.pendingUpgrade);

    expect(await screen.findByRole("heading", { name: "WCR0000003" })).toBeInTheDocument();
    expect(screen.getByText("What changes when approved")).toBeInTheDocument();

    expect(within(rowFor("Subscription")).getByText("sub-initech-identity")).toBeInTheDocument();
    const plan = rowFor("Plan");
    expect(within(plan).getByText("identity-free")).toBeInTheDocument();
    expect(within(plan).getByText("identity-enterprise")).toBeInTheDocument();
    expect(within(plan).getByText("changed")).toBeInTheDocument();
    expect(within(rowFor("Billing")).getByText("Wire transfer, annual")).toBeInTheDocument();
    expect(within(rowFor("Notify the customer")).getByText("Yes")).toBeInTheDocument();
    // The account is created by the approval.
    expect(within(rowFor("Billing account")).getByText("Created when approved")).toBeInTheDocument();
  });

  it("shows a decision made by someone else without a reload", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderPage(SEEDED.pendingUpgrade);
      expect(await screen.findByRole("button", { name: "Approve" })).toBeInTheDocument();

      // Approved in another browser: this page made no request of its own.
      await fakeCloudBackend.api.post(`/wso2-cloud-requests/${SEEDED.pendingUpgrade}/resolve`, { status: "approved" });
      await vi.advanceTimersByTimeAsync(WSO2_CLOUD_REQUESTS_REFRESH_MS);

      await waitFor(() => expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument());
      expect(screen.getByText("Approved by")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the billing account an approved upgrade created", async () => {
    renderPage(SEEDED.approvedUpgrade);

    expect(await screen.findByRole("heading", { name: "WCR0000001" })).toBeInTheDocument();
    expect(within(rowFor("Billing account")).getByText("ba-globex")).toBeInTheDocument();
    expect(within(rowFor("Billing")).getByText("Wire transfer")).toBeInTheDocument();
  });

  it("shows who rejected a request and why, with nothing left to decide", async () => {
    renderPage(SEEDED.rejectedUnlock);

    expect(await screen.findByText("Rejected by")).toBeInTheDocument();
    expect(screen.getByText("pat.morgan@example.com")).toBeInTheDocument();
    expect(screen.getByText(/clears on its own after the lockout period/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
    // Nothing was decided to carry out, so nothing is said about carrying it out.
    expect(screen.queryByText(NOT_CARRIED_OUT_APPROVED)).not.toBeInTheDocument();
    expect(screen.queryByText(NOT_CARRIED_OUT_PENDING)).not.toBeInTheDocument();
  });

  it("says a user change is recorded but not carried out, before and after approval", async () => {
    const unlock = fakeCloudBackend.raise(
      { type: "unlock_user", targetId: "u-1002", targetLabel: "john.smith@example.com" },
      "sam.taylor@example.com",
    );
    const { unmount } = renderPage(unlock.id);
    expect(await screen.findByText(NOT_CARRIED_OUT_PENDING)).toBeInTheDocument();
    unmount();

    await fakeCloudBackend.api.post(`/wso2-cloud-requests/${unlock.id}/resolve`, { status: "approved" });
    renderPage(unlock.id);
    expect(await screen.findByText(NOT_CARRIED_OUT_APPROVED)).toBeInTheDocument();
    expect(screen.queryByText(NOT_CARRIED_OUT_PENDING)).not.toBeInTheDocument();
  });

  it("says nothing about carrying out on an upgrade, which is applied", async () => {
    renderPage(SEEDED.approvedUpgrade);
    expect(await screen.findByText("What it changed")).toBeInTheDocument();
    expect(screen.queryByText(NOT_CARRIED_OUT_APPROVED)).not.toBeInTheDocument();
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

/** A pending customer upgrade raised by someone else, so the signed-in engineer can approve it. */
function raiseCustomerUpgrade(): string {
  return fakeCloudBackend.raise(
    {
      type: "upgrade_organization",
      targetId: "org-hooli",
      targetLabel: "hooli",
      product: "identity",
      subscriptionId: "sub-hooli",
      planCode: "identity-enterprise",
      upgradeKind: "customer",
      salesforceAccountId: SF_ACTIVE.id,
      region: "eu",
    },
    "sam.taylor@example.com",
  ).id;
}

describe("CloudRequestDetailPage: Salesforce", () => {
  beforeEach(() => {
    fakeCloudBackend.reset();
    access.isAdmin = false;
  });

  it("shows a customer upgrade's account and region, and that it is linked when approved", async () => {
    renderPage(raiseCustomerUpgrade());
    expect(await screen.findByRole("cell", { name: "Upgrade kind" })).toBeInTheDocument();
    expect(within(rowFor("Upgrade kind")).getByText("Customer")).toBeInTheDocument();
    expect(within(rowFor("Salesforce account")).getByText(SF_ACTIVE.id)).toBeInTheDocument();
    expect(within(rowFor("WSO2 Cloud region")).getByText("Europe (eu)")).toBeInTheDocument();
    expect(screen.getByText(/could not be applied when it was raised/)).toBeInTheDocument();
    expect(screen.getByText("Salesforce")).toBeInTheDocument();
  });

  it("links the organisation once the upgrade is approved", async () => {
    renderPage(raiseCustomerUpgrade());
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/Linked in Salesforce to account 001000000000001AAA/)).toBeInTheDocument();
  });

  it("shows a failed link with its reason, and retries it", async () => {
    fakeCloudBackend.linkError = "Salesforce could not be reached. Retry later.";
    renderPage(raiseCustomerUpgrade());
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/linking the organization in Salesforce failed. Salesforce could not be reached/)).toBeInTheDocument();
    fakeCloudBackend.linkError = null;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/Linked in Salesforce to account/)).toBeInTheDocument();
  });

  it("refuses to approve once the account's subscription has lapsed", async () => {
    const id = raiseCustomerUpgrade();
    fakeCloudBackend.salesforceAccounts[SF_ACTIVE.id] = { ...SF_ACTIVE, hasActiveSubscription: false, activeOpportunities: [] };
    renderPage(id);
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/no longer has an active subscription/)).toBeInTheDocument();
    expect(fakeCloudBackend.requests.find((r) => r.id === id)?.status).toBe("pending");
  });

  it("shows no Salesforce card on an internal upgrade", async () => {
    renderPage(SEEDED.pendingUpgrade);
    expect(await screen.findByRole("cell", { name: "Upgrade kind" })).toBeInTheDocument();
    expect(within(rowFor("Upgrade kind")).getByText("Not recorded (treated as internal)")).toBeInTheDocument();
    expect(screen.queryByText("Salesforce")).not.toBeInTheDocument();
    expect(screen.queryByRole("cell", { name: "Salesforce account" })).not.toBeInTheDocument();
  });
});

describe("CloudRequestDetailPage: a customer upgrade needs no approval", () => {
  beforeEach(() => {
    fakeCloudBackend.reset();
    access.isAdmin = false;
  });

  it("shows who applied it, not who approved it", async () => {
    const id = raiseCustomerUpgrade();
    fakeCloudBackend.me = "sam.taylor@example.com";
    await fakeCloudBackend.api.post(`/wso2-cloud-requests/${id}/resolve`, { status: "approved" });
    renderPage(id);
    expect(await screen.findByText("Applied by")).toBeInTheDocument();
    expect(screen.queryByText("Approved by")).not.toBeInTheDocument();
  });
});
