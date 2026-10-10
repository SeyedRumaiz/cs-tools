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

import type { JSX } from "react";
import type { PagingChainMember } from "../types";
import { phoneChipState } from "../utils/pagingPhone";

/** What a reader may do about a person's paging number; the tab owns it. */
export interface PagingPhoneControls {
  edit: (member: PagingChainMember) => void;
  remove: (member: PagingChainMember) => void;
  test: (member: PagingChainMember) => void;
  /** Whether a test call to this person is placed and its result awaited. */
  calling: (member: PagingChainMember) => boolean;
}

/** "Calling…" while a test call's result is awaited. */
export const CALLING_LABEL = "Calling… result in about a minute";

function PhoneIcon(): JSX.Element {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z" />
    </svg>
  );
}

/**
 * The number Case Paging would call for one person, and -- for a reader who
 * may -- the actions on their paging-only number. The words say the state
 * ("Profile ✓", "No number", ...); the colour only repeats them. Only the
 * masked number is ever shown.
 */
export default function PhoneChip({
  member,
  controls,
}: {
  member: PagingChainMember;
  controls?: PagingPhoneControls;
}): JSX.Element {
  const st = phoneChipState(member);
  const name = member.name || member.email;
  const can = Boolean(controls) && member.canEditPhone === true;
  const calling = can && controls ? controls.calling(member) : false;

  return (
    <span className="cp-phone">
      <span className={`cp-ph ${st.tone}`} aria-live="polite">
        <PhoneIcon />
        <span>
          {st.label}
          {st.masked ? <span className="cp-mask"> · {st.masked}</span> : null}
        </span>
      </span>
      {can && controls ? (
        <span className="cp-phact">
          {!member.pagingPhone && member.hasProfilePhone !== true ? (
            <button
              type="button"
              className="btn ghost sm"
              aria-label={`Add a paging number for ${name}`}
              onClick={() => controls.edit(member)}
            >
              Add number
            </button>
          ) : null}
          {member.pagingPhone ? (
            <>
              <button
                type="button"
                className="btn ghost sm"
                aria-label={`Change ${name}'s paging number`}
                onClick={() => controls.edit(member)}
              >
                Change
              </button>
              <button
                type="button"
                className="btn ghost sm"
                aria-label={`Remove ${name}'s paging number`}
                onClick={() => controls.remove(member)}
              >
                Remove
              </button>
              <button
                type="button"
                className="btn ghost sm"
                aria-label={calling ? `${CALLING_LABEL} (${name})` : `Test call ${name}'s paging number`}
                aria-disabled={calling || undefined}
                onClick={() => {
                  if (!calling) controls.test(member);
                }}
              >
                {calling ? CALLING_LABEL : "Test call"}
              </button>
            </>
          ) : null}
        </span>
      ) : null}
    </span>
  );
}
