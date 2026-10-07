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
  type LiveChatCurrentChat,
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

/** Why a chat ended, as reported in the `live-chat-ended` event. "unknown"
 * means it ended while the page was not listening, for example during a
 * reload. */
export type LiveChatEndReason = "customer" | "engineer" | "converted" | "expired" | "unknown";

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
  resumed: "live-chat-resumed",
  assigned: "live-chat-assigned",
  ended: "live-chat-ended",
  error: "live-chat-error",
} as const;

export type AccessTokenProvider = () => Promise<string> | string;

/** A response from the host's HTTP client. A non-2xx status resolves; only a
 * failure to get any response rejects. */
export interface LiveChatTransportResponse {
  status: number;
  text: string;
}

/** Sends authenticated JSON requests through the host's HTTP client. */
export interface LiveChatRequestTransport {
  getJson(url: string): Promise<LiveChatTransportResponse>;
  postJson(url: string, body: unknown): Promise<LiveChatTransportResponse>;
}

/** Opens the authenticated event stream through the host's HTTP client.
 * Rejects when no usable stream can be opened; `signal` aborts it. */
export interface LiveChatStreamTransport {
  openStream(url: string, signal: AbortSignal): Promise<ReadableStream<Uint8Array>>;
}

/**
 * Sends the widget's requests through the host product's own HTTP client
 * instead of attaching a token, for products whose sign-in keeps the token
 * away from page scripts (for example the Asgardeo SDK's web-worker storage).
 */
export interface LiveChatTransport {
  requestTransport: LiveChatRequestTransport;
  streamTransport: LiveChatStreamTransport;
}

// The limits console-chat-bridge enforces on priorMessages. Starting a chat
// with more than this fails, so older turns are dropped and long ones cut.
const MAX_PRIOR_MESSAGES = 20;
const MAX_PRIOR_MESSAGE_BYTES = 4000;
const MAX_PRIOR_TOTAL_BYTES = 32 * 1024;

const utf8 = new TextEncoder();

function truncateUtf8(text: string, maxBytes: number): string {
  if (utf8.encode(text).length <= maxBytes) return text;
  const ellipsis = "…";
  const budget = maxBytes - utf8.encode(ellipsis).length;
  let bytes = 0;
  let end = 0;
  for (const char of text) {
    const size = utf8.encode(char).length;
    if (bytes + size > budget) break;
    bytes += size;
    end += char.length;
  }
  return text.slice(0, end) + ellipsis;
}

// fitPriorMessages keeps the most recent messages that fit the bridge's limits.
function fitPriorMessages(messages: LiveChatPriorMessage[]): LiveChatPriorMessage[] {
  const kept: LiveChatPriorMessage[] = [];
  let total = 0;
  for (const message of messages.slice(-MAX_PRIOR_MESSAGES).reverse()) {
    const content = truncateUtf8(message.content, MAX_PRIOR_MESSAGE_BYTES);
    total += utf8.encode(content).length;
    if (total > MAX_PRIOR_TOTAL_BYTES) break;
    kept.unshift({ role: message.role, content });
  }
  return kept;
}

const CHAT_ICON =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>';

/**
 * LiveChatWidgetElement implements `<wso2-live-chat>`, a "Chat with Support"
 * widget for any host product.
 *
 * It is configured through the `config` property or attribute, or a JSON file
 * named by `config-src`. `getAccessToken` (or `transport`) must be set from
 * code, because a function cannot be expressed in JSON.
 */
export class LiveChatWidgetElement extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["config", "config-src"];
  }


  /** @internal Builds the SDK client; replaced in tests. */
  createClient: (config: LiveChatClientConfig) => LiveChatClient = createLiveChatClient;

  private tokenProvider: AccessTokenProvider | null = null;
  private customTransport: LiveChatTransport | null = null;
  private restoreStarted = false;
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

  /** Returns the signed-in user's access token. Called before every request. */
  get getAccessToken(): AccessTokenProvider | null {
    return this.tokenProvider;
  }

  set getAccessToken(provider: AccessTokenProvider | null) {
    this.tokenProvider = provider;
    this.resetClient();
  }

  /** The host product's own HTTP client; used instead of getAccessToken when set. */
  get transport(): LiveChatTransport | null {
    return this.customTransport;
  }

  set transport(value: LiveChatTransport | null) {
    this.customTransport = value;
    this.resetClient();
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
    // Detaching stopped listening; resume an in-progress chat when re-attached.
    if (this.caseId && (this.phase === "queued" || this.phase === "connected") && !this.unsubscribe) {
      void this.reconnect();
    } else {
      this.maybeRestore();
    }
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
      this.priorMessages = fitPriorMessages(
        options.priorMessages.filter(
          (m) => (m.role === "customer" || m.role === "assistant") && typeof m.content === "string" && m.content.trim() !== "",
        ),
      );
    }
    if (!this.isOpen) {
      this.isOpen = true;
      this.rememberOpen(true);
      this.emit(LIVE_CHAT_EVENTS.open, {});
    }
    this.render();
    this.ui.input.focus();
  }

  close(): void {
    if (!this.isOpen) return;
    this.isOpen = false;
    this.rememberOpen(false);
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
    this.restoreStarted = false;
    this.render();
    this.maybeRestore();
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
    if (!this.resolved) return null;
    if (!this.client) {
      const base = { baseUrl: this.resolved.bridgeUrl, tenant: this.resolved.tenant };
      const transport = this.customTransport;
      const getAccessToken = this.tokenProvider;
      if (transport) {
        this.client = this.createClient({
          ...base,
          requestTransport: transport.requestTransport,
          streamTransport: transport.streamTransport,
        });
      } else if (getAccessToken) {
        this.client = this.createClient({ ...base, getAccessToken: () => getAccessToken() });
      } else {
        return null;
      }
    }
    return this.client;
  }

  // A new token function or transport needs a new client, and may now be
  // able to find an open chat that could not be looked up before.
  private resetClient(): void {
    this.client = null;
    this.restoreStarted = false;
    this.maybeRestore();
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
      if (err instanceof LiveChatError && err.status === 409 && (await this.resumeExisting())) return;
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
        // The bridge replays this event to a reconnecting page, so when
        // already connected it only fills in an engineer not yet known.
        if (this.phase === "connected" && (engineer === this.engineer || this.engineer === null)) {
          this.engineer = engineer;
          break;
        }
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
    const caseId = this.caseId;
    if (!caseId || !this.getClient()) return;
    this.error = null;
    this.render();
    const restored = await this.loadTranscript(caseId);
    if (this.caseId !== caseId) return;
    if (restored.length > 0) this.transcript = restored;
    if (restored.some((m) => m.from === "engineer")) this.phase = "connected";
    this.listen(caseId);
    this.render();
    await this.reconcile(caseId);
  }

  // Resumes the customer's open chat after a page reload or in another tab:
  // the server still holds it, and starting a new one would be refused.
  private maybeRestore(): void {
    if (this.restoreStarted || !this.isConnected || this.phase !== "idle") return;
    const client = this.getClient();
    if (!client) return;
    this.restoreStarted = true;
    client.getCurrentChat().then(
      (chat) => {
        if (chat && this.phase === "idle") void this.resume(chat, this.texts().chatResumed, this.wasOpen());
      },
      () => {
        // Best effort: starting a chat later still recovers an open one.
      },
    );
  }

  // After a 409 on start: reopen the chat the server says is already open.
  private async resumeExisting(): Promise<boolean> {
    let chat: LiveChatCurrentChat | null;
    try {
      chat = (await this.getClient()?.getCurrentChat()) ?? null;
    } catch {
      return false;
    }
    if (!chat || this.phase !== "idle") return false;
    await this.resume(chat, this.texts().alreadyOpenResumed, false);
    return true;
  }

  private async resume(chat: LiveChatCurrentChat, notice: string, openPanel: boolean): Promise<void> {
    this.caseId = chat.caseId;
    this.engineer = chat.engineerEmail ?? null;
    this.phase = chat.status === "connected" ? "connected" : "queued";
    this.priorMessages = [];
    this.error = null;
    this.transcript = [];
    this.render();
    const history = await this.loadTranscript(chat.caseId);
    if (this.caseId !== chat.caseId) return;
    this.transcript = [...history, { from: "system", text: notice }, ...this.transcript];
    this.listen(chat.caseId);
    if (openPanel) this.open();
    this.render();
    this.emit(LIVE_CHAT_EVENTS.resumed, { caseId: chat.caseId });
    await this.reconcile(chat.caseId);
  }

  // Catches an accept, or the chat ending, that happened before the event
  // stream was open and so was never delivered.
  private async reconcile(caseId: string): Promise<void> {
    let chat: LiveChatCurrentChat | null;
    try {
      chat = (await this.getClient()?.getCurrentChat()) ?? null;
    } catch {
      return;
    }
    if (this.caseId !== caseId || (this.phase !== "queued" && this.phase !== "connected")) return;
    if (!chat || chat.caseId !== caseId) {
      this.transcript.push({ from: "system", text: this.texts().endedWhileAway });
      this.finish("unknown");
      return;
    }
    if (chat.status === "connected" && this.phase === "queued") {
      this.phase = "connected";
      this.engineer = chat.engineerEmail ?? this.engineer;
      this.transcript.push({ from: "system", text: format(this.texts().engineerJoined, { engineer: this.engineerName() }) });
      this.emit(LIVE_CHAT_EVENTS.assigned, { caseId, engineerEmail: chat.engineerEmail ?? "" });
      this.render();
    }
  }

  // History also holds the AI-assistant conversation handed over when the
  // chat started; only what follows it belongs in this transcript.
  private async loadTranscript(caseId: string): Promise<LiveChatTranscriptEntry[]> {
    const client = this.getClient();
    if (!client) return [];
    try {
      const history = await client.getHistory(caseId);
      const live = history.slice(history.map((m) => m.role).lastIndexOf("assistant") + 1);
      return live.flatMap((m): LiveChatTranscriptEntry[] =>
        m.role === "customer" || m.role === "engineer" ? [{ from: m.role, text: m.content }] : [],
      );
    } catch {
      return [];
    }
  }

  private openKey(): string | null {
    return this.resolved ? `wso2-live-chat:${this.resolved.tenant}:open` : null;
  }

  // Remembered per browser tab so a reload reopens the panel only if it was
  // open. Storage can be unavailable (privacy modes); that just loses this.
  private rememberOpen(open: boolean): void {
    const key = this.openKey();
    if (!key) return;
    try {
      if (open) sessionStorage.setItem(key, "1");
      else sessionStorage.removeItem(key);
    } catch {
      // Storage unavailable.
    }
  }

  private wasOpen(): boolean {
    const key = this.openKey();
    if (!key) return false;
    try {
      return sessionStorage.getItem(key) === "1";
    } catch {
      return false;
    }
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
        ? this.engineer
          ? format(texts.statusConnected, { engineer: this.engineer })
          : texts.statusConnectedAnonymous
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
