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
import { fakeCloudBackend } from "@features/csm-wso2-cloud/test/fakeCloudRequestsBackend";
import { subscriptionBlockedReason } from "@features/csm-wso2-cloud/utils/subscriptions";
import CloudOrganizationUpgradePage from "./CloudOrganizationUpgradePage";

vi.mock("@api/backend/client", async () => {
  const { fakeCloudBackend } = await import("@features/csm-wso2-cloud/test/fakeCloudRequestsBackend");
  return { useBackendApi: () => fakeCloudBackend.api };
});

/** A sample organisation (the organisation pages still read sample data). */
const ORG_ID = "0f6c1a52-5b1e-4c39-9a51-2d7e1f3b8a01";

function LocationProbe(): JSX.Element {
  const location = useLocation();
  return <p>at {`${location.pathname}${location.search}`}</p>;
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/wso2-cloud/organizations/${ORG_ID}/upgrade`]}>
        <Routes>
          <Route path="/wso2-cloud/organizations/:organizationId/upgrade" element={<CloudOrganizationUpgradePage />} />
          <Route path="/wso2-cloud/requests" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function choose(label: string, option: string | RegExp) {
  fireEvent.mouseDown(await screen.findByRole("combobox", { name: label }));
  fireEvent.click(await within(await screen.findByRole("listbox")).findByRole("option", { name: option }));
}

describe("CloudOrganizationUpgradePage", () => {
  beforeEach(() => {
    fakeCloudBackend.reset();
  });

  it("raises an upgrade of one subscription to an Enterprise plan", async () => {
    renderPage();
    await choose("Subscription", /identity: identity-payg/);
    // Only the product's own plans, without the one it is already on.
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Enterprise plan" }));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).queryByRole("option", { name: /identity-payg/ })).not.toBeInTheDocument();
    fireEvent.click(within(listbox).getByRole("option", { name: "Enterprise (identity-enterprise)" }));
    await choose("Billing period", "Annual");
    fireEvent.click(screen.getByRole("checkbox", { name: "Email the customer about the plan change" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "Signed contract" } });

    fireEvent.click(screen.getByRole("button", { name: "Submit for approval" }));

    expect(await screen.findByText("at /wso2-cloud/requests?tab=mine")).toBeInTheDocument();
    const raised = fakeCloudBackend.requests[0];
    expect(raised).toMatchObject({
      type: "upgrade_organization",
      targetId: ORG_ID,
      targetLabel: "acme",
      product: "identity",
      subscriptionId: "sub-identity",
      planCode: "identity-enterprise",
      previousPlanCode: "identity-payg",
      billingPeriod: "annual",
      notifyCustomer: true,
      reason: "Signed contract",
      status: "pending",
    });
  });

  it("will not upgrade a suspended subscription", async () => {
    renderPage();
    await choose("Subscription", /integration: integration-free/);
    expect(screen.getByText(/does not change the plan of a subscription in this state/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeDisabled();
  });

  it("says when no Enterprise plan is configured for the product", async () => {
    fakeCloudBackend.plans = {};
    renderPage();
    await choose("Subscription", /identity: identity-payg/);
    expect(await screen.findByText("No Enterprise plans are configured for identity.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeDisabled();
  });

  it("says when WSO2 Cloud is not connected, and offers nothing to submit", async () => {
    fakeCloudBackend.subscriptions = null;
    renderPage();
    expect(await screen.findByText(/WSO2 Cloud is not connected yet/)).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Subscription" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeDisabled();
  });

  it("shows the backend's refusal and stays on the form", async () => {
    // A plan the form offers but the backend does not know (its config changed).
    fakeCloudBackend.plans = { identity: [{ code: "identity-gold", label: "Gold" }] };
    renderPage();
    await choose("Subscription", /identity: identity-payg/);
    await choose("Enterprise plan", "Gold (identity-gold)");
    fakeCloudBackend.plans = {};
    fireEvent.click(screen.getByRole("button", { name: "Submit for approval" }));
    expect(await screen.findByText("planCode is not one of the Enterprise plans configured for this product.")).toBeInTheDocument();
    await waitFor(() => expect(fakeCloudBackend.requests.every((r) => r.planCode !== "identity-gold")).toBe(true));
  });
});

describe("subscriptionBlockedReason", () => {
  it("blocks the states WSO2 Cloud refuses a plan change for", () => {
    for (const status of ["suspended", "Cancelled", "canceled", "pending_cancellation"]) {
      expect(subscriptionBlockedReason({ id: "s", product: "p", planCode: "c", status })).not.toBeNull();
    }
    for (const status of ["active", "trial"]) {
      expect(subscriptionBlockedReason({ id: "s", product: "p", planCode: "c", status })).toBeNull();
    }
  });
});
