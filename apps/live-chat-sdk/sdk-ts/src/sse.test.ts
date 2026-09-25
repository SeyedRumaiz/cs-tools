import { afterEach, describe, expect, it, vi } from "vitest";
import { extractSseData, extractSseFrames, openEventStream } from "./sse.js";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("extractSseFrames", () => {
  it("extracts one complete frame and leaves no rest", () => {
    const { frames, rest } = extractSseFrames('data: {"a":1}\n\n');
    expect(frames).toEqual(['data: {"a":1}']);
    expect(rest).toBe("");
  });

  it("leaves an incomplete frame in rest", () => {
    const { frames, rest } = extractSseFrames('data: {"a":1');
    expect(frames).toEqual([]);
    expect(rest).toBe('data: {"a":1');
  });

  it("extracts multiple frames from one buffer", () => {
    const { frames, rest } = extractSseFrames('data: {"a":1}\n\ndata: {"a":2}\n\n');
    expect(frames).toEqual(['data: {"a":1}', 'data: {"a":2}']);
    expect(rest).toBe("");
  });

  it("normalizes \\r\\n line endings before splitting", () => {
    const { frames } = extractSseFrames('data: {"a":1}\r\n\r\n');
    expect(frames).toEqual(['data: {"a":1}']);
  });
});

describe("extractSseData", () => {
  it("extracts the data: line's payload", () => {
    expect(extractSseData('data: {"a":1}')).toBe('{"a":1}');
  });

  it("trims a space after the colon", () => {
    expect(extractSseData("data:{\"a\":1}")).toBe('{"a":1}');
  });

  it("returns null for a comment-only (heartbeat) frame", () => {
    expect(extractSseData(": heartbeat")).toBeNull();
  });

  it("returns null for an empty frame", () => {
    expect(extractSseData("")).toBeNull();
  });
});

// ---- openEventStream: fetch + ReadableStream behavior ----

function controlledStream(onCancel?: () => void): {
  response: Response;
  push: (chunk: string) => void;
  close: () => void;
} {
  let ctrl!: ReadableStreamDefaultController<Uint8Array>;
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      ctrl = c;
    },
    cancel() {
      onCancel?.();
    }
  });
  return {
    response: new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream" } }),
    push: (chunk: string) => ctrl.enqueue(encoder.encode(chunk)),
    close: () => ctrl.close()
  };
}

async function flush(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe("openEventStream", () => {
  it("delivers one complete frame received in a single chunk", async () => {
    const { response, push } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    push('data: {"type":"engineer_assigned","engineerEmail":"eng@example.com"}\n\n');
    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(1));
    expect(onEvent).toHaveBeenCalledWith({ type: "assigned", engineerEmail: "eng@example.com" });

    unsubscribe();
  });

  it("assembles one frame fragmented across multiple chunks", async () => {
    const { response, push } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    push('data: {"type":"engineer_mess');
    await flush();
    push('age","engineerEmail":"eng@example.com","message":"hi"}\n');
    await flush();
    push("\n");

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(1));
    expect(onEvent).toHaveBeenCalledWith({ type: "message", content: "hi", engineerEmail: "eng@example.com" });

    unsubscribe();
  });

  it("delivers multiple frames arriving in a single chunk", async () => {
    const { response, push } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    push(
      'data: {"type":"engineer_assigned","engineerEmail":"eng@example.com"}\n\n' +
        'data: {"type":"engineer_message","engineerEmail":"eng@example.com","message":"hi"}\n\n'
    );

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(2));
    expect(onEvent).toHaveBeenNthCalledWith(1, { type: "assigned", engineerEmail: "eng@example.com" });
    expect(onEvent).toHaveBeenNthCalledWith(2, {
      type: "message",
      content: "hi",
      engineerEmail: "eng@example.com"
    });

    unsubscribe();
  });

  it("skips a heartbeat/comment frame and keeps delivering real events", async () => {
    const { response, push } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    push(': heartbeat\n\ndata: {"type":"queued","message":"you are next"}\n\n');

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(1));
    expect(onEvent).toHaveBeenCalledWith({ type: "queued", message: "you are next" });

    unsubscribe();
  });

  it("skips a frame with malformed JSON and keeps the stream alive", async () => {
    const { response, push } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    push('data: {not valid json\n\ndata: {"type":"queued","message":"still works"}\n\n');

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(1));
    expect(onEvent).toHaveBeenCalledWith({ type: "queued", message: "still works" });

    unsubscribe();
  });

  it("silently ignores an unknown wire event type instead of surfacing an error", async () => {
    const { response, push } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    push('data: {"type":"some_future_event"}\n\ndata: {"type":"queued","message":"ok"}\n\n');

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(1));
    expect(onEvent).toHaveBeenCalledWith({ type: "queued", message: "ok" });

    unsubscribe();
  });

  it("surfaces a controlled error event when the stream ends unexpectedly", async () => {
    const { response, close } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const onEvent = vi.fn();
    openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    close();

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledWith({ type: "error", message: expect.any(String) }));
  });

  it("surfaces a controlled error event for a non-2xx response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ message: "This chat does not belong to this tenant." }), { status: 403 }))
    );

    const onEvent = vi.fn();
    openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);

    await vi.waitFor(() =>
      expect(onEvent).toHaveBeenCalledWith({ type: "error", message: "This chat does not belong to this tenant." })
    );
  });

  it("unsubscribe aborts the stream and stops further delivery", async () => {
    let cancelled = false;
    const { response, push } = controlledStream(() => {
      cancelled = true;
    });
    const fetchMock = vi.fn().mockResolvedValue(response);
    vi.stubGlobal("fetch", fetchMock);

    const onEvent = vi.fn();
    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, onEvent);
    await flush();

    unsubscribe();
    await vi.waitFor(() => expect(cancelled).toBe(true));

    const signal = (fetchMock.mock.calls[0] as [string, RequestInit])[1].signal as AbortSignal;
    expect(signal.aborted).toBe(true);

    onEvent.mockClear();
    try {
      push('data: {"type":"queued","message":"too late"}\n\n');
    } catch {
      // the stream may already be closed/locked after cancel -- either way,
      // no event should ever be delivered from it
    }
    await flush();
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("calling unsubscribe twice is safe", async () => {
    const { response } = controlledStream();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));

    const unsubscribe = openEventStream({ url: "https://x.test/events", getAccessToken: () => "t" }, vi.fn());
    await flush();

    expect(() => {
      unsubscribe();
      unsubscribe();
    }).not.toThrow();
  });
});
