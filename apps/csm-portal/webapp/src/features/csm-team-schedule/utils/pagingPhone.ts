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

import type { PagingChainMember } from "../types";

/** How often the chain is re-read while a test call's result is awaited. */
export const PAGING_TEST_POLL_MS = 10_000;
/** How long a test call is waited on before the page stops asking. */
export const PAGING_TEST_POLL_MAX_MS = 3 * 60_000;

/**
 * Case Paging calls a person on their profile's mobile number first, and on
 * a paging-only number -- kept by entity-service, never written to the
 * profile -- only when the profile has none.
 */

export type PhoneChipTone = "ok" | "warn" | "err" | "none" | "unknown";

export interface PhoneChipState {
  label: string;
  tone: PhoneChipTone;
  /** The paging number as the page may show it, where it is the one used. */
  masked?: string;
}

/** "8 Oct" for an RFC 3339 instant. */
function fmtShortDate(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

/** Whether a missing number is only a nice-to-have for this person: true for
 *  the 2nd and 3rd responders (called with the 1st). The 1st responder and
 *  every tier above them (Team leads, the America Team lead, the heads) must
 *  have one. Mirrors the readiness strip's rule. */
export function phoneIsOptional(m: PagingChainMember): boolean {
  return m.responderRank === 2 || m.responderRank === 3;
}

/** What the chip beside a person says about the number paging would call.
 *  The words carry the state; the tone only repeats it. */
export function phoneChipState(m: PagingChainMember): PhoneChipState {
  if (m.hasProfilePhone === true) return { label: "Profile ✓", tone: "ok" };
  const p = m.pagingPhone;
  if (p) {
    const masked = p.masked;
    switch (p.lastTestStatus) {
      case "completed": {
        const on = fmtShortDate(p.lastTestAt);
        return { label: `Paging number ✓ tested${on ? ` ${on}` : ""}`, tone: "ok", masked };
      }
      case "no-answer":
      case "busy":
      case "failed":
        return { label: `Paging number · test failed (${p.lastTestStatus})`, tone: "err", masked };
      default:
        return { label: "Paging number · not tested", tone: "warn", masked };
    }
  }
  if (m.hasProfilePhone === false) {
    // The 2nd and 3rd responders are called together with the 1st, so a
    // missing number there does not leave Tier 1 without anyone to reach.
    return phoneIsOptional(m)
      ? { label: "No number · optional", tone: "unknown" }
      : { label: "No number", tone: "none" };
  }
  // The profile could not be checked just now, and there is no paging number.
  return { label: "Number not checked", tone: "unknown" };
}

/** Whether a test call to this person's paging number is still awaited, as of
 *  the chain read at `readAt` (ms). A call older than the polling window is
 *  given up on, so it can be placed again. */
export function testCallAwaited(m: PagingChainMember, readAt: number): boolean {
  const p = m.pagingPhone;
  if (p?.lastTestStatus !== "pending") return false;
  const started = p.lastTestAt ? Date.parse(p.lastTestAt) : NaN;
  return Number.isNaN(started) || readAt - started < PAGING_TEST_POLL_MAX_MS;
}

/** A typed number checked as E.164. Spaces, dashes, dots and brackets are
 *  ignored; `value` is the number to send when it is valid. */
export function validateE164(raw: string): { value?: string; error?: string } {
  const compact = raw.replace(/[\s\-.()]/g, "");
  if (!compact) return { error: "Enter a mobile number." };
  if (!compact.startsWith("+")) return { error: "Start with + and the country code, e.g. +94771234567." };
  const digits = compact.slice(1);
  if (!/^\d+$/.test(digits)) return { error: "Use only digits after the +." };
  if (digits.startsWith("0")) return { error: "The country code cannot start with 0." };
  if (digits.length < 7 || digits.length > 15) return { error: "Use 7 to 15 digits after the +." };
  return { value: compact };
}
