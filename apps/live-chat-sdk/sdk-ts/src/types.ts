/**
 * Public types for @wso2/live-chat-client, mirroring console-chat-bridge's
 * generic `/v1/{tenant}/...` API. Deliberately excluded: source, channel,
 * tenant, tenantSlug, projectId — these are derived server-side from the
 * resolved tenant's own configuration, never client-supplied.
 */

/**
 * One prior message given as context when starting a chat — e.g. an
 * existing AI-chatbot transcript. Never "engineer": a pre-escalation
 * transcript predates any engineer being involved (mirrors
 * router.PriorMessageRole on the backend, minus the read-only "engineer"
 * value that backend adds only when echoing an already-accepted chat's
 * transcript back, which this SDK never does).
 */
export interface ChatMessage {
  role: "customer" | "assistant";
  content: string;
  /** RFC 3339 timestamp. Optional — omitted messages are timestamped by
   * the backend at ingestion time. */
  createdAt?: string;
}

/** Request body for {@link LiveChatClient.startChat}. */
export interface StartChatRequest {
  /** The message that starts the chat. Required — mirrors the backend's
   * own required `message` field. */
  message: string;
  /** A stable per-session id the caller already has (e.g. an existing
   * AI-chatbot conversation this escalates from). Optional — the backend
   * mints one from the new session's caseId when omitted, so caseId and
   * conversationId start out equal. */
  conversationId?: string;
  subject?: string;
  /** Display name only — never used for authorization or attribution.
   * The caller's own identity (from the access token) is what the backend
   * actually attributes the chat to. */
  customerName?: string;
  /** Prior conversation history for context, NOT including `message`
   * itself. */
  priorMessages?: ChatMessage[];
  /** Arbitrary tenant-supplied key/value data. Subject to the backend's
   * own limits (at most 20 keys, 64 bytes/key, 500 bytes/value) and
   * reserved/sensitive-key rejection (e.g. "tenantSlug", any key
   * containing "token"/"secret"/"password"/"credential"/"apikey") — this
   * SDK does not duplicate those checks client-side; a violation surfaces
   * as a 400 {@link LiveChatError} from startChat. */
  metadata?: Record<string, string>;
}

/** Result of {@link LiveChatClient.startChat}. */
export interface StartChatResult {
  /** This session's case id — pass to subscribe/sendMessage/completeChat. */
  caseId: string;
  /** Echoes back the conversation id (client-supplied or backend-minted). */
  conversationId: string;
}

/**
 * One message from {@link LiveChatClient.getHistory} — the full transcript
 * to date for a case. A superset of {@link ChatMessage}: "engineer" is a
 * real value here (the backend echoes back the live, post-acceptance
 * conversation too, not just the pre-escalation transcript a caller
 * supplies to startChat), which is why this is its own type rather than
 * reusing ChatMessage's narrower role union.
 */
/** The caller's open chat, from {@link LiveChatClient.getCurrentChat}. */
export interface LiveChatCurrentChat {
  caseId: string;
  conversationId: string;
  /** "waiting" until an engineer accepts the chat, then "connected". */
  status: "waiting" | "connected";
  /** The assigned engineer's email, when the backend knows it. */
  engineerEmail?: string;
}

export interface LiveChatHistoryMessage {
  role: "customer" | "assistant" | "engineer";
  content: string;
  /** RFC 3339 timestamp, when the backend has one for this message. */
  createdAt?: string;
}

/**
 * Normalized live-chat event, delivered via {@link LiveChatClient.subscribe}.
 * Each variant's fields are exactly what the backend guarantees for that
 * wire event today (see events.ts's own doc comment for the verified
 * wire -> public mapping) — nothing here is speculative optionality.
 *
 * Deliberately NOT collapsed into one generic "completed"/"ended" event:
 * "converted" (the chat became a real support case), "disconnected" (the
 * engineer ended the session with no case created), and "expired" (nobody
 * was ever available) are different outcomes a consuming UI needs to tell
 * apart, not interchangeable ways a chat can stop.
 */
/** See the "engineerStatus" {@link LiveChatEvent}. */
export type LiveChatEngineerStatus = "away" | "back" | "busy";

export type LiveChatEvent =
  | { type: "queued"; message: string }
  | { type: "assigned"; engineerEmail: string }
  | { type: "message"; content: string; engineerEmail: string }
  /** The chat ended on the engineer's side. `reason` is "inactive" when it
   * was ended for the engineer after they were away too long; then
   * `engineerEmail` may be empty. */
  | { type: "disconnected"; engineerEmail: string; reason?: "inactive" }
  /** Not terminal: the engineer seems to have stepped away ("away"), came
   * back ("back"), or has been quiet with the portal open ("busy"). */
  | { type: "engineerStatus"; status: LiveChatEngineerStatus }
  | { type: "converted"; engineerEmail: string; entityCaseId: string }
  | { type: "expired"; message: string }
  /** Client/transport-level only — never a wire event from the backend.
   * See sse.ts for exactly when this is raised (a failed connection
   * attempt, a non-2xx response, or the stream ending unexpectedly). */
  | { type: "error"; message: string };

/**
 * Escape hatch for a consumer whose auth storage strategy makes a raw
 * bearer token string unobtainable in the calling context — e.g. Asgardeo
 * SPA SDK's `storage: "webWorker"` mode, which deliberately keeps the
 * access token inside a dedicated worker and never returns it to
 * main-thread code (see `@asgardeo/auth-spa`'s own `getAccessToken()` doc
 * comment). When {@link LiveChatClientConfig.requestTransport} is
 * provided, the SDK calls it instead of building its own
 * `fetch()` + `Authorization: Bearer <token>` request for every
 * POST-JSON call (startChat/sendMessage/completeChat) — wire it to
 * whatever your app's own authenticated HTTP client already does (e.g.
 * `AsgardeoSPAClient.getInstance().httpRequest`).
 */
export interface LiveChatRequestTransport {
  /**
   * Performs an authenticated POST of `body` as JSON to `url` and
   * resolves with the raw response status and body text — never throws
   * for a non-2xx response (the SDK itself turns that into a
   * {@link LiveChatError}); only reject for a genuine transport failure
   * (e.g. no network).
   */
  postJson(url: string, body: unknown): Promise<{ status: number; text: string }>;
  /**
   * Performs an authenticated GET of `url` and resolves with the raw
   * response status and body text — same never-throws-on-non-2xx contract
   * as {@link postJson}. Optional so an existing implementation written
   * before {@link LiveChatClient.getHistory} existed still satisfies this
   * interface; the SDK throws a clear {@link LiveChatError} if getHistory
   * is called without this being provided, rather than a confusing
   * "not a function" from calling it directly.
   */
  getJson?(url: string): Promise<{ status: number; text: string }>;
}

/**
 * The streaming counterpart to {@link LiveChatRequestTransport}, for
 * {@link LiveChatClient.subscribe}'s SSE connection. When
 * {@link LiveChatClientConfig.streamTransport} is provided, the SDK calls
 * it instead of its own `fetch()`-based SSE open.
 */
export interface LiveChatStreamTransport {
  /**
   * Opens an authenticated GET of `url` and resolves with the response
   * body as a byte stream, already positioned to read from the start.
   * `signal` aborts when the caller's `unsubscribe()` fires — honor it so
   * the underlying connection actually closes. Reject for anything that
   * prevents a usable stream (a non-2xx response, no body, a transport
   * failure); the SDK surfaces that as an `{ type: "error" }` event.
   */
  openStream(url: string, signal: AbortSignal): Promise<ReadableStream<Uint8Array>>;
}

/** Configuration for {@link createLiveChatClient}. */
export interface LiveChatClientConfig {
  /** console-chat-bridge's base URL, e.g. "https://support.example.com". */
  baseUrl: string;
  /** This product's tenant slug, as configured on console-chat-bridge
   * (see internal/tenant.Config.Slug). URL-encoded automatically. */
  tenant: string;
  /**
   * Returns the caller's own current access token. May be sync or async.
   * Called fresh before every request this SDK builds itself (including
   * once per subscribe() call, since a bearer token can only be attached
   * at SSE connection time, not per-frame) — this SDK never caches a
   * token across calls, so token refresh is entirely the caller's
   * responsibility.
   *
   * Required unless both {@link requestTransport} and
   * {@link streamTransport} are provided (in which case this SDK never
   * needs a raw token string at all, and this is ignored if given).
   */
  getAccessToken?: () => Promise<string> | string;
  /** See {@link LiveChatRequestTransport}'s own doc comment. Covers
   * startChat/sendMessage/completeChat; does not affect subscribe() --
   * see {@link streamTransport} for that. */
  requestTransport?: LiveChatRequestTransport;
  /** See {@link LiveChatStreamTransport}'s own doc comment. Covers only
   * subscribe(); does not affect startChat/sendMessage/completeChat --
   * see {@link requestTransport} for those. */
  streamTransport?: LiveChatStreamTransport;
}

/** The public client. See index.ts's module doc comment for a usage example. */
export interface LiveChatClient {
  /** Starts a new chat session: POST /v1/{tenant}/chats. */
  startChat(req: StartChatRequest): Promise<StartChatResult>;

  /**
   * Subscribes to a case's live event stream:
   * GET /v1/{tenant}/chats/{caseId}/events.
   *
   * Returns an unsubscribe function. Calling it aborts the underlying
   * connection immediately — no further onEvent calls happen after it
   * returns, and no reader/connection is left open.
   */
  subscribe(caseId: string, onEvent: (event: LiveChatEvent) => void): () => void;

  /** Sends a customer message: POST /v1/{tenant}/chats/{caseId}/messages. */
  sendMessage(caseId: string, message: { content: string }): Promise<void>;

  /** Ends the chat from the customer's side: POST /v1/{tenant}/chats/{caseId}/complete. */
  completeChat(caseId: string): Promise<void>;

  /**
   * Fetches the full transcript to date for an existing case:
   * GET /v1/{tenant}/chats/{caseId}/history. Meant for restoring a
   * conversation a caller lost track of client-side (e.g. a page reload
   * mid-chat) but still has caseId for — not called by subscribe() or any
   * other method here, since a live stream only ever delivers events going
   * forward from when it opens.
   */
  getHistory(caseId: string): Promise<LiveChatHistoryMessage[]>;

  /**
   * Returns the caller's chat in this tenant that has not ended yet, or null
   * when there is none: GET /v1/{tenant}/chats/current. Used to resume a chat
   * after a page reload or in another tab.
   */
  getCurrentChat(): Promise<LiveChatCurrentChat | null>;

  /** Whether the bridge has an AI assistant for this tenant:
   * GET /v1/{tenant}/assistant. */
  isAssistantAvailable(): Promise<boolean>;

  /**
   * Streams the assistant's answers in one of the caller's conversations:
   * GET /v1/{tenant}/assistant/{conversationId}/events. Open it before
   * asking; answers are not replayed. Returns an unsubscribe function.
   */
  subscribeAssistant(conversationId: string, onEvent: (event: LiveChatAssistantEvent) => void): () => void;

  /** Asks the assistant a question in a conversation the caller names:
   * POST /v1/{tenant}/assistant/{conversationId}/messages. The answer
   * arrives on {@link subscribeAssistant}. */
  askAssistant(conversationId: string, message: string): Promise<void>;
}

/** One step of an AI assistant's answer; see
 * {@link LiveChatClient.subscribeAssistant}. An answer is any number of
 * "status" and "token" events, then "done" or "error". */
export type LiveChatAssistantEvent =
  /** Progress worth showing, such as "Searching the knowledge base". */
  | { type: "status"; text: string }
  /** The next piece of the answer. */
  | { type: "token"; text: string }
  /** The complete answer; it replaces the streamed pieces. */
  | { type: "done"; text: string }
  /** The answer failed; text is safe to show. */
  | { type: "error"; text: string }
  /** Client-side only: the stream itself failed. */
  | { type: "disconnected"; message: string };
