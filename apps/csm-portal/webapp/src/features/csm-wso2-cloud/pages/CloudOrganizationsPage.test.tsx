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
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { describe, expect, it, vi } from "vitest";
import CloudOrganizationsPage from "./CloudOrganizationsPage";

// QueryErrorState reaches the real API config, which reads window.config at load.
vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));

function DetailProbe() {
  const location = useLocation();
  return (
    <div>
      <span>detail page</span>
      <span data-testid="from">{(location.state as { from?: string } | null)?.from}</span>
    </div>
  );
}

function renderPage(initialEntry = "/wso2-cloud/organizations") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/wso2-cloud/organizations" element={<CloudOrganizationsPage />} />
          <Route path="/wso2-cloud/organizations/:organizationId" element={<DetailProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CloudOrganizationsPage", () => {
  it("lists every organisation an owner has", async () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Search organizations"), { target: { value: "jane.doe@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByText("acme-dev")).toBeInTheDocument();
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(screen.queryByText("globex")).not.toBeInTheDocument();
  });

  it("opens an organisation directly when its exact handle is searched", async () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Search organizations"), { target: { value: "globex" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByText("detail page")).toBeInTheDocument();
    expect(screen.getByTestId("from")).toHaveTextContent("/wso2-cloud/organizations?q=globex");
  });

  it("lists an exact match instead of reopening it when reached with the query already in the URL", async () => {
    renderPage("/wso2-cloud/organizations?q=globex");

    expect(await screen.findByText("Globex")).toBeInTheDocument();
    expect(screen.queryByText("detail page")).not.toBeInTheDocument();
  });
});
