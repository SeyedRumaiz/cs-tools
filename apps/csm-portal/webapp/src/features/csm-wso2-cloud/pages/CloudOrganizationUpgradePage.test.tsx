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
import { fakeCloudBackend, SF_ACTIVE, SF_LAPSED } from "@features/csm-wso2-cloud/test/fakeCloudRequestsBackend";
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
          <Route path="/wso2-cloud/requests/:requestId" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function choose(label: string, option: string | RegExp) {
  fireEvent.mouseDown(await screen.findByRole("combobox", { name: label }));
  fireEvent.click(await within(await screen.findByRole("listbox")).findByRole("option", { name: option }));
}

/** Picks a subscription and its Enterprise plan, the rest of the form left as it is. */
async function choosePlan() {
  await choose("Subscription", /identity: identity-payg/);
  await choose("Enterprise plan", "Enterprise (identity-enterprise)");
}

async function chooseKind(kind: "Customer" | "Internal") {
  fireEvent.click(await screen.findByRole("radio", { name: new RegExp(`^${kind} upgrade`) }));
}

/** Types an account id and checks it. */
async function checkAccount(id: string) {
  fireEvent.change(await screen.findByRole("textbox", { name: "Salesforce account ID" }), { target: { value: id } });
  fireEvent.click(screen.getByRole("button", { name: "Check" }));
}

describe("CloudOrganizationUpgradePage", () => {
  beforeEach(() => {
    fakeCloudBackend.reset();
  });

  it("raises an upgrade of one subscription to an Enterprise plan", async () => {
    fakeCloudBackend.regions = ["us", "eu"];
    renderPage();
    await choose("Region", "Europe (eu)");
    await choose("Subscription", /identity: identity-payg/);
    // Only the product's own plans, without the one it is already on.
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Enterprise plan" }));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).queryByRole("option", { name: /identity-payg/ })).not.toBeInTheDocument();
    fireEvent.click(within(listbox).getByRole("option", { name: "Enterprise (identity-enterprise)" }));
    await choose("Billing period", "Annual");
    fireEvent.click(screen.getByRole("checkbox", { name: "Email the customer about the plan change" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "Signed contract" } });
    await chooseKind("Customer");
    await checkAccount(SF_ACTIVE.id.slice(0, 15));
    expect(await screen.findByText(/has an active subscription/)).toBeInTheDocument();
    expect(screen.getByText(/Acme support 2027, ends 2027-06-30/)).toBeInTheDocument();

    // A customer upgrade needs no approval: it is applied at once and the page opens it.
    fireEvent.click(screen.getByRole("button", { name: "Upgrade now" }));

    const raised = await waitFor(() => {
      const r = fakeCloudBackend.requests.find((x) => x.targetId === ORG_ID);
      if (!r) throw new Error("not raised yet");
      return r;
    });
    expect(await screen.findByText(`at /wso2-cloud/requests/${raised.id}`)).toBeInTheDocument();
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
      status: "approved",
      resolvedBy: fakeCloudBackend.me,
      salesforceLinkStatus: "linked",
      upgradeKind: "customer",
      salesforceAccountId: SF_ACTIVE.id,
      region: "eu",
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
    await chooseKind("Internal");
    fakeCloudBackend.plans = {};
    fireEvent.click(screen.getByRole("button", { name: "Submit for approval" }));
    expect(await screen.findByText("planCode is not one of the Enterprise plans configured for this product.")).toBeInTheDocument();
    await waitFor(() => expect(fakeCloudBackend.requests.every((r) => r.planCode !== "identity-gold")).toBe(true));
  });
});

describe("CloudOrganizationUpgradePage: Salesforce", () => {
  beforeEach(() => {
    fakeCloudBackend.reset();
  });

  it("needs a kind before it can be submitted", async () => {
    renderPage();
    await choosePlan();
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeDisabled();
    await chooseKind("Internal");
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeEnabled();
  });

  it("raises an internal upgrade without an account", async () => {
    renderPage();
    await choosePlan();
    await chooseKind("Internal");
    expect(screen.queryByRole("textbox", { name: "Salesforce account ID" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Submit for approval" }));
    expect(await screen.findByText("at /wso2-cloud/requests?tab=mine")).toBeInTheDocument();
    // An internal upgrade names its region too: the billing API of that region applies it.
    expect(fakeCloudBackend.requests[0]).toMatchObject({ upgradeKind: "internal", region: "us" });
    expect(fakeCloudBackend.requests[0].salesforceAccountId).toBeUndefined();
  });

  it("starts as a customer upgrade with the account and region the organisation is already under", async () => {
    fakeCloudBackend.regions = ["us", "eu"];
    fakeCloudBackend.salesforceOrgs[ORG_ID] = { accountId: SF_ACTIVE.id, region: "eu" };
    renderPage();
    expect(await screen.findByText(/In Salesforce under Acme Corp/)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /^Customer upgrade/ })).toBeChecked();
    expect(screen.getByRole("textbox", { name: "Salesforce account ID" })).toHaveValue(SF_ACTIVE.id);
    expect(screen.getByRole("combobox", { name: "Region" })).toHaveTextContent("Europe (eu)");
    expect(screen.getByText(/From the organization's Salesforce record/)).toBeInTheDocument();
    await choosePlan();
    fireEvent.click(screen.getByRole("button", { name: "Upgrade now" }));
    expect(await screen.findByText(/^at \/wso2-cloud\/requests\/[0-9a-f-]+$/)).toBeInTheDocument();
    expect(fakeCloudBackend.requests[0]).toMatchObject({
      upgradeKind: "customer",
      salesforceAccountId: SF_ACTIVE.id,
      region: "eu",
      status: "approved",
    });
  });

  it("will not raise a customer upgrade for an account with no active subscription", async () => {
    renderPage();
    await choosePlan();
    await chooseKind("Customer");
    await checkAccount(SF_LAPSED.id);
    expect(await screen.findByText(/Initech \(001000000000002AAA\) has no active subscription/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Upgrade now" })).toBeDisabled();
  });

  it("keeps a customer upgrade that cannot be applied pending, and says why", async () => {
    fakeCloudBackend.applyError = "WSO2 Cloud is not connected yet, so the change cannot be applied. The request stays pending.";
    fakeCloudBackend.salesforceOrgs[ORG_ID] = { accountId: SF_ACTIVE.id, region: "eu" };
    renderPage();
    await screen.findByText(/In Salesforce under Acme Corp/);
    await choosePlan();
    fireEvent.click(screen.getByRole("button", { name: "Upgrade now" }));
    expect(await screen.findByText(/The request stays pending/)).toBeInTheDocument();
    expect(fakeCloudBackend.requests[0]).toMatchObject({ targetId: ORG_ID, status: "pending" });
  });

  it("preselects the only region this environment has", async () => {
    renderPage();
    const region = await screen.findByRole("combobox", { name: "Region" });
    expect(region).toHaveTextContent("United States (us)");
    expect(region).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByText("This environment has one WSO2 Cloud region.")).toBeInTheDocument();
    expect(await screen.findByRole("combobox", { name: "Subscription" })).toBeInTheDocument();
  });

  it("asks for the region before loading subscriptions when there is more than one", async () => {
    fakeCloudBackend.regions = ["us", "eu"];
    renderPage();
    expect(await screen.findByText(/Choose the organization's WSO2 Cloud region to load its subscriptions/)).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Subscription" })).not.toBeInTheDocument();
    await choose("Region", "United States (us)");
    expect(await screen.findByRole("combobox", { name: "Subscription" })).toBeInTheDocument();
  });

  it("says when the organisation is not in the chosen region", async () => {
    fakeCloudBackend.regions = ["us", "eu"];
    fakeCloudBackend.orgRegions[ORG_ID] = "us";
    renderPage();
    await choose("Region", "Europe (eu)");
    expect(await screen.findByText("This organization was not found in the EU region. Check the region.")).toBeInTheDocument();
    await choose("Region", "United States (us)");
    expect(await screen.findByRole("combobox", { name: "Subscription" })).toBeInTheDocument();
  });

  it("starts the subscription again when the region changes", async () => {
    fakeCloudBackend.regions = ["us", "eu"];
    renderPage();
    await choose("Region", "United States (us)");
    await choosePlan();
    await chooseKind("Internal");
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeEnabled();
    await choose("Region", "Europe (eu)");
    expect(await screen.findByRole("combobox", { name: "Subscription" })).not.toHaveTextContent(/identity/);
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeDisabled();
  });

  it("says when Salesforce has no such account", async () => {
    renderPage();
    await chooseKind("Customer");
    await checkAccount("001999999999999AAA");
    expect(await screen.findByText("Salesforce has no account with this ID.")).toBeInTheDocument();
  });

  it("does not check an id that is not an Account id", async () => {
    renderPage();
    await chooseKind("Customer");
    fireEvent.change(await screen.findByRole("textbox", { name: "Salesforce account ID" }), { target: { value: "006000000000001" } });
    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
  });

  it("asks to check again once the id is changed", async () => {
    renderPage();
    await chooseKind("Customer");
    await checkAccount(SF_ACTIVE.id);
    expect(await screen.findByText(/has an active subscription/)).toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "Salesforce account ID" }), { target: { value: SF_LAPSED.id } });
    expect(screen.queryByText(/has an active subscription/)).not.toBeInTheDocument();
  });

  it("offers only an internal upgrade when Salesforce is not connected", async () => {
    fakeCloudBackend.salesforceConnected = false;
    renderPage();
    expect(await screen.findByText(/Salesforce is not connected yet/)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /^Customer upgrade/ })).toBeDisabled();
    await choosePlan();
    await chooseKind("Internal");
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeEnabled();
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
