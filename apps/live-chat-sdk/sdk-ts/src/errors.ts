/**
 * The error type every rejected {@link LiveChatClient} promise throws.
 * Consumers should never need to parse an English message to branch on
 * what went wrong -- check `status` instead (e.g. 403 for a cross-tenant
 * case, 409 for "you already have an open chat", 401 for an expired/
 * invalid token).
 *
 * `code` exists for forward compatibility but is never populated today --
 * the backend's error responses are a plain `{"message": string}` JSON
 * body with no machine-readable code (see console-chat-bridge's
 * writeError/writeTenantError/writeUnauthorized, and csm-portal/backend's
 * own writeError, which every /v1 error response ultimately goes through).
 * This SDK does not invent one; do not rely on `code` being set.
 */
export class LiveChatError extends Error {
  /** HTTP status code, when this error came from an HTTP response. */
  readonly status?: number;
  /** Always undefined today -- see this class's own doc comment. */
  readonly code?: string;
  /** The parsed response body, when the server returned one and it was
   * valid JSON -- for forward-compatible access to any extra fields a
   * future backend response might add. Never contains a token: this SDK
   * never sends one in a body, and the backend's own error responses
   * never echo one back. */
  readonly details?: unknown;

  constructor(message: string, options?: { status?: number; code?: string; details?: unknown }) {
    super(message);
    this.name = "LiveChatError";
    this.status = options?.status;
    this.code = options?.code;
    this.details = options?.details;
  }
}

/** True for a plain JSON object (not null, not an array). */
function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Extracts a safe error message from a response body, falling back to
 * fallback when the body isn't the `{"message": string}` shape the
 * backend always sends on an error (or isn't valid JSON at all -- e.g. an
 * intermediary proxy's own HTML error page). Returns the parsed body too,
 * for LiveChatError.details, when it did parse as JSON.
 */
export function parseErrorBody(rawBody: string, fallback: string): { message: string; details?: unknown } {
  if (rawBody.trim() === "") {
    return { message: fallback };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawBody);
  } catch {
    return { message: fallback };
  }
  if (isPlainObject(parsed) && typeof parsed.message === "string" && parsed.message.trim() !== "") {
    return { message: parsed.message, details: parsed };
  }
  return { message: fallback, details: parsed };
}

/** Builds a LiveChatError for a non-2xx HTTP response. */
export function errorFromResponse(status: number, rawBody: string): LiveChatError {
  const { message, details } = parseErrorBody(rawBody, `Request failed with status ${status}.`);
  return new LiveChatError(message, { status, details });
}

/** True for the DOMException a fetch()/reader.read() raises when its
 * AbortSignal fires -- used to distinguish a deliberate unsubscribe() from
 * a genuine transport failure. */
export function isAbortError(err: unknown): boolean {
  return err instanceof Error && err.name === "AbortError";
}

/** A safe, non-throwing description of a caught transport-level error
 * (a network failure, not an HTTP response) -- never includes the access
 * token, since it only ever reads err.message. */
export function describeTransportError(err: unknown): string {
  if (err instanceof Error && err.message) {
    return err.message;
  }
  return "A network error occurred.";
}
