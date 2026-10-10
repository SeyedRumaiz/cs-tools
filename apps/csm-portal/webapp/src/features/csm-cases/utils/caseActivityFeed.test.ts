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
import type { CaseAuditEntry, CsmCaseComment } from "@features/csm-cases/types/csmCases";
import {
  compareCommentsChronologically,
  compareFeedEntries,
  describeAuditEntry,
  type FeedEntry,
} from "./caseActivityFeed";

function commentEntry(
  id: string,
  at: string,
  role: CsmCaseComment["authorRole"],
): FeedEntry {
  return {
    kind: "comment",
    at,
    comment: {
      id,
      caseId: "c",
      authorName: role === "chatbot" ? "Novera" : "Someone",
      authorRole: role,
      bodyHtml: "",
      createdAt: at,
    },
  };
}

describe("compareFeedEntries", () => {
  it("orders by timestamp ascending", () => {
    const older = commentEntry("a", "2026-07-01T00:00:00Z", "customer");
    const newer = commentEntry("b", "2026-07-01T00:05:00Z", "chatbot");
    expect(compareFeedEntries(older, newer)).toBeLessThan(0);
    expect(compareFeedEntries(newer, older)).toBeGreaterThan(0);
  });

  it("puts the human question before the bot answer on a timestamp tie", () => {
    // Both imported at the same second — the real Novera Q&A case.
    const ts = "2026-07-01T00:51:54Z";
    const question = commentEntry("q", ts, "customer");
    const answer = commentEntry("a", ts, "chatbot");
    // answer sorts AFTER question regardless of input order
    expect(compareFeedEntries(question, answer)).toBeLessThan(0);
    expect(compareFeedEntries(answer, question)).toBeGreaterThan(0);
  });

  it("newest-first (negated) keeps the bot answer above the question", () => {
    const ts = "2026-07-01T00:51:54Z";
    const question = commentEntry("q", ts, "customer");
    const answer = commentEntry("a", ts, "chatbot");
    const feed = [question, answer].sort((x, y) => -compareFeedEntries(x, y));
    // Newest-first view: the later-in-thread bot answer shows above the question.
    expect(feed.map((e) => (e.kind === "comment" ? e.comment.id : ""))).toEqual([
      "a",
      "q",
    ]);
  });

  it("is deterministic for two non-bot entries at the same time (by id)", () => {
    const ts = "2026-07-01T00:51:54Z";
    const a = commentEntry("a", ts, "customer");
    const b = commentEntry("b", ts, "customer");
    expect(compareFeedEntries(a, b)).toBeLessThan(0);
    expect(compareFeedEntries(b, a)).toBeGreaterThan(0);
  });
});

describe("compareCommentsChronologically", () => {
  function comment(
    id: string,
    createdAt: string,
    role: CsmCaseComment["authorRole"],
  ): CsmCaseComment {
    return {
      id,
      caseId: "c",
      authorName: role === "chatbot" ? "Novera" : "Someone",
      authorRole: role,
      bodyHtml: "",
      createdAt,
    };
  }

  it("orders by timestamp ascending", () => {
    const older = comment("a", "2026-07-01T00:00:00Z", "customer");
    const newer = comment("b", "2026-07-01T00:05:00Z", "chatbot");
    expect(compareCommentsChronologically(older, newer)).toBeLessThan(0);
    expect(compareCommentsChronologically(newer, older)).toBeGreaterThan(0);
  });

  // Regression: entity-service returns conversation messages `created_on
  // DESC` with a random-UUID tie-break, so the standalone conversation
  // transcript pages (which consume this flat CsmCaseComment[] directly,
  // never through the case activity feed's FeedEntry merge) need their own
  // sort to guarantee the user's question renders before Novera's reply
  // when both share a whole-second timestamp.
  it("puts the human question before the bot answer on a timestamp tie", () => {
    const ts = "2026-07-01T00:51:54Z";
    const question = comment("q", ts, "customer");
    const answer = comment("a", ts, "chatbot");
    expect(compareCommentsChronologically(question, answer)).toBeLessThan(0);
    expect(compareCommentsChronologically(answer, question)).toBeGreaterThan(0);
  });

  it("is deterministic for two non-bot entries at the same time (by id)", () => {
    const ts = "2026-07-01T00:51:54Z";
    const a = comment("a", ts, "customer");
    const b = comment("b", ts, "customer");
    expect(compareCommentsChronologically(a, b)).toBeLessThan(0);
    expect(compareCommentsChronologically(b, a)).toBeGreaterThan(0);
  });
});

describe("describeAuditEntry", () => {
  function auditEntry(overrides: Partial<CaseAuditEntry> = {}): CaseAuditEntry {
    return {
      id: "audit-1",
      kind: "field_change",
      actor: "Jane Doe",
      createdAt: "2026-07-01T00:00:00Z",
      ...overrides,
    };
  }

  it("describes a single field change as 'Label: old → new', through the curated state label", () => {
    const entry = auditEntry({
      changes: [{ field: "state", fieldLabel: "State", previousValue: "open", newValue: "work_in_progress" }],
    });
    expect(describeAuditEntry(entry)).toBe("State: Open → Work in progress");
  });

  it("joins multiple field changes from the same transaction with a semicolon", () => {
    const entry = auditEntry({
      changes: [
        { field: "state", fieldLabel: "State", previousValue: "open", newValue: "work_in_progress" },
        { field: "assignee", fieldLabel: "Assignee", previousValue: undefined, newValue: "Jane Doe" },
      ],
    });
    expect(describeAuditEntry(entry)).toBe(
      "State: Open → Work in progress; Assignee: Jane Doe",
    );
  });

  it("humanizes a raw, all-caps state value the same as an already-lowercase one", () => {
    const entry = auditEntry({
      changes: [
        { field: "state", fieldLabel: "State", previousValue: "SOLUTION_PROPOSED", newValue: "CLOSED" },
      ],
    });
    expect(describeAuditEntry(entry)).toBe("State: Solution proposed → Closed");
  });

  it("maps a severity field change through the curated severity label, both for an already-human value and a raw P-notation one", () => {
    const entry = auditEntry({
      changes: [
        { field: "severity", fieldLabel: "Severity", previousValue: "Critical", newValue: "P2" },
      ],
    });
    expect(describeAuditEntry(entry)).toBe("Severity: Critical → High");
  });

  it("maps a bare S0-S4 severity code too, which severityFromBe alone does not recognize", () => {
    const entry = auditEntry({
      changes: [
        { field: "severity", fieldLabel: "Severity", previousValue: "S0", newValue: "s2" },
      ],
    });
    expect(describeAuditEntry(entry)).toBe("Severity: Catastrophic → High");
  });

  it("renders 'cleared' when a field's new value is absent", () => {
    const entry = auditEntry({
      changes: [{ field: "assignee", fieldLabel: "Assignee", previousValue: "Jane Doe", newValue: undefined }],
    });
    expect(describeAuditEntry(entry)).toBe("Assignee: Jane Doe → cleared");
  });

  it("falls back to the entry's own description when there are no structured changes", () => {
    const entry = auditEntry({ changes: undefined, description: "Case escalated to EL2" });
    expect(describeAuditEntry(entry)).toBe("Case escalated to EL2");
  });

  it("formats a timestamp-shaped field value in the user's locale rather than showing the raw ISO string", () => {
    const entry = auditEntry({
      changes: [
        {
          field: "slaBreach",
          fieldLabel: "SLA breach",
          previousValue: "2026-07-01T00:00:00Z",
          newValue: "2026-07-02T00:00:00Z",
        },
      ],
    });
    const described = describeAuditEntry(entry);
    expect(described).not.toContain("2026-07-01T00:00:00Z");
    expect(described.startsWith("SLA breach:")).toBe(true);
  });
});
