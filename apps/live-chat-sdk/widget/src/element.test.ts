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
import { LiveChatError, type LiveChatEvent } from "@wso2/live-chat-client";
import "./index.js";
import { LIVE_CHAT_EVENTS, type LiveChatEndedDetail, type LiveChatWidgetElement } from "./element.js";

function fakeClient() {
  let onEvent: ((e: LiveChatEvent) => void) | null = null;
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
  };
  return { client, unsubscribe, push: (e: LiveChatEvent) => onEvent?.(e) };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

function mount(config: object = { bridgeUrl: "https://bridge.example", tenant: "my-product" }) {
  const el = document.createElement("wso2-live-chat") as LiveChatWidgetElement;
  const fake = fakeClient();
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
  beforeEach(() => document.body.replaceChildren());
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
});
