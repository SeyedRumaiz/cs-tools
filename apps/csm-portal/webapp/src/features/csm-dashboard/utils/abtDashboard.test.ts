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

import { describe, expect, it } from "vitest";
import { humanizeState, stateLabel } from "./abtDashboard";

describe("humanizeState", () => {
  it("title-cases a lowercase snake_case key", () => {
    expect(humanizeState("pending_review")).toBe("Pending review");
  });

  it("title-cases an all-caps snake_case key the same way, not shouting it back", () => {
    expect(humanizeState("PENDING_REVIEW")).toBe("Pending review");
  });

  it("falls back to 'Unknown' for an empty value", () => {
    expect(humanizeState("")).toBe("Unknown");
  });
});

describe("stateLabel", () => {
  it("uses the curated label for a known lowercase state", () => {
    expect(stateLabel("solution_proposed")).toBe("Solution proposed");
  });

  it("resolves a raw, all-caps state value to the same curated label — the\n" +
    "exact shape a sync-written audit entry sent ('SOLUTION_PROPOSED' /\n" +
    "'CLOSED' instead of the app's own 'solution_proposed' / 'closed')", () => {
    expect(stateLabel("SOLUTION_PROPOSED")).toBe("Solution proposed");
    expect(stateLabel("CLOSED")).toBe("Closed");
  });

  it("resolves an already-humanized value (spaces, sentence case) to the same curated label", () => {
    expect(stateLabel("Solution proposed")).toBe("Solution proposed");
  });

  it("degrades to a humanized key for an unrecognized state", () => {
    expect(stateLabel("PENDING_CUSTOMER_REVIEW")).toBe("Pending customer review");
  });
});
