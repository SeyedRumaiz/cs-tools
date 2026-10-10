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

import { describe, expect, it } from "vitest";
import { pagingMember } from "../test/fixtures";
import { phoneChipState, testCallAwaited, validateE164 } from "./pagingPhone";

const phone = (over: Record<string, unknown> = {}) => ({
  masked: "+94•••••123",
  setBy: "lead@example.com",
  setAt: "2026-10-01T09:00:00Z",
  ...over,
});

describe("phoneChipState", () => {
  it("names each state in words", () => {
    const at = "2026-10-08T09:00:00Z";
    const cases: [Parameters<typeof pagingMember>[0], string, string][] = [
      [{ membershipId: "a", hasProfilePhone: true, pagingPhone: phone() }, "Profile ✓", "ok"],
      [{ membershipId: "b", hasProfilePhone: false, pagingPhone: phone({ lastTestStatus: "completed", lastTestAt: at }) },
        `Paging number ✓ tested ${new Date(at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}`, "ok"],
      [{ membershipId: "c", hasProfilePhone: false, pagingPhone: phone() }, "Paging number · not tested", "warn"],
      [{ membershipId: "d", hasProfilePhone: false, pagingPhone: phone({ lastTestStatus: "pending" }) }, "Paging number · not tested", "warn"],
      [{ membershipId: "e", hasProfilePhone: false, pagingPhone: phone({ lastTestStatus: "no-answer" }) }, "Paging number · test failed (no-answer)", "err"],
      [{ membershipId: "f", hasProfilePhone: false, pagingPhone: null }, "No number", "none"],
      // The 1st responder must have a number; the 2nd and 3rd are optional.
      [{ membershipId: "f1", hasProfilePhone: false, pagingPhone: null, responderRank: 1 }, "No number", "none"],
      [{ membershipId: "f2", hasProfilePhone: false, pagingPhone: null, responderRank: 2 }, "No number · optional", "unknown"],
      [{ membershipId: "f3", hasProfilePhone: false, pagingPhone: null, responderRank: 3 }, "No number · optional", "unknown"],
      [{ membershipId: "g", pagingPhone: null }, "Number not checked", "unknown"],
    ];
    for (const [over, label, tone] of cases) {
      const st = phoneChipState(pagingMember(over));
      expect(st.label).toBe(label);
      expect(st.tone).toBe(tone);
    }
  });

  it("shows only the masked paging number, and only where it is the one used", () => {
    expect(phoneChipState(pagingMember({ membershipId: "a", hasProfilePhone: false, pagingPhone: phone({ phone: "+94771234123" }) })).masked).toBe("+94•••••123");
    expect(phoneChipState(pagingMember({ membershipId: "b", hasProfilePhone: true, pagingPhone: phone() })).masked).toBeUndefined();
  });
});

describe("validateE164", () => {
  it("accepts an E.164 number, ignoring spaces and dashes", () => {
    expect(validateE164("+94 77-123 4567")).toEqual({ value: "+94771234567" });
    expect(validateE164("+1234567")).toEqual({ value: "+1234567" });
  });

  it("says what is wrong", () => {
    expect(validateE164("").error).toMatch(/Enter a mobile number/);
    expect(validateE164("0771234567").error).toMatch(/Start with \+ and the country code/);
    expect(validateE164("+94 77a").error).toMatch(/only digits/);
    expect(validateE164("+0771234567").error).toMatch(/cannot start with 0/);
    expect(validateE164("+123456").error).toMatch(/7 to 15 digits/);
    expect(validateE164("+1234567890123456").error).toMatch(/7 to 15 digits/);
  });
});

describe("testCallAwaited", () => {
  const started = Date.parse("2026-10-08T09:00:00Z");
  const m = pagingMember({ membershipId: "a", pagingPhone: phone({ lastTestStatus: "pending", lastTestAt: "2026-10-08T09:00:00Z" }) });
  it("is awaited while pending within three minutes, then given up on", () => {
    expect(testCallAwaited(m, started + 60_000)).toBe(true);
    expect(testCallAwaited(m, started + 3 * 60_000)).toBe(false);
    expect(testCallAwaited(pagingMember({ membershipId: "b", pagingPhone: phone({ lastTestStatus: "completed" }) }), started)).toBe(false);
  });
});
