/**
 * @wso2/live-chat-client
 *
 * Framework-agnostic TypeScript client for the generic live-engineer-chat
 * API exposed by console-chat-bridge (`POST /v1/{tenant}/chats`,
 * `GET /v1/{tenant}/chats/{caseId}/events`,
 * `POST /v1/{tenant}/chats/{caseId}/messages`,
 * `POST /v1/{tenant}/chats/{caseId}/complete`). Any product embedding
 * this can escalate a chat to a live engineer and exchange messages
 * without knowing anything about csm-portal/backend, chat-routing-service,
 * console-chat-bridge's own internals, Identity Console, or Ask AI.
 *
 * ## Usage
 *
 * ```ts
 * import { createLiveChatClient } from "@wso2/live-chat-client";
 *
 * const client = createLiveChatClient({
 *   baseUrl: "https://support.example.com",
 *   tenant: "my-product",
 *   getAccessToken: () => auth.getAccessToken()
 * });
 *
 * const session = await client.startChat({
 *   message: "I need help configuring SSO.",
 *   subject: "SSO configuration"
 * });
 *
 * const unsubscribe = client.subscribe(session.caseId, (event) => {
 *   console.log(event);
 * });
 *
 * await client.sendMessage(session.caseId, { content: "Hello" });
 *
 * // later
 * unsubscribe();
 * await client.completeChat(session.caseId);
 * ```
 *
 * ## What this SDK owns, and what it doesn't
 *
 * - **The consumer owns UI and state.** This is a transport/client
 *   library, not a state-management layer -- it holds no session list, no
 *   message history, no React/Vue/Svelte/etc. bindings. Track whatever
 *   `startChat` returns and whatever `subscribe` delivers however your
 *   own app already manages state.
 * - **The consumer owns access-token acquisition and refresh.**
 *   `getAccessToken` is called fresh before every request this SDK makes,
 *   including once per `subscribe()` call -- it never caches a token
 *   itself. If your token can expire mid-session, either refresh
 *   proactively inside `getAccessToken` or re-`subscribe()`/retry after
 *   refreshing on a 401 {@link LiveChatError}.
 * - **Realtime delivery is Server-Sent Events**, over `fetch` +
 *   `ReadableStream` rather than the native `EventSource` API -- this API
 *   requires a bearer token on every request, including the stream
 *   itself, which `EventSource` cannot send.
 * - **`/v1` requires an IdP configuration that produces a canonical
 *   Subject claim.** console-chat-bridge's `/v1` routes authorize by
 *   Subject only (never falling back to a Username claim the way the
 *   legacy Ask AI integration does) -- a token whose introspection/JWKS
 *   validation yields no Subject is rejected with 401 regardless of what
 *   other claims it carries. This is not something this SDK works around
 *   client-side; it's entirely a bridge/IdP-configuration concern -- see
 *   console-chat-bridge's own tenant onboarding doc (`docs/
 *   TENANT_ONBOARDING.md`) for exactly what a tenant's IdP needs to
 *   provide, including the JWT-introspection fast path and the optional
 *   UserInfo/SCIM resolution paths for an opaque-token IdP that doesn't
 *   put `sub` on introspection directly.
 * - **React integration is intentionally separate.** A future
 *   `@wso2/live-chat-react` package (hooks, providers) is meant to sit on
 *   top of this client, not inside it -- this package has zero React (or
 *   any other framework) dependency.
 */

export { createLiveChatClient } from "./client.js";
export { LiveChatError } from "./errors.js";
export type {
  ChatMessage,
  LiveChatClient,
  LiveChatClientConfig,
  LiveChatEvent,
  StartChatRequest,
  StartChatResult
} from "./types.js";
