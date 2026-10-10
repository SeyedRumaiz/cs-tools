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

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";

const postMock = vi.fn();
vi.mock("@api/backend/client", () => ({ useBackendApi: () => ({ post: postMock }) }));
vi.mock("@config/apiConfig", () => ({ apiConfig: { backendUrl: "https://example.test" } }));

import GenerateRotaDialog, { type GenerateRotaTeam } from "./GenerateRotaDialog";
import { monthKey } from "../utils/rotaMonth";
import type { GenerateRotaResult } from "../api/useGenerateRota";

const TODAY = new Date(2026, 9, 9); // 9 October 2026

function result(over: Partial<GenerateRotaResult> = {}): GenerateRotaResult {
  return {
    rotaCode: "SRE_SAAS",
    month: "2026-11",
    from: "2026-11-01",
    dryRun: true,
    alreadyGenerated: false,
    summary: { planned: 120, keptByHand: 4, written: 0, replaced: 0, lieuPlanned: 6, lieuWritten: 0 },
    turns: [],
    lieuLeave: [],
    warnings: [],
    ...over,
  };
}

const TEAMS: GenerateRotaTeam[] = [
  {
    key: "apollo",
    name: "Apollo",
    members: [
      { userId: "u-ann", name: "Ann Perera", email: "ann@example.com" },
      { userId: "u-ben", name: "Ben Silva", email: "ben@example.com" },
      { userId: "u-cal", name: "Cal Fernando", email: "cal@example.com" },
    ],
  },
  { key: "artemis", name: "Artemis", members: [{ userId: "u-dee", name: "Dee Mendis", email: "dee@example.com" }] },
];

function renderDialog(onClose = vi.fn(), teams?: GenerateRotaTeam[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <GenerateRotaDialog open onClose={onClose} today={TODAY} teams={teams} />
    </QueryClientProvider>,
  );
  return { onClose };
}

describe("monthKey", () => {
  it("rolls over the year", () => {
    expect(monthKey(new Date(2026, 11, 15), 1)).toBe("2027-01");
    expect(monthKey(TODAY, 0)).toBe("2026-10");
  });
});

describe("GenerateRotaDialog", () => {
  beforeEach(() => {
    postMock.mockReset();
    window.localStorage.clear();
  });

  it("previews next month first and writes nothing until Generate", async () => {
    postMock.mockResolvedValueOnce(result());
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/turns and SUP from 2026-11-01/);
    expect(postMock).toHaveBeenCalledTimes(1);
    expect(postMock).toHaveBeenCalledWith("/team-schedule/rotas/SRE_SAAS/generate", { month: "2026-11", dryRun: true });
    expect(screen.getByText("116")).toBeInTheDocument();
    expect(screen.getByText(/4 left as set by hand/)).toBeInTheDocument();

    postMock.mockResolvedValueOnce(
      result({ dryRun: false, summary: { planned: 120, keptByHand: 4, written: 116, replaced: 0, lieuPlanned: 6, lieuWritten: 6 } }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await screen.findByText(/116 turns and SUP, 6 lieu leave/);
    expect(postMock).toHaveBeenLastCalledWith("/team-schedule/rotas/SRE_SAAS/generate", { month: "2026-11", regenerate: false });
    expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
  });

  it("asks to regenerate a month that has been generated before", async () => {
    postMock.mockResolvedValueOnce(result({ alreadyGenerated: true }));
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/already been generated/);
    postMock.mockResolvedValueOnce(result({ dryRun: false, alreadyGenerated: true }));
    fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
    await waitFor(() =>
      expect(postMock).toHaveBeenLastCalledWith("/team-schedule/rotas/SRE_SAAS/generate", { month: "2026-11", regenerate: true }),
    );
  });

  it("lists the slots the generator could not fill", async () => {
    const warnings = Array.from({ length: 10 }, (_, i) => ({ date: `2026-11-${String(i + 2).padStart(2, "0")}`, message: "TZ2 L1 has nobody" }));
    postMock.mockResolvedValueOnce(result({ warnings }));
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText("10 slots to look at");
    expect(screen.getByText("and 2 more")).toBeInTheDocument();
  });

  it("shows the server's reason when it refuses", async () => {
    postMock.mockRejectedValueOnce(new Error("only a lead of Apollo or Artemis may generate the SaaS rota"));
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText("only a lead of Apollo or Artemis may generate the SaaS rota");
  });

  it("sends the TZ3 people chosen for a team with the preview and the write", async () => {
    postMock.mockResolvedValue(result());
    renderDialog(vi.fn(), TEAMS);
    const apollo = screen.getByLabelText("Apollo TZ3");
    for (const name of ["Ann Perera", "Ben Silva", "Cal Fernando"]) {
      fireEvent.change(apollo, { target: { value: name.split(" ")[0] } });
      fireEvent.click(await screen.findByRole("option", { name }));
    }
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/turns and SUP from/);
    const crew = { apollo: ["u-ann", "u-ben", "u-cal"] };
    expect(postMock).toHaveBeenLastCalledWith("/team-schedule/rotas/SRE_SAAS/generate", {
      month: "2026-11",
      dryRun: true,
      nightCrew: crew,
    });
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() =>
      expect(postMock).toHaveBeenLastCalledWith("/team-schedule/rotas/SRE_SAAS/generate", {
        month: "2026-11",
        regenerate: false,
        nightCrew: crew,
      }),
    );
  });

  it("sends no TZ3 people when nobody is chosen, and remembers a choice for next time", async () => {
    postMock.mockResolvedValueOnce(result());
    renderDialog(vi.fn(), TEAMS);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/turns and SUP from/);
    // Undefined, so it is left out of the JSON body.
    expect(JSON.parse(JSON.stringify(postMock.mock.calls[0][1]))).toEqual({ month: "2026-11", dryRun: true });

    fireEvent.change(screen.getByLabelText("Artemis TZ3"), { target: { value: "Dee" } });
    fireEvent.click(await screen.findByRole("option", { name: "Dee Mendis" }));
    // Choosing people starts the preview again.
    expect(screen.getByRole("button", { name: "Preview" })).toBeInTheDocument();
    expect(JSON.parse(window.localStorage.getItem("csm.teamSchedule.generate.tz3Crew") ?? "{}")).toEqual({
      artemis: ["u-dee"],
    });
  });

  it("stays open while the month is being written", async () => {
    postMock.mockResolvedValueOnce(result());
    const { onClose } = renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/turns and SUP from/);

    let finish: (r: GenerateRotaResult) => void = () => {};
    postMock.mockReturnValueOnce(new Promise<GenerateRotaResult>((res) => (finish = res)));
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await screen.findByText(/Writing the month/);
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();

    finish(result({ dryRun: false, summary: { planned: 120, keptByHand: 4, written: 116, replaced: 0, lieuPlanned: 6, lieuWritten: 6 } }));
    fireEvent.click(await screen.findByRole("button", { name: "Done" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows who would work each zone, and the lieu leave, before writing", async () => {
    postMock.mockResolvedValueOnce(
      result({
        turns: [
          { userId: "u-ann", name: "Ann Perera", teamKey: "apollo", shiftCode: "SRE_TZ1_L1", tier: "L1", rotaDate: "2026-11-02" },
          { userId: "u-ben", name: "Ben Silva", teamKey: "apollo", shiftCode: "SRE_TZ1", tier: "L2", rotaDate: "2026-11-02" },
          { userId: "u-cal", name: "Cal Fernando", teamKey: "apollo", shiftCode: "SRE_TZ3", tier: "L3", rotaDate: "2026-11-02" },
          { userId: "u-ann", name: "Ann Perera", teamKey: "apollo", shiftCode: "SRE_TZ1_REGULAR", tier: "", rotaDate: "2026-11-02" },
        ],
        lieuLeave: [{ userId: "u-ben", name: "Ben Silva", teamKey: "apollo", startsOn: "2026-11-09", endsOn: "2026-11-10" }],
      }),
    );
    renderDialog(vi.fn(), TEAMS);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    fireEvent.click(await screen.findByRole("button", { name: "Show who works when" }));
    const table = screen.getByRole("table", { name: "Who works when" });
    expect(table).toHaveTextContent("Apollo");
    expect(table).toHaveTextContent("L1 Ann Perera · L2 Ben Silva");
    expect(table).toHaveTextContent("L3 Cal Fernando");
    expect(screen.getByText(/Ben Silva \(Apollo\):/)).toBeInTheDocument();
  });

  it("starts again when another month is picked", async () => {
    postMock.mockResolvedValueOnce(result());
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/turns and SUP from/);
    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: /December 2026/ }));
    expect(screen.queryByText(/turns and SUP from/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview" })).toBeInTheDocument();
  });
});
