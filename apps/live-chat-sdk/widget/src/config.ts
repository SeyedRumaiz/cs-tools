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

/** Which bottom corner the launcher button and chat panel sit in. */
export type LiveChatPosition = "bottom-right" | "bottom-left";

/** Size and placement. All lengths are CSS pixels. */
export interface LiveChatLayout {
  position: LiveChatPosition;
  /** Show the floating launcher button. Turn off when your product opens
   * the chat from its own button by calling `element.open()`. */
  showLauncher: boolean;
  /** Distance from the side edge of the window. */
  offsetX: number;
  /** Distance from the bottom edge of the window. */
  offsetY: number;
  /** Panel size; shrinks to fit small windows. */
  width: number;
  height: number;
  zIndex: number;
}

/** Colors, font and shape. Any CSS color / font value is accepted. */
export interface LiveChatTheme {
  /** Launcher, header, buttons and the customer's own messages. */
  primaryColor: string;
  /** Text and icons drawn on primaryColor. */
  onPrimaryColor: string;
  backgroundColor: string;
  /** Engineer messages and the message box. */
  surfaceColor: string;
  textColor: string;
  mutedTextColor: string;
  borderColor: string;
  errorColor: string;
  fontFamily: string;
  fontSize: string;
  borderRadius: string;
  shadow: string;
}

/** Every piece of text the widget shows. `{engineer}` and `{caseId}` are
 * replaced where noted. */
export interface LiveChatTexts {
  title: string;
  subtitle: string;
  launcherLabel: string;
  intro: string;
  /** Shown when the chat was opened with earlier AI-assistant messages. */
  contextNotice: string;
  inputPlaceholder: string;
  startButton: string;
  sendButton: string;
  endButton: string;
  newChatButton: string;
  reconnectButton: string;
  closeLabel: string;
  statusIdle: string;
  statusStarting: string;
  statusQueued: string;
  /** `{engineer}` is the engineer's email. */
  statusConnected: string;
  statusEnded: string;
  /** `{engineer}` is the engineer's email. */
  engineerJoined: string;
  endedByCustomer: string;
  endedByEngineer: string;
  /** `{caseId}` is the new support case id. */
  convertedToCase: string;
  expired: string;
  connectionLost: string;
  startFailed: string;
  alreadyOpen: string;
  sendFailed: string;
  endFailed: string;
  notConfigured: string;
}

/**
 * What a product supplies, typically as a JSON configuration file. Only
 * `bridgeUrl` and `tenant` are required; everything else falls back to the
 * defaults below, key by key.
 */
export interface LiveChatWidgetConfig {
  /** console-chat-bridge base URL, e.g. "https://support.example.com". */
  bridgeUrl: string;
  /** This product's tenant slug on console-chat-bridge. */
  tenant: string;
  layout?: Partial<LiveChatLayout>;
  theme?: Partial<LiveChatTheme>;
  texts?: Partial<LiveChatTexts>;
  /** Prefix for the conversation ids the widget mints. */
  conversationIdPrefix?: string;
}

export interface ResolvedLiveChatConfig {
  bridgeUrl: string;
  tenant: string;
  layout: LiveChatLayout;
  theme: LiveChatTheme;
  texts: LiveChatTexts;
  conversationIdPrefix: string;
}

export const DEFAULT_LAYOUT: LiveChatLayout = {
  position: "bottom-right",
  showLauncher: true,
  offsetX: 24,
  offsetY: 24,
  width: 380,
  height: 560,
  zIndex: 1000,
};

export const DEFAULT_THEME: LiveChatTheme = {
  primaryColor: "#ff7300",
  onPrimaryColor: "#ffffff",
  backgroundColor: "#ffffff",
  surfaceColor: "#f3f4f6",
  textColor: "#1f2937",
  mutedTextColor: "#6b7280",
  borderColor: "#e5e7eb",
  errorColor: "#b91c1c",
  fontFamily: "system-ui, -apple-system, 'Segoe UI', Roboto, Arial, sans-serif",
  fontSize: "14px",
  borderRadius: "12px",
  shadow: "0 12px 32px rgba(0, 0, 0, 0.18)",
};

export const DEFAULT_TEXTS: LiveChatTexts = {
  title: "Chat with Support",
  subtitle: "A WSO2 support engineer will help you",
  launcherLabel: "Chat with Support",
  intro: "Describe what you need help with to start a chat with a support engineer.",
  contextNotice: "Your conversation with the assistant will be shared with the engineer.",
  inputPlaceholder: "Type your message…",
  startButton: "Start chat",
  sendButton: "Send",
  endButton: "End chat",
  newChatButton: "Start a new chat",
  reconnectButton: "Reconnect",
  closeLabel: "Close chat",
  statusIdle: "Not connected",
  statusStarting: "Starting your chat…",
  statusQueued: "Waiting for an engineer",
  statusConnected: "Connected with {engineer}",
  statusEnded: "Chat ended",
  engineerJoined: "{engineer} joined the chat.",
  endedByCustomer: "You ended the chat.",
  endedByEngineer: "The engineer ended the chat.",
  convertedToCase: "The engineer turned this chat into support case {caseId}.",
  expired: "No engineer was available in time. Please try again.",
  connectionLost: "The connection was lost.",
  startFailed: "Could not start the chat. Please try again.",
  alreadyOpen: "You already have an open chat. Please continue it or end it first.",
  sendFailed: "Your message could not be sent.",
  endFailed: "Could not end the chat. Please try again.",
  notConfigured: "Live chat is not configured.",
};

const DEFAULT_CONVERSATION_PREFIX = "live-chat-";

/** Thrown when a configuration is missing something it cannot work without. */
export class LiveChatConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "LiveChatConfigError";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// Copies only known keys whose value has the same type as the default, so a
// typo or a wrong type in a hand-written JSON file falls back to the
// default instead of breaking the widget.
function pick<T extends object>(defaults: T, overrides: unknown): T {
  const result = { ...defaults };
  if (!isRecord(overrides)) return result;
  for (const key of Object.keys(defaults) as (keyof T)[]) {
    const value = overrides[key as string];
    if (value !== undefined && typeof value === typeof defaults[key]) {
      result[key] = value as T[keyof T];
    }
  }
  return result;
}

/** Merges a product's configuration over the defaults and validates it. */
export function resolveConfig(input: unknown): ResolvedLiveChatConfig {
  if (!isRecord(input)) {
    throw new LiveChatConfigError("Live chat config must be an object.");
  }
  const bridgeUrl = typeof input.bridgeUrl === "string" ? input.bridgeUrl.trim().replace(/\/+$/, "") : "";
  const tenant = typeof input.tenant === "string" ? input.tenant.trim() : "";
  if (!bridgeUrl) throw new LiveChatConfigError("Live chat config needs a bridgeUrl.");
  if (!tenant) throw new LiveChatConfigError("Live chat config needs a tenant.");

  const layout = pick(DEFAULT_LAYOUT, input.layout);
  if (layout.position !== "bottom-right" && layout.position !== "bottom-left") {
    layout.position = DEFAULT_LAYOUT.position;
  }
  for (const key of ["offsetX", "offsetY", "width", "height", "zIndex"] as const) {
    if (!Number.isFinite(layout[key]) || layout[key] < 0) layout[key] = DEFAULT_LAYOUT[key];
  }

  return {
    bridgeUrl,
    tenant,
    layout,
    theme: pick(DEFAULT_THEME, input.theme),
    texts: pick(DEFAULT_TEXTS, input.texts),
    conversationIdPrefix:
      typeof input.conversationIdPrefix === "string" ? input.conversationIdPrefix : DEFAULT_CONVERSATION_PREFIX,
  };
}

/** Fills `{name}` placeholders. Unknown placeholders are left as they are. */
export function format(template: string, values: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (match, name: string) => values[name] ?? match);
}
