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

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import WorkItemsTab from "@features/csm-projects/components/WorkItemsTab";

function renderWorkItemsTab(projectId = "proj-1") {
  return render(
    <MemoryRouter initialEntries={["/customers/projects/proj-1?tab=workItems"]}>
      <WorkItemsTab projectId={projectId} />
    </MemoryRouter>,
  );
}

/** Destination probe: exposes the current URL search string so a test can
 * assert on it after a sub-tab switch. */
function LocationSearchProbe() {
  const location = useLocation();
  return <div data-testid="search-probe">{location.search}</div>;
}

function renderWorkItemsTabWithLocationProbe(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route
          path="/customers/projects/:id"
          element={
            <>
              <WorkItemsTab projectId="proj-1" />
              <LocationSearchProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

const mockCsmIssuesView = vi.fn();

vi.mock("@features/csm-cases/components/CsmIssuesView", () => ({
  default: (props: Record<string, unknown>) => {
    mockCsmIssuesView(props);
    return <div>IssuesView: {props.entityNoun as string}</div>;
  },
}));

vi.mock("@features/csm-projects/components/ConversationsTab", () => ({
  default: ({ projectId }: { projectId: string }) => <div>Conversations for {projectId}</div>,
}));

// usePortalAccess transitively imports the real backend client/config, which
// read window.config at module load (absent under vitest) -- mocked
// directly instead. Default to a caller holding the staff view (e.g.
// cs_engineer); the dedicated describe block below overrides it to exercise
// the Chats gate.
let mockCanViewWorkItemsStaffView = true;
vi.mock("@context/current-user/usePortalAccess", () => ({
  usePortalAccess: () => ({ canViewWorkItemsStaffView: mockCanViewWorkItemsStaffView }),
}));

describe("WorkItemsTab", () => {
  beforeEach(() => {
    mockCanViewWorkItemsStaffView = true;
  });

  it("defaults to a single flat work-items list, locked to this project but unlocked on type", () => {
    renderWorkItemsTab();

    expect(screen.getByText("IssuesView: work items")).toBeInTheDocument();
    expect(mockCsmIssuesView).toHaveBeenCalledWith(
      expect.objectContaining({
        entityNoun: "work items",
        lockedFilters: { projects: ["proj-1"] },
        hideProjectFilter: true,
        typeFilterLabel: "Work item type",
      }),
    );
    // No per-type case-type lock and no fixed detail base path — a mixed
    // list resolves each row's detail link by its own type instead.
    expect(mockCsmIssuesView).not.toHaveBeenCalledWith(
      expect.objectContaining({ hideTypeFilter: true }),
    );
    const lastCallProps = mockCsmIssuesView.mock.calls.at(-1)?.[0] as
      | Record<string, unknown>
      | undefined;
    expect(lastCallProps?.detailBasePath).toBeUndefined();
  });

  it("has no per-case-type sub-tabs (Cases/Service requests/Security reports/Engagements are gone)", () => {
    renderWorkItemsTab();

    expect(screen.queryByText("Cases")).not.toBeInTheDocument();
    expect(screen.queryByText("Service requests")).not.toBeInTheDocument();
    expect(screen.queryByText("Security reports")).not.toBeInTheDocument();
    expect(screen.queryByText("Engagements")).not.toBeInTheDocument();
    expect(screen.getByText("Work items")).toBeInTheDocument();
    expect(screen.getByText("Chats")).toBeInTheDocument();
  });

  it("switches to the Chats sub-tab", () => {
    renderWorkItemsTab();

    fireEvent.click(screen.getByText("Chats"));

    expect(screen.getByText("Conversations for proj-1")).toBeInTheDocument();
    expect(screen.queryByText(/IssuesView/)).not.toBeInTheDocument();
  });

  // Regression tests: the sub-tab used to be plain component state, so a
  // create-flow round trip back to the project always reset it to Cases.
  // It's now kept in the URL (`?subTab=`) instead.
  it("writes the selected sub-tab to the URL's ?subTab= param", () => {
    renderWorkItemsTabWithLocationProbe("/customers/projects/proj-1?tab=workItems");

    fireEvent.click(screen.getByText("Chats"));

    expect(screen.getByTestId("search-probe")).toHaveTextContent("subTab=conversations");
  });

  it("restores the sub-tab named in the URL on mount, instead of always defaulting to the issues list", () => {
    renderWorkItemsTabWithLocationProbe(
      "/customers/projects/proj-1?tab=workItems&subTab=conversations",
    );

    expect(screen.getByText("Conversations for proj-1")).toBeInTheDocument();
    expect(screen.queryByText(/IssuesView/)).not.toBeInTheDocument();
  });
});

// Regression: Chats (the project's pre-case Novera conversations) must be
// hidden for a caller without the Work items staff view (cs_engineer/admin/
// timecard_approver) -- reported live. With only one sub-tab left, the
// sub-tab strip itself disappears too.
describe("WorkItemsTab — caller without the staff view", () => {
  beforeEach(() => {
    mockCanViewWorkItemsStaffView = false;
  });

  it("shows the issues list directly, with no Chats tab and no sub-tab strip at all", () => {
    renderWorkItemsTab();

    expect(screen.getByText("IssuesView: work items")).toBeInTheDocument();
    expect(screen.queryByText("Chats")).not.toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  });

  it("never shows Chats even if a stale ?subTab=conversations is in the URL", () => {
    renderWorkItemsTabWithLocationProbe(
      "/customers/projects/proj-1?tab=workItems&subTab=conversations",
    );

    expect(screen.getByText("IssuesView: work items")).toBeInTheDocument();
    expect(screen.queryByText(/Conversations for/)).not.toBeInTheDocument();
  });
});
