import { afterEach, describe, expect, it, vi } from "vitest";
import { createLiveChatClient } from "./client.js";
import { LiveChatError } from "./errors.js";
import type { LiveChatClientConfig } from "./types.js";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" }
  });
}

function baseConfig(overrides?: Partial<LiveChatClientConfig>): LiveChatClientConfig {
  return {
    baseUrl: "https://support.example.com",
    tenant: "my-product",
    getAccessToken: () => "test-token",
    ...overrides
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("startChat", () => {
  it("sends the correct URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { caseId: "c1", conversationId: "conv1" }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig());
    await client.startChat({ message: "hi" });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://support.example.com/v1/my-product/chats");
  });

  it("sends the correct request body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { caseId: "c1", conversationId: "conv1" }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig());
    await client.startChat({
      message: "hello",
      subject: "help",
      conversationId: "conv-abc",
      customerName: "Jane",
      priorMessages: [{ role: "customer", content: "hi" }],
      metadata: { plan: "enterprise" }
    });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({
      conversationId: "conv-abc",
      subject: "help",
      message: "hello",
      customerName: "Jane",
      priorMessages: [{ role: "customer", content: "hi", createdAt: undefined }],
      metadata: { plan: "enterprise" }
    });
  });

  it("sends the bearer token", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { caseId: "c1", conversationId: "conv1" }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig({ getAccessToken: () => "secret-token-value" }));
    await client.startChat({ message: "hi" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer secret-token-value");
    expect(headers["Content-Type"]).toBe("application/json");
  });

  it("resolves an async getAccessToken", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { caseId: "c1", conversationId: "conv1" }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(
      baseConfig({ getAccessToken: () => Promise.resolve("async-token") })
    );
    await client.startChat({ message: "hi" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer async-token");
  });

  it("parses the returned caseId and conversationId", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse(202, { caseId: "case-123", conversationId: "conv-456" }))
    );

    const client = createLiveChatClient(baseConfig());
    const result = await client.startChat({ message: "hi" });

    expect(result).toEqual({ caseId: "case-123", conversationId: "conv-456" });
  });

  it("rejects an empty message before ever calling fetch", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig());
    await expect(client.startChat({ message: "" })).rejects.toBeInstanceOf(LiveChatError);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("throws LiveChatError with the status and server message on a non-2xx response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse(409, { message: "You already have a live chat in progress." }))
    );

    const client = createLiveChatClient(baseConfig());
    const err = await client.startChat({ message: "hi" }).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(LiveChatError);
    expect((err as LiveChatError).status).toBe(409);
    expect((err as LiveChatError).message).toBe("You already have a live chat in progress.");
  });

  it("falls back to a safe message when the error body isn't JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("<html>gateway error</html>", { status: 502 }))
    );

    const client = createLiveChatClient(baseConfig());
    const err = await client.startChat({ message: "hi" }).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(LiveChatError);
    expect((err as LiveChatError).status).toBe(502);
    expect((err as LiveChatError).message).not.toContain("<html>");
  });

  it("throws LiveChatError when a 2xx response body is malformed JSON", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("not json", { status: 202 })));

    const client = createLiveChatClient(baseConfig());
    await expect(client.startChat({ message: "hi" })).rejects.toBeInstanceOf(LiveChatError);
  });

  it("throws LiveChatError when a 2xx response is missing caseId", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(202, { conversationId: "c1" })));

    const client = createLiveChatClient(baseConfig());
    await expect(client.startChat({ message: "hi" })).rejects.toBeInstanceOf(LiveChatError);
  });
});

describe("sendMessage", () => {
  it("sends the correct URL, body, and auth", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig());
    await client.sendMessage("case-1", { content: "hello engineer" });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://support.example.com/v1/my-product/chats/case-1/messages");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ message: "hello engineer" });
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer test-token");
  });

  it("rejects an empty caseId before calling fetch", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const client = createLiveChatClient(baseConfig());
    await expect(client.sendMessage("", { content: "hi" })).rejects.toBeInstanceOf(LiveChatError);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects empty message content before calling fetch", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const client = createLiveChatClient(baseConfig());
    await expect(client.sendMessage("case-1", { content: "" })).rejects.toBeInstanceOf(LiveChatError);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("completeChat", () => {
  it("sends the correct URL and auth", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig());
    await client.completeChat("case-1");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://support.example.com/v1/my-product/chats/case-1/complete");
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer test-token");
  });
});

describe("URL encoding", () => {
  it("URL-encodes the tenant segment", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { caseId: "c1", conversationId: "c1" }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig({ tenant: "acme corp/beta" }));
    await client.startChat({ message: "hi" });

    const [url] = fetchMock.mock.calls[0] as [string];
    expect(url).toBe("https://support.example.com/v1/acme%20corp%2Fbeta/chats");
  });

  it("URL-encodes the caseId segment", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const client = createLiveChatClient(baseConfig());
    await client.completeChat("case/with slash");

    const [url] = fetchMock.mock.calls[0] as [string];
    expect(url).toBe("https://support.example.com/v1/my-product/chats/case%2Fwith%20slash/complete");
  });
});

describe("createLiveChatClient config validation", () => {
  it("throws synchronously for an empty baseUrl", () => {
    expect(() => createLiveChatClient(baseConfig({ baseUrl: "" }))).toThrow(LiveChatError);
  });

  it("throws synchronously for an empty tenant", () => {
    expect(() => createLiveChatClient(baseConfig({ tenant: "" }))).toThrow(LiveChatError);
  });

  it("throws when none of getAccessToken/requestTransport/streamTransport are given", () => {
    expect(() =>
      createLiveChatClient({
        baseUrl: "https://support.example.com",
        tenant: "my-product"
      })
    ).toThrow(LiveChatError);
  });

  it("does not throw when only requestTransport is given (no getAccessToken)", () => {
    expect(() =>
      createLiveChatClient({
        baseUrl: "https://support.example.com",
        tenant: "my-product",
        requestTransport: { postJson: async () => ({ status: 202, text: "{}" }) }
      })
    ).not.toThrow();
  });

  it("does not throw when only streamTransport is given (no getAccessToken)", () => {
    expect(() =>
      createLiveChatClient({
        baseUrl: "https://support.example.com",
        tenant: "my-product",
        streamTransport: { openStream: async () => new ReadableStream() }
      })
    ).not.toThrow();
  });
});

describe("requestTransport", () => {
  it("uses requestTransport instead of fetch when provided", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const postJson = vi.fn().mockResolvedValue({
      status: 202,
      text: JSON.stringify({ caseId: "c1", conversationId: "conv1" })
    });

    const client = createLiveChatClient(
      baseConfig({ getAccessToken: undefined, requestTransport: { postJson } })
    );
    const result = await client.startChat({ message: "hi" });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(postJson).toHaveBeenCalledTimes(1);
    const [url, body] = postJson.mock.calls[0] as [string, unknown];
    expect(url).toBe("https://support.example.com/v1/my-product/chats");
    expect(body).toMatchObject({ message: "hi" });
    expect(result).toEqual({ caseId: "c1", conversationId: "conv1" });
  });

  it("turns a non-2xx requestTransport status into a LiveChatError", async () => {
    const postJson = vi.fn().mockResolvedValue({
      status: 409,
      text: JSON.stringify({ message: "You already have an open chat." })
    });

    const client = createLiveChatClient(
      baseConfig({ getAccessToken: undefined, requestTransport: { postJson } })
    );

    await expect(client.startChat({ message: "hi" })).rejects.toMatchObject({
      status: 409,
      message: "You already have an open chat."
    });
  });

  it("wraps a rejected requestTransport call in a LiveChatError", async () => {
    const postJson = vi.fn().mockRejectedValue(new Error("no network"));

    const client = createLiveChatClient(
      baseConfig({ getAccessToken: undefined, requestTransport: { postJson } })
    );

    await expect(client.startChat({ message: "hi" })).rejects.toThrow(LiveChatError);
  });
});
