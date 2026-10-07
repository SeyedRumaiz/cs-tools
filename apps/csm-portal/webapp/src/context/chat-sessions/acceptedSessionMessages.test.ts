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
import { acceptedSessionMessages } from "./acceptedSessionMessages";

describe("acceptedSessionMessages", () => {
  it("puts the opening message after the AI-assistant conversation", () => {
    const messages = acceptedSessionMessages(
      "case-1",
      [
        { role: "customer", content: "How do I set up RAG?" },
        { role: "assistant", content: "Open Setup RAG Ingestion." },
      ],
      "It still fails",
    );
    expect(messages.map((m) => [m.from, m.text])).toEqual([
      ["customer", "How do I set up RAG?"],
      ["assistant", "Open Setup RAG Ingestion."],
      ["customer", "It still fails"],
    ]);
    expect(new Set(messages.map((m) => m.id)).size).toBe(3);
  });

  it("keeps the opening message when there was no AI conversation", () => {
    expect(acceptedSessionMessages("case-1", undefined, "a")).toEqual([
      { id: "opening-case-1", from: "customer", text: "a" },
    ]);
  });

  it("adds nothing when there is no opening message", () => {
    expect(acceptedSessionMessages("case-1", [], undefined)).toEqual([]);
    expect(acceptedSessionMessages("case-1", [], "")).toEqual([]);
  });
});
