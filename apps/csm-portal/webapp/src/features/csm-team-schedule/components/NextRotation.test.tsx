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

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import NextRotation from "./NextRotation";
import { EVENING, REGULAR, TZ, assignment, shiftMap } from "../test/fixtures";

const SHIFTS = shiftMap(REGULAR, EVENING);
/** Wednesday 23 September 2026, 08:00 UTC. */
const NOW = new Date("2026-09-23T08:00:00.000Z");

function renderNext(mine: React.ComponentProps<typeof NextRotation>["mine"]) {
  return render(
    <NextRotation mine={mine} shifts={SHIFTS} tz={TZ} horizonDays={56} isLoading={false} />,
  );
}

describe("NextRotation", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => vi.useRealTimers());

  it("says plainly when nothing is rostered, and over what horizon", () => {
    // Not "you are never on again" -- only that the query looked this far.
    renderNext([]);
    expect(screen.getByText(/Nothing rostered in the next 8 weeks/)).toBeInTheDocument();
  });

  it("ignores regular hours, which everybody has every weekday", () => {
    renderNext([
      assignment({
        name: "Asela",
        rotaDate: "2026-09-24",
        shiftCode: REGULAR.code,
        startsAt: "2026-09-24T03:30:00.000Z",
        endsAt: "2026-09-24T12:30:00.000Z",
      }),
    ]);
    expect(screen.getByText(/Nothing rostered/)).toBeInTheDocument();
  });

  it("names the next real rotation", () => {
    renderNext([
      assignment({
        name: "Asela",
        rotaDate: "2026-09-25",
        shiftCode: EVENING.code,
        startsAt: "2026-09-25T12:30:00.000Z",
        endsAt: "2026-09-25T15:30:00.000Z",
      }),
    ]);
    expect(screen.getByText("Your next rotation")).toBeInTheDocument();
    expect(screen.getByText("Evening 6-9pm")).toBeInTheDocument();
  });

  it("counts a rotation already running as the one you are on", () => {
    // Compared against the end, not the start: a shift half way through is
    // the one you are on, not one you have finished.
    renderNext([
      assignment({
        name: "Asela",
        rotaDate: "2026-09-23",
        shiftCode: EVENING.code,
        startsAt: "2026-09-23T06:00:00.000Z",
        endsAt: "2026-09-23T10:00:00.000Z",
      }),
    ]);
    expect(screen.getByText("On now")).toBeInTheDocument();
  });

  it("passes over a rotation that has already finished", () => {
    renderNext([
      assignment({
        name: "Asela",
        rotaDate: "2026-09-22",
        shiftCode: EVENING.code,
        startsAt: "2026-09-22T06:00:00.000Z",
        endsAt: "2026-09-22T10:00:00.000Z",
      }),
      assignment({
        name: "Asela",
        rotaDate: "2026-09-28",
        shiftCode: EVENING.code,
        startsAt: "2026-09-28T12:30:00.000Z",
        endsAt: "2026-09-28T15:30:00.000Z",
      }),
    ]);
    expect(screen.getByText("Your next rotation")).toBeInTheDocument();
    expect(screen.queryByText("On now")).not.toBeInTheDocument();
  });

  it("says it is still looking rather than that there is nothing", () => {
    render(
      <NextRotation mine={[]} shifts={SHIFTS} tz={TZ} horizonDays={56} isLoading />,
    );
    expect(screen.getByText("Looking…")).toBeInTheDocument();
  });
});
