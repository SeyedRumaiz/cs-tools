import { describeTransportError, isAbortError, parseErrorBody } from "./errors.js";
import { normalizeWireEvent } from "./events.js";
import type { LiveChatEvent, LiveChatStreamTransport } from "./types.js";

/** One SSE frame is a "\n\n"-terminated block; extractSseFrames splits a
 * growing buffer into every complete frame plus whatever incomplete tail
 * remains (to be prefixed onto the next chunk). Normalizes "\r\n" to "\n"
 * first for leniency against an intermediary that rewrites line endings --
 * console-chat-bridge itself only ever emits "\n" (see
 * HandleStreamV1/HandleStream's `fmt.Fprintf(w, "data: %s\n\n", payload)`). */
export function extractSseFrames(buffer: string): { frames: string[]; rest: string } {
  const normalized = buffer.replace(/\r\n/g, "\n");
  const parts = normalized.split("\n\n");
  const rest = parts.pop() ?? "";
  return { frames: parts, rest };
}

/** Extracts one frame's "data:" payload, or null for a frame with none --
 * console-chat-bridge's own ": heartbeat" comment frames, or a stray blank
 * frame from consecutive separators. Only the first "data:" line in a
 * frame is read; this backend never emits more than one per frame. */
export function extractSseData(frame: string): string | null {
  for (const rawLine of frame.split("\n")) {
    const line = rawLine.trim();
    if (line.startsWith("data:")) {
      return line.slice("data:".length).trim();
    }
  }
  return null;
}

export interface EventStreamTarget {
  url: string;
  /** Required unless streamTransport is given -- see that field. */
  getAccessToken?: () => Promise<string> | string;
  /** See LiveChatStreamTransport's own doc comment (types.ts). When
   * given, used instead of this module's own fetch()+getAccessToken()
   * connection open. */
  streamTransport?: LiveChatStreamTransport;
}

/**
 * Opens GET {target.url} as an SSE stream (fetch + ReadableStream, not
 * native EventSource -- EventSource cannot send an Authorization header,
 * and this API requires a bearer token on every request, including the
 * stream itself) and delivers every normalized event to onEvent until the
 * returned function is called.
 *
 * Calling the returned function aborts the underlying fetch/reader
 * immediately: no further onEvent calls happen afterward, and no reader or
 * connection is left open. Safe to call before the connection has even
 * finished opening (the abort races the in-flight fetch and wins).
 */
export function openEventStream(target: EventStreamTarget, onEvent: (event: LiveChatEvent) => void): () => void {
  const controller = new AbortController();
  let closed = false;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;

  const close = (): void => {
    if (closed) return;
    closed = true;
    controller.abort();
    void reader?.cancel().catch(() => {
      /* stream already closed/errored -- nothing further to release */
    });
  };

  void run();

  async function run(): Promise<void> {
    let body: ReadableStream<Uint8Array>;

    if (target.streamTransport) {
      try {
        body = await target.streamTransport.openStream(target.url, controller.signal);
      } catch (err) {
        reportUnlessClosed(err);
        return;
      }
    } else {
      let response: Response;
      try {
        const token = await target.getAccessToken?.();
        response = await fetch(target.url, {
          method: "GET",
          headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
          signal: controller.signal
        });
      } catch (err) {
        reportUnlessClosed(err);
        return;
      }

      if (!response.ok) {
        const rawBody = await response.text().catch(() => "");
        const { message } = parseErrorBody(rawBody, `Failed to open the event stream (status ${response.status}).`);
        onEvent({ type: "error", message });
        return;
      }
      if (!response.body) {
        onEvent({ type: "error", message: "The server did not return a readable stream." });
        return;
      }
      body = response.body;
    }

    reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    try {
      for (;;) {
        const result = await reader.read();
        if (result.done) {
          if (!closed) {
            onEvent({ type: "error", message: "The connection to the server was closed." });
          }
          return;
        }

        buffer += decoder.decode(result.value, { stream: true });
        const { frames, rest } = extractSseFrames(buffer);
        buffer = rest;

        for (const frame of frames) {
          const dataStr = extractSseData(frame);
          if (dataStr === null) continue;

          let parsed: unknown;
          try {
            parsed = JSON.parse(dataStr);
          } catch {
            continue; // malformed JSON -- skip this one frame, not the stream
          }

          const event = normalizeWireEvent(parsed);
          if (event) onEvent(event);
        }
      }
    } catch (err) {
      reportUnlessClosed(err);
    }
  }

  function reportUnlessClosed(err: unknown): void {
    if (closed || isAbortError(err)) return;
    onEvent({ type: "error", message: describeTransportError(err) });
  }

  return close;
}
