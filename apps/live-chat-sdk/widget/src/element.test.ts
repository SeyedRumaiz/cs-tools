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

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LiveChatError, type LiveChatAssistantEvent, type LiveChatEvent } from "@wso2/live-chat-client";
import "./index.js";
import { LIVE_CHAT_EVENTS, type LiveChatEndedDetail, type LiveChatWidgetElement } from "./element.js";

function fakeClient() {
  let onEvent: ((e: LiveChatEvent) => void) | null = null;
  let onAssistant: ((e: LiveChatAssistantEvent) => void) | null = null;
  const unsubscribe = vi.fn();
  const client = {
    startChat: vi.fn().mockResolvedValue({ caseId: "case-1", conversationId: "conv-1" }),
    subscribe: vi.fn((_caseId: string, cb: (e: LiveChatEvent) => void) => {
      onEvent = cb;
      return unsubscribe;
    }),
    sendMessage: vi.fn().mockResolvedValue(undefined),
    completeChat: vi.fn().mockResolvedValue(undefined),
    getHistory: vi.fn().mockResolvedValue([]),
    getCurrentChat: vi.fn().mockResolvedValue(null),
    isAssistantAvailable: vi.fn().mockResolvedValue(false),
    subscribeAssistant: vi.fn((_id: string, cb: (e: LiveChatAssistantEvent) => void) => {
      onAssistant = cb;
      return vi.fn();
    }),
    askAssistant: vi.fn().mockResolvedValue(undefined),
  };
  return {
    client,
    unsubscribe,
    push: (e: LiveChatEvent) => onEvent?.(e),
    answer: (e: LiveChatAssistantEvent) => onAssistant?.(e),
  };
}

const flush = () => new Promise((r) => setTimeout(r, 0));
const settle = async () => {
  for (let i = 0; i < 6; i++) await flush();
};

function mount(
  config: object = { bridgeUrl: "https://bridge.example", tenant: "my-product" },
  setup: (fake: ReturnType<typeof fakeClient>) => void = () => {},
) {
  const el = document.createElement("wso2-live-chat") as LiveChatWidgetElement;
  const fake = fakeClient();
  setup(fake);
  el.createClient = vi.fn(() => fake.client as never);
  el.getAccessToken = () => "token";
  el.config = config;
  document.body.append(el);
  const q = <T extends HTMLElement = HTMLElement>(sel: string) => el.shadowRoot!.querySelector(sel) as T;
  const type = async (text: string) => {
    const input = q<HTMLTextAreaElement>(".input");
    input.value = text;
    input.dispatchEvent(new Event("input"));
    q<HTMLButtonElement>(".send").click();
    await flush();
  };
  return { el, fake, q, type };
}

describe("<wso2-live-chat>", () => {
  beforeEach(() => {
    document.body.replaceChildren();
    sessionStorage.clear();
  });
  afterEach(() => vi.restoreAllMocks());

  it("shows a default launcher and applies config values as CSS variables", () => {
    const { el, q } = mount({
      bridgeUrl: "https://b",
      tenant: "t",
      layout: { position: "bottom-left" },
      theme: { primaryColor: "rgb(1, 2, 3)" },
      texts: { launcherLabel: "Need help?" },
    });
    expect(q(".launcher").hidden).toBe(false);
    expect(q(".launcher span").textContent).toBe("Need help?");
    expect(el.dataset.position).toBe("bottom-left");
    expect(el.style.getPropertyValue("--lc-primary")).toBe("rgb(1, 2, 3)");
    expect(q(".panel").hidden).toBe(true);
  });

  it("hides the launcher when the product opens the chat itself", () => {
    const { el, q } = mount({ bridgeUrl: "https://b", tenant: "t", layout: { showLauncher: false } });
    expect(q(".launcher").hidden).toBe(true);
    el.open();
    expect(q(".panel").hidden).toBe(false);
  });

  it("reads JSON from the config attribute and reports invalid configs", () => {
    const el = document.createElement("wso2-live-chat") as LiveChatWidgetElement;
    vi.spyOn(console, "error").mockImplementation(() => {});
    el.setAttribute("config", JSON.stringify({ bridgeUrl: "https://b", tenant: "t", texts: { title: "Hi" } }));
    document.body.append(el);
    expect(el.config?.texts.title).toBe("Hi");
    el.setAttribute("config", "{ not json");
    el.open();
    expect(el.shadowRoot!.querySelector(".error span")!.textContent).toContain("not valid JSON");
  });

  it("loads a JSON configuration file from config-src", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, json: async () => ({ bridgeUrl: "https://b", tenant: "from-file" }) }),
    );
    const el = document.createElement("wso2-live-chat") as LiveChatWidgetElement;
    el.setAttribute("config-src", "/live-chat.config.json");
    await flush();
    expect(el.config?.tenant).toBe("from-file");
    vi.unstubAllGlobals();
  });

  it("starts a chat, carries AI context, and follows engineer events", async () => {
    const { el, fake, q, type } = mount();
    const started = vi.fn();
    el.addEventListener(LIVE_CHAT_EVENTS.started, started);
    el.open({ priorMessages: [{ role: "assistant", content: "Try restarting." }, { role: "customer", content: " " }] });
    expect(q(".notice")).not.toBeNull();

    await type("It still fails");
    expect(fake.client.startChat).toHaveBeenCalledWith(
      expect.objectContaining({
        message: "It still fails",
        priorMessages: [{ role: "assistant", content: "Try restarting." }],
        conversationId: expect.stringMatching(/^live-chat-/),
      }),
    );
    expect(fake.client.subscribe).toHaveBeenCalledWith("case-1", expect.any(Function));
    expect(started).toHaveBeenCalledOnce();
    expect(el.state.phase).toBe("queued");

    fake.push({ type: "assigned", engineerEmail: "eng@example.com" });
    fake.push({ type: "assigned", engineerEmail: "eng@example.com" });
    expect(q(".status").textContent).toBe("Connected with eng@example.com");
    expect(el.shadowRoot!.querySelectorAll('.message[data-from="system"]')).toHaveLength(1);

    fake.push({ type: "message", content: "<img src=x onerror=alert(1)>", engineerEmail: "eng@example.com" });
    const engineerMessage = q('.message[data-from="engineer"]');
    expect(engineerMessage.textContent).toBe("<img src=x onerror=alert(1)>");
    expect(engineerMessage.querySelector("img")).toBeNull();

    await type("thanks");
    expect(fake.client.sendMessage).toHaveBeenCalledWith("case-1", { content: "thanks" });
  });

  it("keeps the AI context within the bridge's limits", async () => {
    const { el, fake, type } = mount();
    const turns = Array.from({ length: 25 }, (_, i) => ({ role: "customer" as const, content: `turn ${i}` }));
    const long = "é".repeat(3000); // 6,000 bytes
    el.open({ priorMessages: [...turns, { role: "assistant", content: long }] });
    await type("help");

    const sent = fake.client.startChat.mock.calls[0]![0].priorMessages as { content: string }[];
    expect(sent).toHaveLength(20);
    expect(sent[0]!.content).toBe("turn 6");
    const last = sent[19]!.content;
    expect(new TextEncoder().encode(last).length).toBeLessThanOrEqual(4000);
    expect(last.endsWith("…")).toBe(true);
    expect(last.startsWith("éé")).toBe(true);
  });

  it("drops the oldest AI turns once the total would exceed 32 KiB", async () => {
    const { el, fake, type } = mount();
    const turns = Array.from({ length: 10 }, (_, i) => ({ role: "assistant" as const, content: `${i}`.repeat(4000) }));
    el.open({ priorMessages: turns });
    await type("help");

    const sent = fake.client.startChat.mock.calls[0]![0].priorMessages as { content: string }[];
    expect(sent).toHaveLength(8);
    expect(sent[0]!.content[0]).toBe("2");
    expect(sent.reduce((n, m) => n + m.content.length, 0)).toBeLessThanOrEqual(32 * 1024);
  });

  it("shows the end immediately when the engineer ends the chat", async () => {
    const { el, fake, q, type } = mount();
    const ended = vi.fn();
    el.addEventListener(LIVE_CHAT_EVENTS.ended, ended);
    el.open();
    await type("help");
    fake.push({ type: "disconnected", engineerEmail: "eng@example.com" });

    expect(el.state.phase).toBe("ended");
    expect(q(".status").textContent).toBe("Chat ended");
    expect(q(".composer").hidden).toBe(true);
    expect(q(".new").hidden).toBe(false);
    expect(fake.unsubscribe).toHaveBeenCalled();
    const detail = (ended.mock.calls[0]![0] as CustomEvent<LiveChatEndedDetail>).detail;
    expect(detail.reason).toBe("engineer");
    expect(detail.transcript.at(-1)?.text).toBe("The engineer ended the chat.");
  });

  it("tells the customer when the engineer steps away and comes back", async () => {
    const { el, fake, type } = mount();
    el.open();
    await type("help");
    fake.push({ type: "assigned", engineerEmail: "eng@example.com" });
    fake.push({ type: "engineerStatus", status: "away" });
    fake.push({ type: "engineerStatus", status: "away" });
    fake.push({ type: "engineerStatus", status: "back" });
    fake.push({ type: "engineerStatus", status: "busy" });

    const notes = [...el.shadowRoot!.querySelectorAll('.message[data-from="system"]')].map((m) => m.textContent);
    expect(notes).toEqual([
      "eng@example.com joined the chat.",
      "The engineer seems to have stepped away. You can keep waiting or end the chat.",
      "The engineer is back.",
      "The engineer is still looking into this.",
    ]);
    expect(el.state.phase).toBe("connected");
  });

  it("explains a chat ended because the engineer was away too long", async () => {
    const { el, fake, q, type } = mount();
    const ended = vi.fn();
    el.addEventListener(LIVE_CHAT_EVENTS.ended, ended);
    el.open();
    await type("help");
    fake.push({ type: "assigned", engineerEmail: "eng@example.com" });
    fake.push({ type: "disconnected", engineerEmail: "", reason: "inactive" });

    expect(el.state.phase).toBe("ended");
    expect(q(".messages").textContent).toContain("The engineer was away for too long, so the chat was ended.");
    expect((ended.mock.calls[0]![0] as CustomEvent<LiveChatEndedDetail>).detail.reason).toBe("inactive");
  });

  it("reports a conversion to a case with its id", async () => {
    const { el, fake, type } = mount();
    const ended = vi.fn();
    el.addEventListener(LIVE_CHAT_EVENTS.ended, ended);
    el.open();
    await type("help");
    fake.push({ type: "converted", engineerEmail: "e", entityCaseId: "CS-42" });
    const detail = (ended.mock.calls[0]![0] as CustomEvent<LiveChatEndedDetail>).detail;
    expect(detail).toMatchObject({ reason: "converted", entityCaseId: "CS-42" });
  });

  it("ends the chat from the customer side: stop listening, then complete", async () => {
    const { el, fake, q, type } = mount();
    el.open();
    await type("help");
    q<HTMLButtonElement>(".end").click();
    await flush();
    expect(fake.unsubscribe).toHaveBeenCalled();
    expect(fake.client.completeChat).toHaveBeenCalledWith("case-1");
    expect(el.state.phase).toBe("ended");

    q<HTMLButtonElement>(".new").click();
    expect(el.state.phase).toBe("idle");
  });

  it("explains a 409 instead of a generic failure", async () => {
    const { el, fake, q, type } = mount();
    fake.client.startChat.mockRejectedValue(new LiveChatError("conflict", { status: 409 }));
    el.open();
    await type("help");
    expect(q(".error span").textContent).toContain("already have an open chat");
    expect(el.state.phase).toBe("idle");
  });

  it("offers a reconnect after the stream drops and reloads the transcript", async () => {
    const { el, fake, q, type } = mount();
    el.open();
    await type("help");
    fake.push({ type: "error", message: "stream ended" });
    expect(q(".reconnect").hidden).toBe(false);

    fake.client.getHistory.mockResolvedValue([
      { role: "customer", content: "help" },
      { role: "engineer", content: "Hello!" },
    ]);
    fake.client.getCurrentChat.mockResolvedValue({ caseId: "case-1", conversationId: "conv-1", status: "connected" });
    q<HTMLButtonElement>(".reconnect").click();
    await flush();
    expect(fake.client.getHistory).toHaveBeenCalledWith("case-1");
    expect(fake.client.subscribe).toHaveBeenCalledTimes(2);
    expect(el.state.phase).toBe("connected");
    expect(q('.message[data-from="engineer"]').textContent).toBe("Hello!");
  });

  it("says it is not configured when no token function is set", async () => {
    const { el, q, type } = mount();
    el.getAccessToken = null;
    el.open();
    await type("help");
    expect(q(".error span").textContent).toBe("Live chat is not configured.");
  });

  it("sends requests through the host's transport instead of a token", async () => {
    const transport = {
      requestTransport: { getJson: vi.fn(), postJson: vi.fn() },
      streamTransport: { openStream: vi.fn() },
    };
    const { el, fake, type } = mount();
    el.getAccessToken = null;
    el.transport = transport;
    await settle();
    expect(el.createClient).toHaveBeenLastCalledWith({
      baseUrl: "https://bridge.example",
      tenant: "my-product",
      requestTransport: transport.requestTransport,
      streamTransport: transport.streamTransport,
    });
    expect(fake.client.getCurrentChat).toHaveBeenCalled();

    el.open();
    await type("help");
    expect(fake.client.startChat).toHaveBeenCalled();
    expect(el.state.phase).toBe("queued");
  });

  describe("resuming an open chat", () => {
    const connected = { caseId: "case-9", conversationId: "conv-9", status: "connected" as const, engineerEmail: "eng@example.com" };
    const waiting = { caseId: "case-9", conversationId: "conv-9", status: "waiting" as const };

    it("restores a connected chat on load, without the AI conversation that preceded it", async () => {
      const { el, fake, q } = mount(undefined, (f) => {
        f.client.getCurrentChat.mockResolvedValue(connected);
        f.client.getHistory.mockResolvedValue([
          { role: "customer", content: "How do I set up RAG?" },
          { role: "assistant", content: "Open Setup RAG Ingestion." },
          { role: "customer", content: "It still fails" },
          { role: "engineer", content: "Let me check." },
        ]);
      });
      const resumed = vi.fn();
      el.addEventListener(LIVE_CHAT_EVENTS.resumed, resumed);
      await settle();

      expect(el.state).toMatchObject({ phase: "connected", caseId: "case-9", open: false });
      expect(fake.client.subscribe).toHaveBeenCalledWith("case-9", expect.any(Function));
      expect(fake.client.startChat).not.toHaveBeenCalled();
      expect(q(".status").textContent).toBe("Connected with eng@example.com");
      const texts = [...el.shadowRoot!.querySelectorAll(".message")].map((m) => m.textContent);
      expect(texts).toEqual(["It still fails", "Let me check.", "Your chat was restored."]);
      expect(resumed).toHaveBeenCalledOnce();
    });

    it("reopens the panel after a reload only if it was open", async () => {
      sessionStorage.setItem("wso2-live-chat:my-product:open", "1");
      const { el } = mount(undefined, (f) => f.client.getCurrentChat.mockResolvedValue(connected));
      await settle();
      expect(el.state.open).toBe(true);
    });

    it("picks up an accept that happened while the page was reloading", async () => {
      const { el, q } = mount(undefined, (f) => {
        f.client.getCurrentChat.mockResolvedValueOnce(waiting).mockResolvedValueOnce(connected);
      });
      await settle();
      expect(el.state.phase).toBe("connected");
      expect(q(".status").textContent).toBe("Connected with eng@example.com");
    });

    it("shows a restored chat as ended if it ended during the reload", async () => {
      const { el, q } = mount(undefined, (f) => {
        f.client.getCurrentChat.mockResolvedValueOnce(waiting).mockResolvedValueOnce(null);
      });
      const ended = vi.fn();
      el.addEventListener(LIVE_CHAT_EVENTS.ended, ended);
      await settle();
      expect(el.state.phase).toBe("ended");
      expect(q(".new").hidden).toBe(false);
      expect((ended.mock.calls[0]![0] as CustomEvent<LiveChatEndedDetail>).detail.reason).toBe("unknown");
    });

    it("says connected without a name when the engineer's email is unknown", async () => {
      const { q } = mount(undefined, (f) =>
        f.client.getCurrentChat.mockResolvedValue({ caseId: "c", conversationId: "c", status: "connected" }),
      );
      await settle();
      expect(q(".status").textContent).toBe("Connected with an engineer");
    });

    it("does not announce the engineer again when the bridge replays the accept", async () => {
      const { el, fake } = mount(undefined, (f) => {
        f.client.getCurrentChat.mockResolvedValue(connected);
        f.client.getHistory.mockResolvedValue([{ role: "engineer", content: "Hi" }]);
      });
      await settle();
      fake.push({ type: "assigned", engineerEmail: "eng@example.com" });
      const texts = [...el.shadowRoot!.querySelectorAll(".message")].map((m) => m.textContent);
      expect(texts).toEqual(["Hi", "Your chat was restored."]);
    });

    it("takes the engineer's name from a replayed accept when it was unknown", async () => {
      const { el, fake, q } = mount(undefined, (f) =>
        f.client.getCurrentChat.mockResolvedValue({ caseId: "c", conversationId: "c", status: "connected" }),
      );
      await settle();
      fake.push({ type: "assigned", engineerEmail: "eng@example.com" });
      expect(q(".status").textContent).toBe("Connected with eng@example.com");
      const texts = [...el.shadowRoot!.querySelectorAll(".message")].map((m) => m.textContent);
      expect(texts).toEqual(["Your chat was restored."]);
    });

    it("stays idle when there is no open chat or the lookup fails", async () => {
      const none = mount();
      await settle();
      expect(none.el.state.phase).toBe("idle");
      expect(none.fake.client.subscribe).not.toHaveBeenCalled();

      const failing = mount(undefined, (f) => f.client.getCurrentChat.mockRejectedValue(new Error("down")));
      await settle();
      expect(failing.el.state.phase).toBe("idle");
    });

    it("reopens the existing chat when starting one is refused with 409, keeping the typed text", async () => {
      const { el, fake, q } = mount();
      await settle();
      fake.client.startChat.mockRejectedValue(new LiveChatError("conflict", { status: 409 }));
      fake.client.getCurrentChat.mockResolvedValue(waiting);
      el.open();
      const input = q<HTMLTextAreaElement>(".input");
      input.value = "hello again";
      input.dispatchEvent(new Event("input"));
      q<HTMLButtonElement>(".send").click();
      await settle();

      expect(el.state).toMatchObject({ phase: "queued", caseId: "case-9" });
      expect(input.value).toBe("hello again");
      expect(q(".error").hidden).toBe(true);
      expect([...el.shadowRoot!.querySelectorAll(".message")].map((m) => m.textContent)).toContain(
        "You already had an open chat, so it was reopened here.",
      );
    });

    it("still explains a 409 when no open chat can be found", async () => {
      const { el, fake, q, type } = mount();
      await settle();
      fake.client.startChat.mockRejectedValue(new LiveChatError("conflict", { status: 409 }));
      el.open();
      await type("help");
      await settle();
      expect(q(".error span").textContent).toContain("already have an open chat");
    });

    it("listens again when the element is moved in the page during a chat", async () => {
      const { el, fake, type } = mount();
      el.open();
      await type("help");
      fake.client.getCurrentChat.mockResolvedValue({ ...waiting, caseId: "case-1" });
      el.remove();
      expect(fake.unsubscribe).toHaveBeenCalled();
      document.body.append(el);
      await settle();
      expect(fake.client.subscribe).toHaveBeenCalledTimes(2);
      expect(el.state.phase).toBe("queued");
    });

    it("never ends a live chat just because the status lookup failed", async () => {
      const { el, fake, q, type } = mount();
      el.open();
      await type("help");
      fake.push({ type: "error", message: "stream ended" });
      fake.client.getCurrentChat.mockRejectedValue(new LiveChatError("not found", { status: 404 }));
      q<HTMLButtonElement>(".reconnect").click();
      await settle();
      expect(el.state.phase).toBe("queued");
      expect(fake.client.subscribe).toHaveBeenCalledTimes(2);
    });

    it("works when session storage is unavailable", async () => {
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
        throw new Error("blocked");
      });
      vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
        throw new Error("blocked");
      });
      const { el } = mount(undefined, (f) => f.client.getCurrentChat.mockResolvedValue(connected));
      await settle();
      expect(el.state.phase).toBe("connected");
      el.open();
      expect(el.state.open).toBe(true);
    });
  });

  describe("with the AI assistant", () => {
    const withAssistant = (f: ReturnType<typeof fakeClient>) => f.client.isAssistantAvailable.mockResolvedValue(true);
    const bubbles = (el: LiveChatWidgetElement) =>
      [...el.shadowRoot!.querySelectorAll(".message")].map((m) => `${(m as HTMLElement).dataset.from}: ${m.textContent}`);

    it("answers in the panel, streaming and then formatting the answer", async () => {
      const { el, fake, q, type } = mount(undefined, withAssistant);
      await settle();
      el.open();
      expect(q(".intro").textContent).toContain("Ask a question");
      expect(q(".status").textContent).toBe("AI assistant");

      await type("How do I deploy?");
      expect(fake.client.askAssistant).toHaveBeenCalledWith(expect.stringMatching(/^live-chat-ai-/), "How do I deploy?");
      expect(fake.client.subscribeAssistant).toHaveBeenCalledOnce();
      expect(q(".thinking").textContent).toBe("Thinking…");
      expect(q<HTMLButtonElement>(".send").disabled).toBe(true);

      fake.answer({ type: "status", text: "Searching the knowledge base" });
      expect(q(".thinking").textContent).toBe("Searching the knowledge base");
      fake.answer({ type: "token", text: "Use the " });
      expect(bubbles(el)).toEqual(["customer: How do I deploy?", "assistant: Use the "]);
      fake.answer({ type: "done", text: "Use the **Deploy** button, then run `deploy`." });

      const answer = q('.message[data-from="assistant"]');
      expect(answer.querySelector("strong")?.textContent).toBe("Deploy");
      expect(answer.querySelector("code")?.textContent).toBe("deploy");
      expect(q(".thinking")).toBeNull();
      expect(q(".engineer").hidden).toBe(false);
    });

    it("hands the conversation to an engineer in the same panel, then returns to the assistant", async () => {
      const { el, fake, q, type } = mount(undefined, withAssistant);
      await settle();
      el.open();
      await type("It fails");
      fake.answer({ type: "done", text: "Try restarting." });

      q<HTMLButtonElement>(".engineer").click();
      await settle();
      expect(fake.client.startChat).toHaveBeenCalledWith(
        expect.objectContaining({
          message: "I'd like to talk to a support engineer.",
          priorMessages: [
            { role: "customer", content: "It fails" },
            { role: "assistant", content: "Try restarting." },
          ],
        }),
      );
      expect(el.state.phase).toBe("queued");
      expect(bubbles(el)).toEqual([
        "customer: It fails",
        "assistant: Try restarting.",
        "system: Connecting you to a support engineer. They will see this conversation.",
      ]);
      expect(q(".engineer").hidden).toBe(true);

      await type("Still broken");
      expect(fake.client.sendMessage).toHaveBeenCalledWith("case-1", { content: "Still broken" });
      expect(fake.client.askAssistant).toHaveBeenCalledOnce();

      fake.push({ type: "disconnected", engineerEmail: "eng@example.com" });
      expect(el.state.phase).toBe("ended");
      expect(q(".status").textContent).toBe("AI assistant");
      expect(q(".new").hidden).toBe(true);
      expect(q(".engineer").hidden).toBe(false);
      expect(q(".composer").hidden).toBe(false);

      await type("One more question");
      expect(fake.client.askAssistant).toHaveBeenCalledTimes(2);
      expect(fake.client.sendMessage).toHaveBeenCalledOnce();
    });

    it("offers an engineer only after repeated failures when configured", async () => {
      const { el, fake, q, type } = mount(
        { bridgeUrl: "https://bridge.example", tenant: "my-product", assistant: { offerEngineer: "after-errors", errorsBeforeEngineer: 2 } },
        withAssistant,
      );
      await settle();
      el.open();
      expect(q(".engineer").hidden).toBe(true);
      await type("a");
      fake.answer({ type: "error", text: "Too many messages." });
      expect(q(".engineer").hidden).toBe(true);
      expect(bubbles(el)).toEqual(["customer: a", "system: Too many messages."]);
      await type("b");
      fake.answer({ type: "error", text: "Too many messages." });
      expect(q(".engineer").hidden).toBe(false);
    });

    it("continues the assistant conversation after a reload", async () => {
      const first = mount(undefined, withAssistant);
      await settle();
      first.el.open();
      await first.type("Hello");
      first.fake.answer({ type: "done", text: "Hi there." });
      const conversationId = first.fake.client.askAssistant.mock.calls[0]![0];

      document.body.replaceChildren();
      const second = mount(undefined, withAssistant);
      await settle();
      second.el.open();
      expect(bubbles(second.el)).toEqual(["customer: Hello", "assistant: Hi there."]);
      await second.type("And then?");
      expect(second.fake.client.askAssistant).toHaveBeenCalledWith(conversationId, "And then?");
    });

    it("keeps the assistant part above a divider when restoring an engineer chat", async () => {
      const { el } = mount(undefined, (f) => {
        withAssistant(f);
        f.client.getCurrentChat.mockResolvedValue({ caseId: "c9", conversationId: "v9", status: "connected", engineerEmail: "e@x.com" });
        f.client.getHistory.mockResolvedValue([
          { role: "customer", content: "It fails" },
          { role: "assistant", content: "Try restarting." },
          { role: "customer", content: "I'd like to talk to a support engineer." },
          { role: "engineer", content: "Hi!" },
        ]);
      });
      await settle();
      expect(bubbles(el)).toEqual([
        "customer: It fails",
        "assistant: Try restarting.",
        "system: Support engineer chat",
        "customer: I'd like to talk to a support engineer.",
        "engineer: Hi!",
        "system: Your chat was restored.",
      ]);
    });

    it("can be turned off in the configuration", async () => {
      const { el, fake, q } = mount({ bridgeUrl: "https://bridge.example", tenant: "my-product", assistant: { enabled: false } }, withAssistant);
      await settle();
      el.open();
      expect(fake.client.isAssistantAvailable).not.toHaveBeenCalled();
      expect(q(".intro").textContent).toContain("Describe what you need");
      expect(q(".engineer").hidden).toBe(true);
    });

    it("shows a failure when the stream drops mid-answer", async () => {
      const { el, fake, type } = mount(undefined, withAssistant);
      await settle();
      el.open();
      await type("Hello");
      fake.answer({ type: "disconnected", message: "network" });
      expect(bubbles(el)).toEqual(["customer: Hello", "system: The connection was lost."]);
    });
  });
});
