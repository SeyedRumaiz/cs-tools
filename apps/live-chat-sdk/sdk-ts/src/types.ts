/**
 * Public types for @wso2/live-chat-client.
 *
 * These mirror console-chat-bridge's generic `/v1/{tenant}/...` API
 * exactly as implemented (see apps/console-chat-bridge/backend/internal/
 * handler/chats_v1.go's v1EscalateRequest/v1EscalateResponse/
 * v1MessageRequest), not an idealized sketch of it -- a few field names/
 * optionality differ from an earlier illustrative design note, and this
 * file follows the real backend. Deliberately excluded: source, channel,
 * tenant, tenantSlug, projectId -- these are authoritative, derived
 * server-side from the resolved tenant's own configuration, never
 * client-supplied (see tenant.Config in the bridge).
 */

/**
 * One prior message given as context when starting a chat -- e.g. an
 * existing AI-chatbot transcript. Never "engineer": a pre-escalation
 * transcript predates any engineer being involved (mirrors
 * router.PriorMessageRole on the backend, minus the read-only "engineer"
 * value that backend adds only when echoing an already-accepted chat's
 * transcript back, which this SDK never does).
 */
export interface ChatMessage {
  role: "customer" | "assistant";
  content: string;
  /** RFC 3339 timestamp. Optional -- omitted messages are timestamped by
   * the backend at ingestion time. */
  createdAt?: string;
}

/** Request body for {@link LiveChatClient.startChat}. */
export interface StartChatRequest {
  /** The message that starts the chat. Required -- mirrors the backend's
   * own required `message` field. */
  message: string;
  /** A stable per-session id the caller already has (e.g. an existing
   * AI-chatbot conversation this escalates from). Optional -- the backend
   * mints one from the new session's caseId when omitted, so caseId and
   * conversationId start out equal. */
  conversationId?: string;
  subject?: string;
  /** Display name only -- never used for authorization or attribution.
   * The caller's own identity (from the access token) is what the backend
   * actually attributes the chat to. */
  customerName?: string;
  /** Prior conversation history for context, NOT including `message`
   * itself. */
  priorMessages?: ChatMessage[];
  /** Arbitrary tenant-supplied key/value data. Subject to the backend's
   * own limits (at most 20 keys, 64 bytes/key, 500 bytes/value) and
   * reserved/sensitive-key rejection (e.g. "tenantSlug", any key
   * containing "token"/"secret"/"password"/"credential"/"apikey") -- this
   * SDK does not duplicate those checks client-side; a violation surfaces
   * as a 400 {@link LiveChatError} from startChat. */
  metadata?: Record<string, string>;
}

/** Result of {@link LiveChatClient.startChat}. */
export interface StartChatResult {
  /** This session's case id -- pass to subscribe/sendMessage/completeChat. */
  caseId: string;
  /** Echoes back the conversation id (client-supplied or backend-minted). */
  conversationId: string;
}

/**
 * Normalized live-chat event, delivered via {@link LiveChatClient.subscribe}.
 * Each variant's fields are exactly what the backend guarantees for that
 * wire event today (see events.ts's own doc comment for the verified
 * wire -> public mapping) -- nothing here is speculative optionality.
 *
 * Deliberately NOT collapsed into one generic "completed"/"ended" event:
 * "converted" (the chat became a real support case), "disconnected" (the
 * engineer ended the session with no case created), and "expired" (nobody
 * was ever available) are different outcomes a consuming UI needs to tell
 * apart, not interchangeable ways a chat can stop.
 */
export type LiveChatEvent =
  | { type: "queued"; message: string }
  | { type: "assigned"; engineerEmail: string }
  | { type: "message"; content: string; engineerEmail: string }
  | { type: "disconnected"; engineerEmail: string }
  | { type: "converted"; engineerEmail: string; entityCaseId: string }
  | { type: "expired"; message: string }
  /** Client/transport-level only -- never a wire event from the backend.
   * See sse.ts for exactly when this is raised (a failed connection
   * attempt, a non-2xx response, or the stream ending unexpectedly). */
  | { type: "error"; message: string };

/** Configuration for {@link createLiveChatClient}. */
export interface LiveChatClientConfig {
  /** console-chat-bridge's base URL, e.g. "https://support.example.com". */
  baseUrl: string;
  /** This product's tenant slug, as configured on console-chat-bridge
   * (see internal/tenant.Config.Slug). URL-encoded automatically. */
  tenant: string;
  /**
   * Returns the caller's own current access token. May be sync or async.
   * Called fresh before every request (including once per subscribe()
   * call, since a bearer token can only be attached at SSE connection
   * time, not per-frame) -- this SDK never caches a token across calls,
   * so token refresh is entirely the caller's responsibility.
   */
  getAccessToken: () => Promise<string> | string;
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
   * connection immediately -- no further onEvent calls happen after it
   * returns, and no reader/connection is left open.
   */
  subscribe(caseId: string, onEvent: (event: LiveChatEvent) => void): () => void;

  /** Sends a customer message: POST /v1/{tenant}/chats/{caseId}/messages. */
  sendMessage(caseId: string, message: { content: string }): Promise<void>;

  /** Ends the chat from the customer's side: POST /v1/{tenant}/chats/{caseId}/complete. */
  completeChat(caseId: string): Promise<void>;
}
