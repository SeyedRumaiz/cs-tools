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
.status[data-phase="connected"] { color: var(--lc-text); font-weight: 600; }

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
`;
