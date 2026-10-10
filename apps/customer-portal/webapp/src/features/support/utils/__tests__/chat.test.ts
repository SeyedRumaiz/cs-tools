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
  buildEscalationPriorMessages,
  displayTextFromConversationContent,
  getFinalMessageFromPayload,
  isTokenLimitNoticeText,
  sanitizeStreamToken,
  splitTokenForTyping,
  stripThinkingBlocks,
} from "@features/support/utils/chat";
import {
  ChatSender,
  type Message,
} from "@features/support/types/conversations";
import { NOVERA_WELCOME_MESSAGE_ID } from "@features/support/constants/chatConstants";

describe("splitTokenForTyping", () => {
  it("splits text into equal chunks up to max chars", () => {
    expect(splitTokenForTyping("abcdefgh", 3)).toEqual(["abc", "def", "gh"]);
  });

  it("guards against non-positive cap with minimum chunk size of one", () => {
    expect(splitTokenForTyping("abc", 0)).toEqual(["a", "b", "c"]);
  });
});

describe("sanitizeStreamToken", () => {
  it("removes description fragments, markdown bold, and excessive newlines", () => {
    const raw = '**{"description":"ignore me","message":"Hello"}\n\n\n\nnext**';
    expect(sanitizeStreamToken(raw)).toBe('{"message":"Hello"}\n\nnext');
  });
});

describe("stripThinkingBlocks", () => {
  it("removes a leading thinking block and keeps the answer", () => {
    const raw =
      "<thinking> The user asks about Widget 2.\n- Dev: Widget 1\nI should ask.\n</thinking>\n\nI don't see Widget 2 in your environments.";
    expect(stripThinkingBlocks(raw)).toBe(
      "I don't see Widget 2 in your environments.",
    );
  });

  it("removes every block, in any letter case", () => {
    expect(
      stripThinkingBlocks("A<thinking>x</thinking>B<Thinking>y</THINKING>C"),
    ).toBe("ABC");
  });

  it("hides a block that is still open while the answer streams", () => {
    expect(stripThinkingBlocks("<thinking>The user wants")).toBe("");
    expect(stripThinkingBlocks("Done.\n<thinking>more reasoning")).toBe(
      "Done.\n",
    );
  });

  it("hides a half-typed opening tag at the end of the streamed text", () => {
    for (const partial of ["<t", "<thin", "<thinking"]) {
      expect(stripThinkingBlocks(`Hello ${partial}`)).toBe("Hello ");
    }
  });

  it("shows only the answer, never reasoning, as a block is typed out", () => {
    const full = "<thinking>reason</thinking>Answer";
    // Starts at 2: a lone trailing "<" is deliberately kept because it can be
    // real text, so it shows for one character until the next token arrives.
    for (let i = 2; i <= full.length; i += 1) {
      const shown = stripThinkingBlocks(full.slice(0, i));
      expect(shown === "" || "Answer".startsWith(shown)).toBe(true);
    }
  });

  it("keeps the author's indentation, whichever side of a block it is on", () => {
    // A block removed later in the message must not eat leading whitespace.
    expect(stripThinkingBlocks("    code\n<thinking>reason</thinking>")).toBe(
      "    code\n",
    );
    // After a leading block only the gap goes; the first real line keeps its indent.
    expect(stripThinkingBlocks("<thinking>x</thinking>\n\n    code")).toBe(
      "    code",
    );
    expect(stripThinkingBlocks("<thinking>x</thinking>   Answer")).toBe(
      "Answer",
    );
  });

  it("stays linear however many openers there are", () => {
    const manyOpeners = "<thinking>x".repeat(50_000);
    const started = performance.now();
    expect(stripThinkingBlocks(manyOpeners)).toBe("");
    // A rescan per unclosed opener takes several seconds here; a linear scan well
    // under a millisecond. The bound is ~1000x the real cost so load can't flake it.
    expect(performance.now() - started).toBeLessThan(1000);
    expect(stripThinkingBlocks("keep <thinking>x</thinking>".repeat(2_000))).toBe(
      "keep ".repeat(2_000),
    );
  });

  it("returns text without a thinking tag untouched", () => {
    const plain = "Set `a < b` and use <b>bold</b> or <thead> markup.";
    expect(stripThinkingBlocks(plain)).toBe(plain);
    expect(stripThinkingBlocks("")).toBe("");
  });
});

describe("displayTextFromConversationContent", () => {
  it("returns parsed message for bot JSON payloads", () => {
    const raw = '{"message":"Final answer","description":"hidden"}';
    expect(displayTextFromConversationContent(raw, true)).toBe("Final answer");
  });

  it("returns raw text for invalid JSON payloads", () => {
    expect(displayTextFromConversationContent("{bad-json", true)).toBe("{bad-json");
  });
});

describe("isTokenLimitNoticeText", () => {
  it("matches a limit notice the customer can see", () => {
    expect(isTokenLimitNoticeText("You have hit the usage limit.")).toBe(true);
  });

  it("ignores a limit mentioned only inside hidden reasoning", () => {
    expect(
      isTokenLimitNoticeText(
        "<thinking>They may be hitting a rate limit.</thinking>Which gateway is this?",
      ),
    ).toBe(false);
    expect(isTokenLimitNoticeText("")).toBe(false);
  });
});

describe("getFinalMessageFromPayload", () => {
  it("supports nested message object", () => {
    expect(
      getFinalMessageFromPayload({
        message: { message: "Nested content" },
      }),
    ).toBe("Nested content");
  });
});

describe("buildEscalationPriorMessages", () => {
  const baseTimestamp = new Date("2026-01-01T00:00:00.000Z");

  function makeMessage(
    overrides: Partial<Message> & Pick<Message, "id" | "text" | "sender">,
  ): Message {
    return { timestamp: baseTimestamp, ...overrides };
  }

  it("includes both customer and assistant messages, in their existing chronological order", () => {
    const messages: Message[] = [
      makeMessage({
        id: NOVERA_WELCOME_MESSAGE_ID,
        text: "Hi! I'm Novera...",
        sender: ChatSender.BOT,
      }),
      makeMessage({ id: "m1", text: "my build is failing", sender: ChatSender.USER }),
      makeMessage({ id: "m2", text: "have you tried X?", sender: ChatSender.BOT }),
      makeMessage({ id: "m3", text: "yes, still failing", sender: ChatSender.USER }),
    ];

    expect(buildEscalationPriorMessages(messages)).toEqual([
      {
        role: "customer",
        content: "my build is failing",
        createdAt: baseTimestamp.toISOString(),
      },
      {
        role: "assistant",
        content: "have you tried X?",
        createdAt: baseTimestamp.toISOString(),
      },
      {
        role: "customer",
        content: "yes, still failing",
        createdAt: baseTimestamp.toISOString(),
      },
    ]);
  });

  it("excludes the welcome message and any UI-only loading/streaming/human-message artifact, but keeps an errored turn's final text", () => {
    const messages: Message[] = [
      makeMessage({
        id: NOVERA_WELCOME_MESSAGE_ID,
        text: "Hi! I'm Novera...",
        sender: ChatSender.BOT,
      }),
      makeMessage({ id: "m1", text: "real question", sender: ChatSender.USER }),
      makeMessage({
        id: "m2",
        text: "Novera is analyzing your request...",
        sender: ChatSender.BOT,
        isLoading: true,
      }),
      makeMessage({
        id: "m3",
        text: "Something went wrong",
        sender: ChatSender.BOT,
        isError: true,
      }),
      makeMessage({
        id: "m4",
        text: "partial ans",
        sender: ChatSender.BOT,
        isStreaming: true,
      }),
      makeMessage({
        id: "m5",
        text: "an engineer's reply",
        sender: ChatSender.BOT,
        isHumanMessage: true,
      }),
      makeMessage({ id: "m6", text: "real answer", sender: ChatSender.BOT }),
    ];

    expect(buildEscalationPriorMessages(messages)).toEqual([
      { role: "customer", content: "real question", createdAt: baseTimestamp.toISOString() },
      {
        role: "assistant",
        content: "Something went wrong",
        createdAt: baseTimestamp.toISOString(),
      },
      { role: "assistant", content: "real answer", createdAt: baseTimestamp.toISOString() },
    ]);
  });

  it("prefers createdOnRaw over the local timestamp when present", () => {
    const messages: Message[] = [
      makeMessage({
        id: "m1",
        text: "with raw timestamp",
        sender: ChatSender.USER,
        createdOnRaw: "2026-02-02T12:00:00Z",
      }),
    ];

    expect(buildEscalationPriorMessages(messages)).toEqual([
      { role: "customer", content: "with raw timestamp", createdAt: "2026-02-02T12:00:00Z" },
    ]);
  });

  it("returns an empty array for a conversation with nothing worth persisting", () => {
    const messages: Message[] = [
      makeMessage({
        id: NOVERA_WELCOME_MESSAGE_ID,
        text: "Hi! I'm Novera...",
        sender: ChatSender.BOT,
      }),
    ];

    expect(buildEscalationPriorMessages(messages)).toEqual([]);
  });
});
