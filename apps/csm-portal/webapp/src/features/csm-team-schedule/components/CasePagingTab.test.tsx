/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import type { ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render as rtlRender, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import type { PagingChainResponse, PagingReadinessResponse } from "../types";
import { pagingChain, pagingMember, readinessChain, readinessGap } from "../test/fixtures";
// vi.mock below is hoisted above this import, so the panel gets the mocks.
import CasePagingTab from "./CasePagingTab";

const mutateAsync = vi.fn();
const putMutate = vi.fn();
const delMutate = vi.fn();
const testMutate = vi.fn();
const readiness: { data?: PagingReadinessResponse } = {};
const chain: { data?: PagingChainResponse } = {};

function render(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

vi.mock("@api/backend/client", () => ({
  BackendApiError: class BackendApiError extends Error {
    status = 0;
  },
  useBackendApi: () => ({}),
}));
vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));
vi.mock("@context/error-banner/ErrorBannerContext", () => ({
  useErrorBanner: () => ({ showError: vi.fn() }),
}));
vi.mock("../api/usePagingChain", () => ({
  useGetPagingChain: () => ({ data: chain.data, isLoading: false, isError: false, dataUpdatedAt: 0 }),
  usePatchPagingMember: () => ({ mutateAsync }),
  useGetPagingReadiness: () => ({ data: readiness.data, isLoading: false, isError: false }),
  usePutPagingContact: () => ({ mutate: putMutate, isPending: false }),
  useDeletePagingContact: () => ({ mutate: delMutate, isPending: false }),
  useTestPagingContact: () => ({ mutate: testMutate, isPending: false }),
}));

beforeEach(() => {
  mutateAsync.mockReset();
  putMutate.mockReset();
  delMutate.mockReset();
  testMutate.mockReset();
  chain.data = pagingChain();
  readiness.data = {
    generatedAt: "2026-10-08T03:00:00Z",
    from: "2026-10-08",
    to: "2026-10-14",
    chains: [
      readinessChain({ chain: "CRE", label: "CRE" }),
      readinessChain({
        chain: "SRE_SAAS",
        label: "SaaS SRE",
        ready: false,
        gaps: [readinessGap({ code: "NO_L1", message: "No L1 in TZ2", fix: "rota", zoneCode: "TZ2", date: "2026-10-10" })],
      }),
      readinessChain({ chain: "SRE_IAAS", label: "IaaS SRE" }),
      readinessChain({ chain: "SME", label: "SME" }),
    ],
  };
});

describe("CasePagingTab", () => {
  it("opens on CRE: the editable chain, with its readiness", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} />);
    expect(screen.getByRole("tab", { name: "CRE" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("button", { name: /Edit paging chain/ })).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "CRE paging chain by team and level" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: /^Alpha/ })).toBeInTheDocument();
    expect(screen.getByText(/✓ ready for the next 7 days/)).toBeInTheDocument();
  });

  it("switches to a rota'd chain: read-only, its own readiness, and a way to the rota", () => {
    const onOpenRota = vi.fn();
    render(<CasePagingTab onOpenRota={onOpenRota} />);
    fireEvent.click(screen.getByRole("tab", { name: "SaaS SRE" }));

    expect(screen.getByRole("tab", { name: "SaaS SRE" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("button", { name: /Edit paging chain/ })).not.toBeInTheDocument();
    expect(
      screen.getByText(/People come from the SaaS SRE rota; set them in the Team Schedule views/),
    ).toBeInTheDocument();
    expect(screen.getByText(/⚠ 1 gap/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Open the SaaS SRE rota" }));
    expect(onOpenRota).toHaveBeenLastCalledWith({ family: "SRE", rotaCode: "SRE_SAAS" });

    // A rota gap opens the rota on its zone and day.
    fireEvent.click(screen.getByRole("button", { name: /Open the rota on/ }));
    expect(onOpenRota).toHaveBeenLastCalledWith({
      family: "SRE",
      rotaCode: "SRE_SAAS",
      teamKey: undefined,
      zoneCode: "TZ2",
      date: "2026-10-10",
    });
  });

  it("opens SME on its family, leaving the rota to the page", () => {
    const onOpenRota = vi.fn();
    render(<CasePagingTab onOpenRota={onOpenRota} />);
    fireEvent.click(screen.getByRole("tab", { name: "SME" }));
    fireEvent.click(screen.getByRole("button", { name: "Open the SME rota" }));
    expect(onOpenRota).toHaveBeenCalledWith({ family: "SME", rotaCode: undefined });
  });

  it("opens on the reader's own chain when given one, and the picker still switches", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} initialChain="SME" />);
    expect(screen.getByRole("tab", { name: "SME" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("button", { name: "Open the SME rota" })).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "CRE paging chain by team and level" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "CRE" }));
    expect(screen.getByRole("tab", { name: "CRE" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("table", { name: "CRE paging chain by team and level" })).toBeInTheDocument();
  });

  it("opens an IaaS SRE reader on the IaaS SRE chain", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} initialChain="SRE_IAAS" />);
    expect(screen.getByRole("tab", { name: "IaaS SRE" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("button", { name: "Open the IaaS SRE rota" })).toBeInTheDocument();
  });

  it("offers an SME rota admin every chain, opening on SME", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} initialChain="SME" />);
    expect(screen.getAllByRole("tab")).toHaveLength(5);
    expect(screen.getByRole("tab", { name: "SME" })).toHaveAttribute("aria-selected", "true");
  });

  it("offers the chains in order: CRE, the three SRE sub-teams together, then SME", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} />);
    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
      "CRE",
      "SaaS SRE",
      "IaaS SRE",
      "PaaS SRE",
      "SME",
    ]);
  });

  it("shows PaaS SRE, says why it cannot be picked, and stays put when clicked", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} />);
    const paas = screen.getByRole("tab", { name: "PaaS SRE" });
    expect(paas).toHaveAttribute("aria-disabled", "true");
    expect(paas).toHaveAttribute("title", "No PaaS rota yet");
    fireEvent.click(paas);
    expect(screen.getByRole("tab", { name: "CRE" })).toHaveAttribute("aria-selected", "true");
    expect(paas).toHaveAttribute("aria-selected", "false");
  });

  it("goes back to the editable chain from a read-only one", () => {
    render(<CasePagingTab onOpenRota={vi.fn()} />);
    fireEvent.click(screen.getByRole("tab", { name: "IaaS SRE" }));
    expect(screen.getByText(/People come from the IaaS SRE rota/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "CRE" }));
    expect(screen.getByRole("button", { name: /Edit paging chain/ })).toBeInTheDocument();
  });

  it("shows a phone chip by each person and runs the paging-number actions", () => {
    const base = pagingChain();
    chain.data = {
      ...base,
      members: base.members.map((m) =>
        m.membershipId === "a2"
          ? {
              ...m,
              canEditPhone: true,
              hasProfilePhone: false,
              pagingPhone: { masked: "+94•••••123", phone: "+94771234123", setBy: "x", setAt: "2026-10-01T09:00:00Z" },
            }
          : m.membershipId === "a1"
            ? { ...m, hasProfilePhone: true }
            : m,
      ),
    };
    render(<CasePagingTab onOpenRota={vi.fn()} />);
    // Jane Doe (lead) has a profile number; John Roe (1st responder) a paging one.
    expect(screen.getByText("Profile ✓")).toBeInTheDocument();
    expect(screen.getByText(/Paging number · not tested/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Test call John Roe's paging number" }));
    expect(testMutate).toHaveBeenCalledWith("u-a2", expect.anything());

    fireEvent.click(screen.getByRole("button", { name: "Change John Roe's paging number" }));
    const dialog = screen.getByRole("dialog", { name: "Change paging number for John Roe" });
    expect(within(dialog).getByLabelText("Mobile number (with country code)")).toHaveValue("+94771234123");
    fireEvent.change(within(dialog).getByLabelText("Mobile number (with country code)"), { target: { value: "+94 77 765 4321" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(putMutate).toHaveBeenCalledWith({ userId: "u-a2", phone: "+94777654321" }, expect.anything());

    fireEvent.keyDown(dialog, { key: "Escape" });
    fireEvent.click(screen.getByRole("button", { name: "Remove John Roe's paging number" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Remove" }));
    expect(delMutate).toHaveBeenCalledWith("u-a2", expect.anything());
  });

  it("acts on a readiness phone gap for a person the reader may edit, and keeps the hint otherwise", () => {
    const base = pagingChain();
    chain.data = {
      ...base,
      members: [...base.members, pagingMember({ membershipId: "a4", name: "Sam Lee", canEditPhone: true, hasProfilePhone: false })],
    };
    readiness.data!.chains[0] = readinessChain({
      chain: "CRE",
      label: "CRE",
      ready: false,
      gaps: [
        readinessGap({ code: "NO_PHONE", fix: "profile", userId: "u-a4", message: "Sam Lee (L1) has no number" }),
        readinessGap({ code: "NO_PHONE", fix: "profile", userId: "u-a3", message: "Ann Poe (L1) has no number" }),
      ],
    });
    render(<CasePagingTab onOpenRota={vi.fn()} />);
    const strip = screen.getByRole("list", { name: "CRE gaps" });
    const [sam, ann] = within(strip).getAllByRole("listitem");
    fireEvent.click(within(sam).getByRole("button", { name: "Add number" }));
    expect(screen.getByRole("dialog", { name: "Add paging number for Sam Lee" })).toBeInTheDocument();
    expect(ann).toHaveTextContent("Ask them to add it under avatar › Profile");
  });
});
