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
import type { BeUser } from "@api/backend/types";
import { userLabel } from "@features/csm-operations/utils/incidentFormOptions";

const ID = "00000000-0000-0000-0000-000000000001";

function user(overrides: Partial<BeUser>): BeUser {
  return { id: ID, ...overrides } as BeUser;
}

describe("userLabel", () => {
  it("prefers the full name", () => {
    expect(userLabel(user({ firstName: "Jane", lastName: "Doe", email: "jane.doe@example.com" }))).toBe("Jane Doe");
  });

  it("falls back to the email when there is no name", () => {
    expect(userLabel(user({ email: "jane.doe@example.com" }))).toBe("jane.doe@example.com");
  });

  it("falls back to the username when there is no name or email", () => {
    expect(userLabel(user({ userName: "jane.doe" }))).toBe("jane.doe");
  });

  it("never returns the record id", () => {
    expect(userLabel(user({}))).toBe("Unnamed user");
    expect(userLabel(user({}))).not.toContain(ID);
  });

  it("treats a name that is itself a UUID as missing", () => {
    expect(userLabel(user({ firstName: ID, email: "jane.doe@example.com" }))).toBe("jane.doe@example.com");
  });

  it("treats a username that is a UUID as missing", () => {
    expect(userLabel(user({ userName: ID }))).toBe("Unnamed user");
  });
});
