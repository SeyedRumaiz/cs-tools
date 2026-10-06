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
import { DEFAULT_LAYOUT, DEFAULT_TEXTS, DEFAULT_THEME, format, LiveChatConfigError, resolveConfig } from "./config.js";

describe("resolveConfig", () => {
  it("fills every missing key with its default", () => {
    const c = resolveConfig({ bridgeUrl: "https://bridge.example/", tenant: " my-product " });
    expect(c.bridgeUrl).toBe("https://bridge.example");
    expect(c.tenant).toBe("my-product");
    expect(c.layout).toEqual(DEFAULT_LAYOUT);
    expect(c.theme).toEqual(DEFAULT_THEME);
    expect(c.texts).toEqual(DEFAULT_TEXTS);
  });

  it("keeps a product's overrides and defaults the rest, key by key", () => {
    const c = resolveConfig({
      bridgeUrl: "https://b",
      tenant: "t",
      layout: { position: "bottom-left", width: 420 },
      theme: { primaryColor: "#123456" },
      texts: { title: "Ask an engineer" },
    });
    expect(c.layout.position).toBe("bottom-left");
    expect(c.layout.width).toBe(420);
    expect(c.layout.height).toBe(DEFAULT_LAYOUT.height);
    expect(c.theme.primaryColor).toBe("#123456");
    expect(c.theme.textColor).toBe(DEFAULT_THEME.textColor);
    expect(c.texts.title).toBe("Ask an engineer");
    expect(c.texts.endButton).toBe(DEFAULT_TEXTS.endButton);
  });

  it("ignores unknown keys, wrong types and invalid values instead of failing", () => {
    const c = resolveConfig({
      bridgeUrl: "https://b",
      tenant: "t",
      layout: { position: "top-center", width: "wide", height: -5, unknown: 1 },
      theme: { primaryColor: 42 },
    });
    expect(c.layout.position).toBe(DEFAULT_LAYOUT.position);
    expect(c.layout.width).toBe(DEFAULT_LAYOUT.width);
    expect(c.layout.height).toBe(DEFAULT_LAYOUT.height);
    expect(c.theme.primaryColor).toBe(DEFAULT_THEME.primaryColor);
    expect(c.layout).not.toHaveProperty("unknown");
  });

  it("requires bridgeUrl and tenant", () => {
    expect(() => resolveConfig({ tenant: "t" })).toThrow(LiveChatConfigError);
    expect(() => resolveConfig({ bridgeUrl: "https://b" })).toThrow(LiveChatConfigError);
    expect(() => resolveConfig("not an object")).toThrow(LiveChatConfigError);
  });
});

describe("format", () => {
  it("fills known placeholders and leaves unknown ones", () => {
    expect(format("Hi {engineer}, case {caseId} {other}", { engineer: "a@b.c", caseId: "CS1" })).toBe(
      "Hi a@b.c, case CS1 {other}",
    );
  });
});
