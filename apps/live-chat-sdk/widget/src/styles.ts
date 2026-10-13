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

import type { LiveChatLayout, LiveChatTheme } from "./config.js";

/** Maps each theme key to the CSS custom property the stylesheet reads. */
export const THEME_VARIABLES: Record<keyof LiveChatTheme, string> = {
  primaryColor: "--lc-primary",
  onPrimaryColor: "--lc-on-primary",
  backgroundColor: "--lc-background",
  surfaceColor: "--lc-surface",
  textColor: "--lc-text",
  mutedTextColor: "--lc-muted",
  borderColor: "--lc-border",
  errorColor: "--lc-error",
  fontFamily: "--lc-font-family",
  fontSize: "--lc-font-size",
  borderRadius: "--lc-radius",
  shadow: "--lc-shadow",
};

/** CSS custom properties derived from the layout settings. */
export function layoutVariables(layout: LiveChatLayout): Record<string, string> {
  return {
    "--lc-offset-x": `${layout.offsetX}px`,
    "--lc-offset-y": `${layout.offsetY}px`,
    "--lc-width": `${layout.width}px`,
    "--lc-height": `${layout.height}px`,
    "--lc-z-index": String(layout.zIndex),
  };
}

// Scoped to the element's shadow root: it does not affect the host page, and
// the host page's CSS does not affect it. Hosts can still style named parts
// with ::part().
export const STYLES = `
:host {
  all: initial;
  font-family: var(--lc-font-family);
  font-size: var(--lc-font-size);
  color: var(--lc-text);
}
[hidden] { display: none !important; }
* { box-sizing: border-box; }

.launcher, .panel {
  position: fixed;
  bottom: var(--lc-offset-y);
  z-index: var(--lc-z-index);
}
:host([data-position="bottom-right"]) .launcher,
:host([data-position="bottom-right"]) .panel { right: var(--lc-offset-x); }
:host([data-position="bottom-left"]) .launcher,
:host([data-position="bottom-left"]) .panel { left: var(--lc-offset-x); }

.launcher {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 52px;
  padding: 0 20px 0 16px;
  border: none;
  border-radius: 26px;
  background: var(--lc-primary);
  color: var(--lc-on-primary);
  font: inherit;
  font-weight: 600;
  box-shadow: var(--lc-shadow);
  cursor: pointer;
}
.launcher svg { width: 22px; height: 22px; }
.badge {
  position: absolute;
  top: -6px;
  right: -6px;
  min-width: 22px;
  height: 22px;
  padding: 0 6px;
  border-radius: 11px;
  border: 2px solid var(--lc-background);
  background: var(--lc-error);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  line-height: 18px;
  text-align: center;
}

.panel {
  display: flex;
  flex-direction: column;
  width: min(var(--lc-width), calc(100vw - 2 * var(--lc-offset-x)));
  height: min(var(--lc-height), calc(100vh - 2 * var(--lc-offset-y)));
  background: var(--lc-background);
  border: 1px solid var(--lc-border);
  border-radius: var(--lc-radius);
  box-shadow: var(--lc-shadow);
  overflow: hidden;
}
:host([data-launcher="true"]) .panel {
  bottom: calc(var(--lc-offset-y) + 64px);
  height: min(var(--lc-height), calc(100vh - 2 * var(--lc-offset-y) - 64px));
}

.header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px;
  background: var(--lc-primary);
  color: var(--lc-on-primary);
}
.title { margin: 0; font-size: 1.15em; font-weight: 600; }
.subtitle { margin: 2px 0 0; font-size: 0.9em; opacity: 0.9; }
.close {
  border: none;
  background: transparent;
  color: inherit;
  font-size: 22px;
  line-height: 1;
  cursor: pointer;
  padding: 0 4px;
}

.status {
  padding: 8px 16px;
  border-bottom: 1px solid var(--lc-border);
  color: var(--lc-muted);
  font-size: 0.9em;
}
.status {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 37px;
}
.status[data-phase="connected"] { color: var(--lc-text); font-weight: 600; }
/* A dot before the status: blue for the assistant, amber while waiting. */
.status[data-phase="assistant"]::before,
.status[data-phase="queued"]::before,
.status[data-phase="starting"]::before {
  content: "";
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--lc-primary);
}
.status[data-phase="queued"]::before,
.status[data-phase="starting"]::before { background: #e0a100; }
.avatar {
  position: relative;
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--lc-primary);
  color: var(--lc-on-primary);
  font-size: 11px;
  font-weight: 700;
}
.avatar::before { content: attr(data-initials); }
/* Green "connected" dot on the avatar. */
.avatar::after {
  content: "";
  position: absolute;
  right: -1px;
  bottom: -1px;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #2e9d4f;
  border: 2px solid var(--lc-background);
}

.messages {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.intro, .notice { color: var(--lc-muted); margin: 0; line-height: 1.45; }
.notice { font-style: italic; }
.message {
  max-width: 80%;
  padding: 8px 12px;
  border-radius: var(--lc-radius);
  line-height: 1.45;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.message[data-from="customer"] {
  align-self: flex-end;
  background: var(--lc-primary);
  color: var(--lc-on-primary);
}
.message[data-from="engineer"] {
  align-self: flex-start;
  background: var(--lc-surface);
  color: var(--lc-text);
}
.message[data-from="assistant"] {
  align-self: flex-start;
  background: var(--lc-surface);
  color: var(--lc-text);
  white-space: normal;
}
.message[data-from="assistant"] > :first-child { margin-top: 0; }
.message[data-from="assistant"] > :last-child { margin-bottom: 0; }
.message[data-from="assistant"] p, .message[data-from="assistant"] ul,
.message[data-from="assistant"] ol, .message[data-from="assistant"] pre { margin: 0 0 8px; }
.message[data-from="assistant"] ul, .message[data-from="assistant"] ol { padding-left: 20px; }
.message[data-from="assistant"] code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.92em;
  background: color-mix(in srgb, var(--lc-text) 8%, transparent);
  border-radius: 4px;
  padding: 1px 4px;
}
.message[data-from="assistant"] pre {
  overflow-x: auto;
  padding: 8px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--lc-text) 8%, transparent);
}
.message[data-from="assistant"] pre code { background: none; padding: 0; }
.message[data-from="assistant"] a { color: var(--lc-primary); }
.thinking { margin: 0; color: var(--lc-muted); font-style: italic; font-size: 0.9em; }
.row {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  max-width: 80%;
  align-self: flex-start;
}
.row[data-from="customer"] { align-self: flex-end; align-items: flex-end; }
.row .message { max-width: 100%; }
.time { margin-top: 3px; padding: 0 4px; color: var(--lc-muted); font-size: 0.75em; }
.thinking { display: flex; align-items: center; gap: 8px; }
.dots { display: inline-flex; gap: 3px; }
.dots i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--lc-muted);
  opacity: 0.6;
}
.suggestions { display: flex; flex-wrap: wrap; gap: 8px; }
.suggestions-title { flex-basis: 100%; margin: 4px 0 0; color: var(--lc-muted); font-size: 0.85em; }
.suggestion {
  padding: 7px 12px;
  border: 1px solid var(--lc-border);
  border-radius: 16px;
  background: var(--lc-background);
  color: var(--lc-text);
  font: inherit;
  font-size: 0.92em;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}
.suggestion:hover, .suggestion:focus-visible {
  border-color: var(--lc-primary);
  background: color-mix(in srgb, var(--lc-primary) 8%, var(--lc-background));
}
.suggestion:focus-visible { outline: 2px solid var(--lc-primary); outline-offset: 1px; }
.message[data-from="system"] {
  align-self: center;
  max-width: 100%;
  padding: 2px 0;
  background: transparent;
  color: var(--lc-muted);
  font-style: italic;
  font-size: 0.9em;
  text-align: center;
}

.error {
  margin: 0 16px 8px;
  padding: 8px 10px;
  border-radius: 8px;
  color: var(--lc-error);
  background: color-mix(in srgb, var(--lc-error) 10%, transparent);
  font-size: 0.9em;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.composer {
  display: flex;
  gap: 8px;
  padding: 12px 16px 8px;
  border-top: 1px solid var(--lc-border);
}
.input {
  flex: 1;
  min-height: 40px;
  max-height: 120px;
  resize: none;
  padding: 9px 12px;
  border: 1px solid var(--lc-border);
  border-radius: 8px;
  background: var(--lc-surface);
  color: var(--lc-text);
  font: inherit;
}
.input:focus { outline: 2px solid var(--lc-primary); outline-offset: -1px; }

.button {
  border: none;
  border-radius: 8px;
  padding: 0 14px;
  min-height: 40px;
  background: var(--lc-primary);
  color: var(--lc-on-primary);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.button:disabled { opacity: 0.5; cursor: not-allowed; }
.button.secondary {
  background: transparent;
  color: var(--lc-text);
  border: 1px solid var(--lc-border);
}
.button.link {
  min-height: auto;
  padding: 2px 6px;
  background: transparent;
  color: inherit;
  text-decoration: underline;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 0 16px 12px;
}
.actions:empty { display: none; }

/* Motion, for hosts with animations on and viewers who have not asked the
   system to reduce it. */
@media (prefers-reduced-motion: no-preference) {
  :host([data-animate="true"]) .panel:not([hidden]) { animation: lc-panel-in 0.2s ease-out; }
  :host([data-animate="true"]) .enter { animation: lc-message-in 0.22s ease-out; }
  :host([data-animate="true"]) .badge:not([hidden]) { animation: lc-pop 0.25s ease-out; }
  :host([data-animate="true"]) .dots i { animation: lc-bounce 1.2s infinite ease-in-out; }
  :host([data-animate="true"]) .dots i:nth-child(2) { animation-delay: 0.15s; }
  :host([data-animate="true"]) .dots i:nth-child(3) { animation-delay: 0.3s; }
  :host([data-animate="true"]) .status[data-phase="queued"]::before { animation: lc-pulse 1.4s infinite ease-in-out; }
  :host([data-animate="true"]) .launcher { transition: transform 0.15s; }
  :host([data-animate="true"]) .launcher:hover { transform: translateY(-2px); }
}
@keyframes lc-panel-in {
  from { opacity: 0; transform: translateY(12px) scale(0.98); }
  to { opacity: 1; transform: none; }
}
@keyframes lc-message-in {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: none; }
}
@keyframes lc-pop {
  0% { transform: scale(0.4); }
  70% { transform: scale(1.15); }
  100% { transform: scale(1); }
}
@keyframes lc-bounce {
  0%, 80%, 100% { transform: translateY(0); opacity: 0.4; }
  40% { transform: translateY(-4px); opacity: 1; }
}
@keyframes lc-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
`;
