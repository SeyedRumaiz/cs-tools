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

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";

const navigateMock = vi.fn();
const postOutageMutateMock = vi.fn();

vi.mock("react-router", () => ({
  useNavigate: () => navigateMock,
  useLocation: () => ({ state: undefined }),
}));
vi.mock("@context/error-banner/ErrorBannerContext", () => ({
  useErrorBanner: () => ({ showError: vi.fn() }),
}));
vi.mock("@features/csm-operations/api/useOutages", () => ({
  usePostOutage: () => ({ mutate: postOutageMutateMock, isPending: false }),
  useGetOutageMetadata: () => ({ data: undefined }),
}));
vi.mock("@api/useSearchConfigurationItems", () => ({ useSearchConfigurationItems: vi.fn() }));
vi.mock("@features/csm-operations/api/useSearchIncidentsForSelect", () => ({
  useSearchIncidentsForSelect: vi.fn(),
}));
vi.mock("@api/backend/client", () => ({
  BackendApiError: class BackendApiError extends Error {
    status = 500;
  },
}));
vi.mock("@components/AsyncEntitySelect", () => ({ default: () => null }));
vi.mock("@features/csm-operations/components/OutagePublicationNotice", () => ({
  default: ({
    hasConfigurationItem,
    onAcknowledgedChange,
  }: {
    hasConfigurationItem: boolean;
    onAcknowledgedChange: (v: boolean) => void;
  }) =>
    hasConfigurationItem ? (
      <input type="checkbox" aria-label="acknowledge publication" onChange={(e) => onAcknowledgedChange(e.target.checked)} />
    ) : null,
}));
vi.mock("@components/AsyncEntityMultiSelect", () => ({
  default: ({ label, values, onChange }: { label: string; values: string[]; onChange: (next: string[]) => void }) => (
    <input
      aria-label={label}
      value={values.join(",")}
      onChange={(e) => onChange(e.target.value ? e.target.value.split(",") : [])}
    />
  ),
}));


import CreateOutagePage from "@features/csm-operations/pages/CreateOutagePage";

// The picker's hidden <input> carries the whole value as text; changing it is
// how a complete date is entered without driving every section.
function pickerInput(label: string): HTMLInputElement {
  const group = screen.getAllByText(label)[0].closest(".MuiFormControl-root") as HTMLElement;
  return group.querySelector("input") as HTMLInputElement;
}

function fillRequired(): void {
  fireEvent.change(screen.getByLabelText(/Short description/), { target: { value: "Login errors" } });
  fireEvent.mouseDown(screen.getByRole("combobox"));
  fireEvent.click(within(screen.getByRole("listbox")).getByText("Outage"));
}

describe("CreateOutagePage — Begin outage with a blank Begin", () => {
  beforeEach(() => {
    postOutageMutateMock.mockReset();
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 2, 1, 12, 0)); // 12:00 local
  });
  afterEach(() => vi.useRealTimers());

  it("submits with an End still ahead of now", () => {
    render(<CreateOutagePage />);
    fillRequired();
    fireEvent.change(pickerInput("End (leave blank if ongoing)"), { target: { value: "03/01/2026 12:05 PM" } });

    fireEvent.click(screen.getByRole("button", { name: "Begin outage" }));

    expect(postOutageMutateMock).toHaveBeenCalledTimes(1);
  });

  it("re-checks End against the time of the click, not of the last render", () => {
    render(<CreateOutagePage />);
    fillRequired();
    fireEvent.change(pickerInput("End (leave blank if ongoing)"), { target: { value: "03/01/2026 12:05 PM" } });
    expect(screen.getByRole("button", { name: "Begin outage" })).toBeEnabled();

    // The engineer waits; End is now behind the begin the click would stamp.
    act(() => {
      vi.setSystemTime(new Date(2026, 2, 1, 12, 10));
    });
    fireEvent.click(screen.getByRole("button", { name: "Begin outage" }));

    expect(postOutageMutateMock).not.toHaveBeenCalled();
    expect(screen.getByText("End must be after now, since begin is blank.")).toBeInTheDocument();
  });

  it("refuses a half-typed Begin instead of replacing it with now", () => {
    render(<CreateOutagePage />);
    fillRequired();

    // Type only the month: the field keeps it, but never calls onChange.
    const beginGroup = screen.getAllByText("Begin (optional)")[0].closest(".MuiFormControl-root") as HTMLElement;
    const month = within(beginGroup).getAllByRole("spinbutton")[0];
    fireEvent.focus(month);
    month.textContent = "1";
    fireEvent.input(month);
    expect(pickerInput("Begin (optional)").value).not.toBe("");

    fireEvent.click(screen.getByRole("button", { name: "Begin outage" }));

    expect(postOutageMutateMock).not.toHaveBeenCalled();
    expect(screen.getByText("Finish the date and time, or clear it to start now.")).toBeInTheDocument();
  });
});

describe("CreateOutagePage — email opt-ins", () => {
  beforeEach(() => postOutageMutateMock.mockReset());

  it("sends neither opt-in nor label when left untouched, as ServiceNow's unticked boxes", () => {
    render(<CreateOutagePage />);
    fillRequired();
    fireEvent.click(screen.getByRole("button", { name: "Begin outage" }));

    const payload = postOutageMutateMock.mock.calls[0][0];
    expect(payload).not.toHaveProperty("notifyInternalStakeholders");
    expect(payload).not.toHaveProperty("outageCommunication");
    expect(payload).not.toHaveProperty("impact");
    expect(payload).not.toHaveProperty("state");
  });

  it("sends both opt-ins and the trimmed labels when set", () => {
    render(<CreateOutagePage />);
    fillRequired();
    fireEvent.click(screen.getByLabelText(/Notify internal stakeholders/));
    fireEvent.click(screen.getByLabelText(/Outage communication/));
    fireEvent.change(screen.getByLabelText("Impact"), { target: { value: " 2 - High " } });
    fireEvent.change(screen.getByLabelText("Current status"), { target: { value: "Investigating" } });
    fireEvent.click(screen.getByRole("button", { name: "Begin outage" }));

    expect(postOutageMutateMock.mock.calls[0][0]).toMatchObject({
      notifyInternalStakeholders: true,
      outageCommunication: true,
      impact: "2 - High",
      state: "Investigating",
    });
  });
});
