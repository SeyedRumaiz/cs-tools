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
import {
  CURRENT_USER_PLACEHOLDER,
  hasCurrentUserPlaceholder,
  resolveCurrentUserPlaceholder,
} from "./currentUserFilterPlaceholder";

const CURRENT_USER_ID = "11111111-aaaa-bbbb-cccc-000000000001";

describe("resolveCurrentUserPlaceholder", () => {
  describe("case-search DSL shape ({ filters: [...] })", () => {
    it("substitutes the placeholder with the signed-in user's own id when one is available", () => {
      const filters = {
        filters: [
          { field: "state", op: "in", values: ["open"] },
          { field: "assignedUserId", op: "in", values: [CURRENT_USER_PLACEHOLDER] },
        ],
      };

      const resolved = resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID);

      expect(resolved).toEqual({
        filters: [
          { field: "state", op: "in", values: ["open"] },
          { field: "assignedUserId", op: "in", values: [CURRENT_USER_ID] },
        ],
      });
    });

    // Fails CLOSED: dropping the assignedUserId entry here would leave a
    // widget filtered only by state, i.e. every engineer's cases painted into
    // a tile labelled as the viewer's own. The caller is expected to notice
    // via hasCurrentUserPlaceholder and hold the request instead.
    it("leaves the filters untouched when no signed-in user id is available yet", () => {
      const filters = {
        filters: [
          { field: "state", op: "in", values: ["open"] },
          { field: "assignedUserId", op: "in", values: [CURRENT_USER_PLACEHOLDER] },
        ],
      };

      expect(resolveCurrentUserPlaceholder(filters, undefined)).toBe(filters);
    });

    it("keeps the literal values of a mixed filter when no signed-in user id is available yet", () => {
      const filters = {
        filters: [
          {
            field: "assignedUserId",
            op: "in",
            values: ["some-literal-user-id", CURRENT_USER_PLACEHOLDER],
          },
        ],
      };

      const resolved = resolveCurrentUserPlaceholder(filters, undefined) as {
        filters: { values: string[] }[];
      };

      expect(resolved.filters[0].values).toContain("some-literal-user-id");
    });

    it("only substitutes the placeholder entry within a values array, leaving other literal values alone", () => {
      const filters = {
        filters: [
          {
            field: "assignedUserId",
            op: "in",
            values: ["some-literal-user-id", CURRENT_USER_PLACEHOLDER],
          },
        ],
      };

      const resolved = resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID);

      expect(resolved).toEqual({
        filters: [
          {
            field: "assignedUserId",
            op: "in",
            values: ["some-literal-user-id", CURRENT_USER_ID],
          },
        ],
      });
    });

    it("returns filters unchanged when there's no entry carrying the placeholder at all", () => {
      const filters = { filters: [{ field: "state", op: "in", values: ["open"] }] };

      expect(resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID)).toEqual(filters);
      expect(resolveCurrentUserPlaceholder(filters, undefined)).toEqual(filters);
    });

    it("passes through an empty filters array unchanged", () => {
      const filters = { filters: [] };

      expect(resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID)).toBe(filters);
    });
  });

  describe("flat { fieldName: string[] | string } shape", () => {
    it("substitutes the placeholder inside a flat string-array field", () => {
      const filters = { assignedUserIds: [CURRENT_USER_PLACEHOLDER], states: ["open"] };

      const resolved = resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID);

      expect(resolved).toEqual({ assignedUserIds: [CURRENT_USER_ID], states: ["open"] });
    });

    it("leaves a flat string-array field untouched when unresolved, rather than dropping the key", () => {
      const filters = { assignedUserIds: [CURRENT_USER_PLACEHOLDER], states: ["open"] };

      expect(resolveCurrentUserPlaceholder(filters, undefined)).toBe(filters);
    });

    it("keeps the literal values of a mixed flat array when unresolved", () => {
      const filters = { assignedUserIds: ["some-literal-user-id", CURRENT_USER_PLACEHOLDER] };

      expect(resolveCurrentUserPlaceholder(filters, undefined)).toBe(filters);
    });

    it("substitutes a bare placeholder string value directly (not nested in an array)", () => {
      const filters = { createdBy: CURRENT_USER_PLACEHOLDER };

      expect(resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID)).toEqual({
        createdBy: CURRENT_USER_ID,
      });
    });

    it("keeps a bare placeholder string value when unresolved, rather than dropping the key", () => {
      const filters = { createdBy: CURRENT_USER_PLACEHOLDER, states: ["open"] };

      expect(resolveCurrentUserPlaceholder(filters, undefined)).toBe(filters);
    });

    it("returns filters unchanged (same reference) when no field carries the placeholder", () => {
      const filters = { states: ["open"], severities: ["critical"] };

      expect(resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID)).toBe(filters);
    });
  });
});

describe("mixed arrays (field predicates alongside another entry shape)", () => {
  const mixed = {
    filters: [
      { field: "assignedUserId", op: "in", values: [CURRENT_USER_PLACEHOLDER] },
      { preset: "activeCaseStates" },
    ],
  };

  it("substitutes the placeholder in the predicates and leaves the other entries untouched", () => {
    expect(resolveCurrentUserPlaceholder(mixed, CURRENT_USER_ID)).toEqual({
      filters: [
        { field: "assignedUserId", op: "in", values: [CURRENT_USER_ID] },
        { preset: "activeCaseStates" },
      ],
    });
  });

  it("still fails closed when no user id is available", () => {
    expect(resolveCurrentUserPlaceholder(mixed, undefined)).toBe(mixed);
  });

  it("is detected by hasCurrentUserPlaceholder until substituted", () => {
    expect(hasCurrentUserPlaceholder(mixed)).toBe(true);
    expect(hasCurrentUserPlaceholder(resolveCurrentUserPlaceholder(mixed, CURRENT_USER_ID))).toBe(
      false,
    );
  });

  it("does not report a placeholder when only the non-predicate entries are present", () => {
    expect(hasCurrentUserPlaceholder({ filters: [{ preset: "activeCaseStates" }] })).toBe(false);
  });
});

describe("hasCurrentUserPlaceholder", () => {
  it("detects the placeholder in the case-search DSL shape", () => {
    expect(
      hasCurrentUserPlaceholder({
        filters: [
          { field: "state", op: "in", values: ["open"] },
          { field: "assignedUserId", op: "in", values: [CURRENT_USER_PLACEHOLDER] },
        ],
      }),
    ).toBe(true);
  });

  it("detects the placeholder in a flat array field and in a bare string field", () => {
    expect(hasCurrentUserPlaceholder({ assignedUserIds: [CURRENT_USER_PLACEHOLDER] })).toBe(true);
    expect(hasCurrentUserPlaceholder({ createdBy: CURRENT_USER_PLACEHOLDER })).toBe(true);
  });

  it("is false once the placeholder has been substituted", () => {
    const filters = {
      filters: [{ field: "assignedUserId", op: "in", values: [CURRENT_USER_PLACEHOLDER] }],
    };

    expect(hasCurrentUserPlaceholder(resolveCurrentUserPlaceholder(filters, CURRENT_USER_ID))).toBe(
      false,
    );
  });

  it("is false for filters that never carried the placeholder", () => {
    expect(hasCurrentUserPlaceholder({ states: ["open"] })).toBe(false);
    expect(
      hasCurrentUserPlaceholder({ filters: [{ field: "state", op: "in", values: ["open"] }] }),
    ).toBe(false);
  });
});
