import { errorFromResponse, LiveChatError } from "./errors.js";
import { openEventStream } from "./sse.js";
import type {
  ChatMessage,
  LiveChatClient,
  LiveChatClientConfig,
  LiveChatEvent,
  StartChatRequest,
  StartChatResult
} from "./types.js";

function requireNonEmpty(value: string | undefined, field: string): string {
  if (!value || value.trim() === "") {
    throw new LiveChatError(`${field} is required.`);
  }
  return value;
}

/** Base "/v1/{tenant}/chats" URL for config. Trims a trailing slash from
 * baseUrl so "https://x.test/" and "https://x.test" behave identically. */
function chatsUrl(config: LiveChatClientConfig): string {
  const base = config.baseUrl.replace(/\/+$/, "");
  return `${base}/v1/${encodeURIComponent(config.tenant)}/chats`;
}

function caseUrl(config: LiveChatClientConfig, caseId: string, suffix: string): string {
  return `${chatsUrl(config)}/${encodeURIComponent(caseId)}${suffix}`;
}

/** Shared POST-JSON request plumbing for startChat/sendMessage/completeChat.
 * Uses config.requestTransport when given (see that field's own doc
 * comment); otherwise fetches a fresh access token for every call -- see
 * LiveChatClientConfig.getAccessToken's own doc comment on why this SDK
 * never caches one. Never logs the token or includes it in any thrown
 * error. */
async function postJson(config: LiveChatClientConfig, url: string, body: unknown): Promise<unknown> {
  let status: number;
  let rawBody: string;

  if (config.requestTransport) {
    try {
      ({ status, text: rawBody } = await config.requestTransport.postJson(url, body));
    } catch (err) {
      throw new LiveChatError(err instanceof Error && err.message ? err.message : "A network error occurred.");
    }
  } else {
    if (typeof config.getAccessToken !== "function") {
      throw new LiveChatError("getAccessToken or requestTransport is required.");
    }
    const token = await config.getAccessToken();

    let response: Response;
    try {
      response = await fetch(url, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(body)
      });
    } catch (err) {
      throw new LiveChatError(err instanceof Error && err.message ? err.message : "A network error occurred.");
    }
    status = response.status;
    rawBody = await response.text();
  }

  if (status < 200 || status >= 300) {
    throw errorFromResponse(status, rawBody);
  }
  if (rawBody.trim() === "") {
    return undefined;
  }
  try {
    return JSON.parse(rawBody) as unknown;
  } catch {
    throw new LiveChatError("The server returned a malformed response.", { status });
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Validates and narrows startChat's raw JSON response -- never a blind
 * cast, since this is untrusted network input regardless of what the
 * backend's own contract promises. */
function parseStartChatResult(raw: unknown): StartChatResult {
  if (
    isRecord(raw) &&
    typeof raw.caseId === "string" &&
    raw.caseId.length > 0 &&
    typeof raw.conversationId === "string"
  ) {
    return { caseId: raw.caseId, conversationId: raw.conversationId };
  }
  throw new LiveChatError("The server returned an unexpected response starting the chat.");
}

function toWirePriorMessages(messages: ChatMessage[] | undefined): ChatMessage[] | undefined {
  return messages?.map((m) => ({ role: m.role, content: m.content, createdAt: m.createdAt }));
}

async function startChat(config: LiveChatClientConfig, req: StartChatRequest): Promise<StartChatResult> {
  requireNonEmpty(req.message, "message");

  const body = {
    conversationId: req.conversationId,
    subject: req.subject,
    message: req.message,
    customerName: req.customerName,
    priorMessages: toWirePriorMessages(req.priorMessages),
    metadata: req.metadata
  };
  const raw = await postJson(config, chatsUrl(config), body);
  return parseStartChatResult(raw);
}

function subscribe(config: LiveChatClientConfig, caseId: string, onEvent: (event: LiveChatEvent) => void): () => void {
  requireNonEmpty(caseId, "caseId");
  if (!config.streamTransport && typeof config.getAccessToken !== "function") {
    throw new LiveChatError("getAccessToken or streamTransport is required.");
  }
  return openEventStream(
    {
      url: caseUrl(config, caseId, "/events"),
      getAccessToken: config.getAccessToken,
      streamTransport: config.streamTransport
    },
    onEvent
  );
}

async function sendMessage(config: LiveChatClientConfig, caseId: string, message: { content: string }): Promise<void> {
  requireNonEmpty(caseId, "caseId");
  requireNonEmpty(message?.content, "message.content");
  await postJson(config, caseUrl(config, caseId, "/messages"), { message: message.content });
}

async function completeChat(config: LiveChatClientConfig, caseId: string): Promise<void> {
  requireNonEmpty(caseId, "caseId");
  await postJson(config, caseUrl(config, caseId, "/complete"), {});
}

/** Creates a {@link LiveChatClient} bound to config. Validates baseUrl/tenant
 * once, up front -- every other input is validated per call (see each
 * method's own requireNonEmpty checks), since getAccessToken/
 * requestTransport/streamTransport are each only required by the calls
 * that actually need them (see subscribe's own check for the streaming
 * case, and postJson's for the request case) -- a consumer that only ever
 * calls startChat/sendMessage/completeChat need not supply a
 * streamTransport, and vice versa. */
export function createLiveChatClient(config: LiveChatClientConfig): LiveChatClient {
  requireNonEmpty(config.baseUrl, "baseUrl");
  requireNonEmpty(config.tenant, "tenant");
  if (
    typeof config.getAccessToken !== "function" &&
    !config.requestTransport &&
    !config.streamTransport
  ) {
    throw new LiveChatError("getAccessToken, requestTransport, or streamTransport is required.");
  }

  return {
    startChat: (req) => startChat(config, req),
    subscribe: (caseId, onEvent) => subscribe(config, caseId, onEvent),
    sendMessage: (caseId, message) => sendMessage(config, caseId, message),
    completeChat: (caseId) => completeChat(config, caseId)
  };
}
