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
import { replaceUuids } from "./redactIds";

const ID = "00000000-0000-0000-0000-000000000001";

describe("replaceUuids", () => {
  it("turns a leading '<noun> <id>' into 'That <noun>' and keeps the actionable rest", () => {
    expect(replaceUuids(`user ${ID} is not a contact on this case's project`, "user")).toBe(
      "That user is not a contact on this case's project",
    );
  });

  it("replaces a bare id mid-sentence with 'that <noun>'", () => {
    expect(replaceUuids(`Cannot add ${ID} to the watch list`, "user")).toBe(
      "Cannot add that user to the watch list",
    );
  });

  it("replaces every id in the message, case-insensitively", () => {
    const upper = ID.replace(/1$/, "A").toUpperCase();
    expect(replaceUuids(`user ${ID} and ${upper} are not contacts`, "user")).toBe(
      "That user and that user are not contacts",
    );
  });

  it("leaves a message with no id completely untouched, including its first character", () => {
    expect(replaceUuids("watchList contains invalid UUID: \"not-a-uuid\"", "user")).toBe(
      "watchList contains invalid UUID: \"not-a-uuid\"",
    );
  });

  it("returns an empty string unchanged", () => {
    expect(replaceUuids("", "user")).toBe("");
  });
});
