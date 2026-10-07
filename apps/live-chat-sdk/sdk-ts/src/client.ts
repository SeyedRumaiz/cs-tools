import { errorFromResponse, LiveChatError } from "./errors.js";
import { openEventStream } from "./sse.js";
import type {
  ChatMessage,
  LiveChatClient,
  LiveChatClientConfig,
  LiveChatCurrentChat,
  LiveChatEvent,
  LiveChatHistoryMessage,
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
 * comment); otherwise fetches a fresh access token for every call — see
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

/** GET counterpart to {@link postJson} — same transport selection and
 * error-shape contract, just no request body. */
async function getJson(config: LiveChatClientConfig, url: string): Promise<unknown> {
  let status: number;
  let rawBody: string;

  if (config.requestTransport) {
    if (typeof config.requestTransport.getJson !== "function") {
      throw new LiveChatError("requestTransport.getJson is required to call getHistory.");
    }
    try {
      ({ status, text: rawBody } = await config.requestTransport.getJson(url));
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
        method: "GET",
        headers: { Authorization: `Bearer ${token}` }
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

/** Validates and narrows startChat's raw JSON response — never a blind
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

/** Validates and narrows getHistory's raw JSON response — never a blind
 * cast, same reasoning as {@link parseStartChatResult}. An entry with an
 * unrecognized role or a missing content string is dropped rather than
 * failing the whole call — one malformed row shouldn't blank out an
 * otherwise-good transcript. */
function parseHistoryMessages(raw: unknown): LiveChatHistoryMessage[] {
  if (!isRecord(raw) || !Array.isArray(raw.messages)) {
    throw new LiveChatError("The server returned an unexpected response loading the conversation history.");
  }
  const messages: LiveChatHistoryMessage[] = [];

  for (const entry of raw.messages) {
    if (
      isRecord(entry) &&
      (entry.role === "customer" || entry.role === "assistant" || entry.role === "engineer") &&
      typeof entry.content === "string"
    ) {
      messages.push({
        role: entry.role,
        content: entry.content,
        createdAt: typeof entry.createdAt === "string" ? entry.createdAt : undefined
      });
    }
  }
  return messages;
}

async function getHistory(config: LiveChatClientConfig, caseId: string): Promise<LiveChatHistoryMessage[]> {
  requireNonEmpty(caseId, "caseId");
  const raw = await getJson(config, caseUrl(config, caseId, "/history"));
  return parseHistoryMessages(raw);
}

function parseCurrentChat(raw: unknown): LiveChatCurrentChat {
  if (
    !isRecord(raw) ||
    typeof raw.caseId !== "string" ||
    raw.caseId === "" ||
    typeof raw.conversationId !== "string" ||
    (raw.status !== "waiting" && raw.status !== "connected")
  ) {
    throw new LiveChatError("The server returned an unexpected response looking up the open chat.");
  }
  return {
    caseId: raw.caseId,
    conversationId: raw.conversationId,
    status: raw.status,
    engineerEmail: typeof raw.engineerEmail === "string" && raw.engineerEmail !== "" ? raw.engineerEmail : undefined
  };
}

async function getCurrentChat(config: LiveChatClientConfig): Promise<LiveChatCurrentChat | null> {
  try {
    return parseCurrentChat(await getJson(config, `${chatsUrl(config)}/current`));
  } catch (err) {
    // Only the bridge's own JSON 404 means "no open chat"; a plain-text 404
    // means a bridge too old to have this route, which must not read as one.
    if (err instanceof LiveChatError && err.status === 404 && err.details !== undefined) {
      return null;
    }
    throw err;
  }
}

/** Creates a {@link LiveChatClient} bound to config. Validates baseUrl/tenant
 * once, up front. getAccessToken/requestTransport/streamTransport are
 * validated lazily instead, by the calls that actually need them — a
 * consumer that only calls startChat/sendMessage/completeChat need not
 * supply a streamTransport, and vice versa. */
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
    completeChat: (caseId) => completeChat(config, caseId),
    getHistory: (caseId) => getHistory(config, caseId),
    getCurrentChat: () => getCurrentChat(config)
  };
}
