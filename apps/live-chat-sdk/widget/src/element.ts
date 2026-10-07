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

import {
  createLiveChatClient,
  LiveChatError,
  type LiveChatClient,
  type LiveChatClientConfig,
  type LiveChatEvent,
} from "@wso2/live-chat-client";
import {
  DEFAULT_TEXTS,
  format,
  LiveChatConfigError,
  resolveConfig,
  type LiveChatTexts,
  type ResolvedLiveChatConfig,
} from "./config.js";
import { layoutVariables, STYLES, THEME_VARIABLES } from "./styles.js";

/** One earlier AI-assistant turn handed to the engineer as context. */
export interface LiveChatPriorMessage {
  role: "customer" | "assistant";
  content: string;
}

export interface LiveChatOpenOptions {
  /** Earlier AI-assistant conversation to send with the next chat. */
  priorMessages?: LiveChatPriorMessage[];
}

export type LiveChatPhase = "idle" | "starting" | "queued" | "connected" | "ended";

/** Why a chat ended, as reported in the `live-chat-ended` event. */
export type LiveChatEndReason = "customer" | "engineer" | "converted" | "expired";

export interface LiveChatTranscriptEntry {
  from: "customer" | "engineer" | "system";
  text: string;
}

export interface LiveChatEndedDetail {
  caseId: string;
  reason: LiveChatEndReason;
  /** Set when reason is "converted". */
  entityCaseId?: string;
  transcript: LiveChatTranscriptEntry[];
}

/** DOM events dispatched by the element. All of them bubble and are composed. */
export const LIVE_CHAT_EVENTS = {
  open: "live-chat-open",
  close: "live-chat-close",
  started: "live-chat-started",
  assigned: "live-chat-assigned",
  ended: "live-chat-ended",
  error: "live-chat-error",
} as const;

export type AccessTokenProvider = () => Promise<string> | string;

const CHAT_ICON =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>';

/**
 * LiveChatWidgetElement implements `<wso2-live-chat>`, a "Chat with Support"
 * widget for any host product.
 *
 * It is configured through the `config` property or attribute, or a JSON file
 * named by `config-src`. `getAccessToken` must be set from code, because a
 * function cannot be expressed in JSON.
 */
export class LiveChatWidgetElement extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["config", "config-src"];
  }

  /** Returns the signed-in user's access token. Called before every request. */
  getAccessToken: AccessTokenProvider | null = null;

  /** @internal Builds the SDK client; replaced in tests. */
  createClient: (config: LiveChatClientConfig) => LiveChatClient = createLiveChatClient;

  private resolved: ResolvedLiveChatConfig | null = null;
  private configError: string | null = null;
  private client: LiveChatClient | null = null;
  private unsubscribe: (() => void) | null = null;

  private isOpen = false;
  private phase: LiveChatPhase = "idle";
  private caseId: string | null = null;
  private engineer: string | null = null;
  private transcript: LiveChatTranscriptEntry[] = [];
  private priorMessages: LiveChatPriorMessage[] = [];
  private error: { text: string; canReconnect: boolean } | null = null;

  private readonly root: ShadowRoot;
  private readonly ui: {
    launcher: HTMLButtonElement;
    launcherLabel: HTMLSpanElement;
    panel: HTMLElement;
    title: HTMLHeadingElement;
    subtitle: HTMLParagraphElement;
    close: HTMLButtonElement;
    status: HTMLDivElement;
    messages: HTMLDivElement;
    error: HTMLDivElement;
    errorText: HTMLSpanElement;
    reconnect: HTMLButtonElement;
    composer: HTMLDivElement;
    input: HTMLTextAreaElement;
    send: HTMLButtonElement;
    actions: HTMLDivElement;
    end: HTMLButtonElement;
    newChat: HTMLButtonElement;
  };

  constructor() {
    super();
    this.root = this.attachShadow({ mode: "open" });
    this.root.innerHTML = `
      <style>${STYLES}</style>
      <button class="launcher" part="launcher" type="button">${CHAT_ICON}<span></span></button>
      <section class="panel" part="panel" role="dialog" hidden>
        <header class="header" part="header">
          <div><h2 class="title"></h2><p class="subtitle"></p></div>
          <button class="close" type="button">&times;</button>
        </header>
        <div class="status" part="status"></div>
        <div class="messages" part="messages" role="log" aria-live="polite"></div>
        <div class="error" part="error" role="alert" hidden>
          <span></span><button class="button link reconnect" type="button"></button>
        </div>
        <div class="composer" part="composer">
          <textarea class="input" rows="1"></textarea>
          <button class="button send" type="button"></button>
        </div>
        <div class="actions" part="actions">
          <button class="button secondary end" type="button"></button>
          <button class="button new" type="button"></button>
        </div>
      </section>`;
    const q = <T extends Element>(selector: string): T => this.root.querySelector(selector) as T;
    this.ui = {
      launcher: q(".launcher"),
      launcherLabel: q(".launcher span"),
      panel: q(".panel"),
      title: q(".title"),
      subtitle: q(".subtitle"),
      close: q(".close"),
      status: q(".status"),
      messages: q(".messages"),
      error: q(".error"),
      errorText: q(".error span"),
      reconnect: q(".reconnect"),
      composer: q(".composer"),
      input: q(".input"),
      send: q(".send"),
      actions: q(".actions"),
      end: q(".end"),
      newChat: q(".new"),
    };

    this.ui.launcher.addEventListener("click", () => this.toggle());
    this.ui.close.addEventListener("click", () => this.close());
    this.ui.send.addEventListener("click", () => void this.submit());
    this.ui.end.addEventListener("click", () => void this.endChat());
    this.ui.newChat.addEventListener("click", () => this.startNewChat());
    this.ui.reconnect.addEventListener("click", () => void this.reconnect());
    this.ui.input.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        void this.submit();
      }
    });
    this.ui.input.addEventListener("input", () => this.renderComposer());
  }

  /** The configuration in use, after defaults were applied. */
  get config(): ResolvedLiveChatConfig | null {
    return this.resolved;
  }

  /** Applies a configuration object; missing keys use the defaults. */
  set config(value: unknown) {
    this.applyConfig(value);
  }

  /** The current phase, case id and whether the panel is open. */
  get state(): { phase: LiveChatPhase; caseId: string | null; open: boolean } {
    return { phase: this.phase, caseId: this.caseId, open: this.isOpen };
  }

  attributeChangedCallback(name: string, _old: string | null, value: string | null): void {
    if (value === null) return;
    if (name === "config") {
      try {
        this.applyConfig(JSON.parse(value));
      } catch {
        this.fail("The config attribute is not valid JSON.");
      }
    } else if (name === "config-src") {
      void this.loadConfig(value);
    }
  }

  connectedCallback(): void {
    this.render();
  }

  disconnectedCallback(): void {
    // Stop listening without ending the chat; ending it is always an
    // explicit customer action.
    this.stopListening();
  }

  /** Fetches a JSON configuration file and applies it. */
  async loadConfig(url: string): Promise<void> {
    try {
      const response = await fetch(url, { credentials: "same-origin" });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      this.applyConfig(await response.json());
    } catch (err) {
      this.fail(`Could not load the live chat config from ${url}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  /** Opens the panel. Any priorMessages are sent with the next chat the
   * customer starts. */
  open(options: LiveChatOpenOptions = {}): void {
    if (options.priorMessages && (this.phase === "idle" || this.phase === "ended")) {
      if (this.phase === "ended") this.resetChat();
      this.priorMessages = options.priorMessages
        .filter((m) => (m.role === "customer" || m.role === "assistant") && typeof m.content === "string" && m.content.trim() !== "")
        .map((m) => ({ role: m.role, content: m.content }));
    }
    if (!this.isOpen) {
      this.isOpen = true;
      this.emit(LIVE_CHAT_EVENTS.open, {});
    }
    this.render();
    this.ui.input.focus();
  }

  close(): void {
    if (!this.isOpen) return;
    this.isOpen = false;
    this.render();
    this.emit(LIVE_CHAT_EVENTS.close, {});
  }

  toggle(): void {
    if (this.isOpen) this.close();
    else this.open();
  }

  /** Ends the current chat from the customer's side. */
  async endChat(): Promise<void> {
    const caseId = this.caseId;
    if (!caseId || (this.phase !== "queued" && this.phase !== "connected") || !this.client) return;
    this.stopListening();
    try {
      await this.client.completeChat(caseId);
    } catch {
      this.listen(caseId);
      this.showError(this.texts().endFailed, false);
      return;
    }
    this.finish("customer");
  }

  /** Clears an ended chat so the customer can start another one. */
  startNewChat(): void {
    if (this.phase !== "ended" && this.phase !== "idle") return;
    this.resetChat();
    this.priorMessages = [];
    this.render();
    this.ui.input.focus();
  }

  private applyConfig(value: unknown): void {
    try {
      this.resolved = resolveConfig(value);
      this.configError = null;
      this.client = null;
    } catch (err) {
      this.fail(err instanceof LiveChatConfigError ? err.message : String(err));
      return;
    }
    const { layout, theme } = this.resolved;
    for (const [key, variable] of Object.entries(THEME_VARIABLES)) {
      this.style.setProperty(variable, theme[key as keyof typeof theme]);
    }
    for (const [variable, val] of Object.entries(layoutVariables(layout))) {
      this.style.setProperty(variable, val);
    }
    this.dataset.position = layout.position;
    this.dataset.launcher = String(layout.showLauncher);
    this.render();
  }

  private fail(message: string): void {
    this.resolved = null;
    this.configError = message;
    console.error(`[wso2-live-chat] ${message}`);
    this.render();
  }

  private texts(): LiveChatTexts {
    return this.resolved?.texts ?? DEFAULT_TEXTS;
  }

  private getClient(): LiveChatClient | null {
    if (!this.resolved || !this.getAccessToken) return null;
    if (!this.client) {
      const getAccessToken = this.getAccessToken;
      this.client = this.createClient({
        baseUrl: this.resolved.bridgeUrl,
        tenant: this.resolved.tenant,
        getAccessToken: () => getAccessToken(),
      });
    }
    return this.client;
  }

  private async submit(): Promise<void> {
    const text = this.ui.input.value.trim();
    if (!text) return;
    if (this.phase === "idle") await this.start(text);
    else if (this.phase === "queued" || this.phase === "connected") await this.send(text);
  }

  private async start(message: string): Promise<void> {
    const client = this.getClient();
    if (!client || !this.resolved) {
      this.showError(this.texts().notConfigured, false);
      return;
    }
    this.phase = "starting";
    this.error = null;
    this.render();
    try {
      const result = await client.startChat({
        message,
        conversationId: `${this.resolved.conversationIdPrefix}${Date.now()}`,
        ...(this.priorMessages.length > 0 ? { priorMessages: this.priorMessages } : {}),
      });
      this.caseId = result.caseId;
      this.phase = "queued";
      this.transcript.push({ from: "customer", text: message });
      this.ui.input.value = "";
      this.listen(result.caseId);
      this.emit(LIVE_CHAT_EVENTS.started, { caseId: result.caseId });
    } catch (err) {
      this.phase = "idle";
      const texts = this.texts();
      this.showError(err instanceof LiveChatError && err.status === 409 ? texts.alreadyOpen : texts.startFailed, false);
      this.emit(LIVE_CHAT_EVENTS.error, { message: err instanceof Error ? err.message : String(err) });
    }
    this.render();
  }

  private async send(text: string): Promise<void> {
    const client = this.getClient();
    if (!client || !this.caseId) return;
    this.ui.input.value = "";
    this.transcript.push({ from: "customer", text });
    this.render();
    try {
      await client.sendMessage(this.caseId, { content: text });
    } catch {
      this.transcript.push({ from: "system", text: this.texts().sendFailed });
      this.render();
    }
  }

  private listen(caseId: string): void {
    const client = this.getClient();
    if (!client) return;
    this.stopListening();
    this.unsubscribe = client.subscribe(caseId, (event) => this.onEvent(event));
  }

  private stopListening(): void {
    this.unsubscribe?.();
    this.unsubscribe = null;
  }

  private onEvent(event: LiveChatEvent): void {
    const texts = this.texts();
    switch (event.type) {
      case "queued":
        if (this.phase !== "connected") this.phase = "queued";
        break;
      case "assigned": {
        const engineer = event.engineerEmail || null;
        // Ignore a repeated assigned event for the same engineer.
        if (this.phase === "connected" && engineer === this.engineer) break;
        this.phase = "connected";
        this.engineer = engineer;
        this.transcript.push({ from: "system", text: format(texts.engineerJoined, { engineer: this.engineerName() }) });
        this.emit(LIVE_CHAT_EVENTS.assigned, { caseId: this.caseId, engineerEmail: event.engineerEmail });
        break;
      }
      case "message":
        if (this.phase === "queued") this.phase = "connected";
        this.transcript.push({ from: "engineer", text: event.content });
        break;
      case "disconnected":
        this.transcript.push({ from: "system", text: texts.endedByEngineer });
        this.finish("engineer");
        return;
      case "converted":
        this.transcript.push({ from: "system", text: format(texts.convertedToCase, { caseId: event.entityCaseId }) });
        this.finish("converted", event.entityCaseId);
        return;
      case "expired":
        this.transcript.push({ from: "system", text: event.message || texts.expired });
        this.finish("expired");
        return;
      case "error":
        this.stopListening();
        this.showError(texts.connectionLost, true);
        this.emit(LIVE_CHAT_EVENTS.error, { message: event.message });
        return;
    }
    this.render();
  }

  // The bridge does not replay events missed while disconnected, so the
  // transcript is reloaded before listening again.
  private async reconnect(): Promise<void> {
    const client = this.getClient();
    if (!client || !this.caseId) return;
    this.error = null;
    this.render();
    try {
      const history = await client.getHistory(this.caseId);
      const restored: LiveChatTranscriptEntry[] = [];
      for (const m of history) {
        if (m.role === "customer") restored.push({ from: "customer", text: m.content });
        else if (m.role === "engineer") restored.push({ from: "engineer", text: m.content });
      }
      if (restored.length > 0) this.transcript = restored;
      if (restored.some((m) => m.from === "engineer")) this.phase = "connected";
    } catch {
      // History is best effort; the new subscription still delivers later
      // messages.
    }
    this.listen(this.caseId);
    this.render();
  }

  private finish(reason: LiveChatEndReason, entityCaseId?: string): void {
    this.stopListening();
    this.phase = "ended";
    if (reason === "customer") this.transcript.push({ from: "system", text: this.texts().endedByCustomer });
    this.error = null;
    this.render();
    this.emit(LIVE_CHAT_EVENTS.ended, {
      caseId: this.caseId ?? "",
      reason,
      ...(entityCaseId ? { entityCaseId } : {}),
      transcript: this.transcript.map((t) => ({ ...t })),
    } satisfies LiveChatEndedDetail);
  }

  private resetChat(): void {
    this.stopListening();
    this.phase = "idle";
    this.caseId = null;
    this.engineer = null;
    this.transcript = [];
    this.error = null;
  }

  private showError(text: string, canReconnect: boolean): void {
    this.error = { text, canReconnect };
    this.render();
  }

  private engineerName(): string {
    return this.engineer ?? "An engineer";
  }

  private emit(name: string, detail: object): void {
    this.dispatchEvent(new CustomEvent(name, { detail, bubbles: true, composed: true }));
  }

  private render(): void {
    const texts = this.texts();
    const ui = this.ui;
    const showLauncher = this.resolved?.layout.showLauncher ?? false;

    ui.launcher.hidden = !this.resolved || !showLauncher || this.isOpen;
    ui.launcherLabel.textContent = texts.launcherLabel;
    ui.launcher.setAttribute("aria-label", texts.launcherLabel);
    ui.panel.hidden = !this.isOpen;
    ui.panel.setAttribute("aria-label", texts.title);
    ui.title.textContent = texts.title;
    ui.subtitle.textContent = texts.subtitle;
    ui.close.setAttribute("aria-label", texts.closeLabel);

    ui.status.dataset.phase = this.phase;
    ui.status.textContent =
      this.phase === "connected"
        ? format(texts.statusConnected, { engineer: this.engineerName() })
        : {
            idle: texts.statusIdle,
            starting: texts.statusStarting,
            queued: texts.statusQueued,
            ended: texts.statusEnded,
          }[this.phase];

    this.renderMessages();

    const error = this.configError ?? this.error?.text ?? null;
    ui.error.hidden = error === null;
    ui.errorText.textContent = error ?? "";
    ui.reconnect.hidden = !(this.error?.canReconnect && this.configError === null);
    ui.reconnect.textContent = texts.reconnectButton;

    this.renderComposer();

    const active = this.phase === "queued" || this.phase === "connected";
    ui.end.hidden = !active;
    ui.end.textContent = texts.endButton;
    ui.newChat.hidden = this.phase !== "ended";
    ui.newChat.textContent = texts.newChatButton;
    ui.actions.hidden = ui.end.hidden && ui.newChat.hidden;
  }

  private renderMessages(): void {
    const texts = this.texts();
    const nodes: HTMLElement[] = [];
    const paragraph = (cls: string, text: string) => {
      const p = document.createElement("p");
      p.className = cls;
      p.textContent = text;
      return p;
    };
    if (this.phase === "idle" || (this.phase === "starting" && this.transcript.length === 0)) {
      nodes.push(paragraph("intro", texts.intro));
      if (this.priorMessages.length > 0) nodes.push(paragraph("notice", texts.contextNotice));
    }
    for (const entry of this.transcript) {
      const div = document.createElement("div");
      div.className = "message";
      div.dataset.from = entry.from;
      div.textContent = entry.text;
      nodes.push(div);
    }
    this.ui.messages.replaceChildren(...nodes);
    this.ui.messages.scrollTop = this.ui.messages.scrollHeight;
  }

  private renderComposer(): void {
    const texts = this.texts();
    const { composer, input, send } = this.ui;
    const usable = this.resolved !== null && this.phase !== "ended";
    composer.hidden = !usable;
    input.placeholder = texts.inputPlaceholder;
    input.disabled = this.phase === "starting";
    send.textContent = this.phase === "idle" || this.phase === "starting" ? texts.startButton : texts.sendButton;
    send.disabled = this.phase === "starting" || input.value.trim() === "";
  }
}

/** Registers the element under `tagName` once; safe to call repeatedly. */
export function defineLiveChatWidget(tagName = "wso2-live-chat"): void {
  if (!customElements.get(tagName)) {
    customElements.define(tagName, class extends LiveChatWidgetElement {});
  }
}
