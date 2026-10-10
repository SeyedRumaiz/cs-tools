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

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import ListFilters from "@components/list-view/ListFilters";
import { ThemeProvider, createTheme } from "@wso2/oxygen-ui";

const mockCaseMetadata = {
  caseStates: [{ id: "1", label: "Open" }],
  severities: [{ id: "2", label: "High" }],
  issueTypes: [{ id: "3", label: "Incident" }],
  deploymentTypes: [{ id: "4", label: "Production" }],
};

describe("ListFilters", () => {
  const theme = createTheme();
  const mockOnFilterChange = vi.fn();
  const defaultFilters = {
    statusIds: [] as string[],
    severityIds: [] as string[],
    issueTypes: [] as string[],
    deploymentIds: [] as string[],
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("should render all filter select components", () => {
    render(
      <ThemeProvider theme={theme}>
        <ListFilters
          filters={defaultFilters}
          filterMetadata={mockCaseMetadata}
          onFilterChange={mockOnFilterChange}
        />
      </ThemeProvider>,
    );

    // Check for labels (Status, Severity, Category)
    // MUI renders labels in multiple places (InputLabel, Legend), so we check getAllByText
    expect(screen.getAllByText("Status")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Severity")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Category")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Deployment")[0]).toBeInTheDocument();
  });

  it("should not render deployment filter when hideDeploymentFilter is true", () => {
    render(
      <ThemeProvider theme={theme}>
        <ListFilters
          filters={defaultFilters}
          filterMetadata={mockCaseMetadata}
          onFilterChange={mockOnFilterChange}
          hideDeploymentFilter
        />
      </ThemeProvider>,
    );

    expect(screen.queryByRole("combobox", { name: /Deployment/i })).toBeNull();
  });

  it("should call onFilterChange when a filter is changed", async () => {
    render(
      <ThemeProvider theme={theme}>
        <ListFilters
          filters={defaultFilters}
          filterMetadata={mockCaseMetadata}
          onFilterChange={mockOnFilterChange}
        />
      </ThemeProvider>,
    );

    // Find the Status select trigger by role and name
    const statusSelect = screen.getByRole("combobox", { name: /Status/i });

    // MUI Select uses a hidden input but also a visible div for the value
    // We click the visible div to open the menu
    fireEvent.mouseDown(statusSelect);

    // MUI Select options are rendered in a Portal
    // We use findByText to find the option by its label
    const option = await screen.findByText("Open");
    fireEvent.click(option);

    // Filter key for Status is 'statusIds' (multi-select — value is an array)
    expect(mockOnFilterChange).toHaveBeenCalledWith("statusIds", expect.arrayContaining(["1"]));
  });

  // Regression test: entity-service's "user"/contact rows carry no
  // email-uniqueness constraint, so the same real person can reach this
  // filter twice with their email differently cased or spaced between the
  // two rows -- reported live as the same name appearing more than once in
  // the "Created By" dropdown.
  it("de-duplicates Created By options whose email differs only by case or whitespace, and submits the trimmed value", async () => {
    const contacts = [
      // The "messy" (trailing-space, differently-cased) row is listed FIRST
      // on purpose: it's the one that must NOT win the dedup, since its raw
      // email would otherwise become the submitted filter value and the
      // backend matches createdBy by exact string equality.
      {
        id: "2",
        email: "Jane.Doe@wso2.com ",
        firstName: "Jane",
        lastName: "Doe",
        isCsAdmin: false,
        isCsIntegrationUser: false,
        isSecurityContact: false,
        membershipStatus: "active",
      },
      {
        id: "1",
        email: "jane.doe@wso2.com",
        firstName: "Jane",
        lastName: "Doe",
        isCsAdmin: false,
        isCsIntegrationUser: false,
        isSecurityContact: false,
        membershipStatus: "active",
      },
      {
        id: "3",
        email: "john.smith@wso2.com",
        firstName: "John",
        lastName: "Smith",
        isCsAdmin: false,
        isCsIntegrationUser: false,
        isSecurityContact: false,
        membershipStatus: "active",
      },
    ];

    render(
      <ThemeProvider theme={theme}>
        <ListFilters
          filters={defaultFilters}
          filterMetadata={mockCaseMetadata}
          contacts={contacts}
          onFilterChange={mockOnFilterChange}
        />
      </ThemeProvider>,
    );

    const createdBySelect = screen.getByRole("combobox", { name: /Created By/i });
    fireEvent.mouseDown(createdBySelect);

    const janeOptions = await screen.findAllByText("Jane Doe");
    expect(janeOptions).toHaveLength(1);
    expect(screen.getAllByText("John Smith")).toHaveLength(1);

    fireEvent.click(janeOptions[0]);

    // Only whitespace is trimmed, not case -- the fix deliberately doesn't
    // guess at normalizing case, since the real stored value's casing isn't
    // confirmed to always be lowercase. The one thing that must never
    // happen: submitting the untrimmed value with its trailing space.
    const [, submittedValues] = mockOnFilterChange.mock.calls.at(-1)!;
    expect(submittedValues).not.toContain("Jane.Doe@wso2.com ");
    expect(submittedValues).toContain("Jane.Doe@wso2.com");
  });
});
