# @wso2/live-chat-client

Framework-agnostic TypeScript client for the generic live-engineer-chat API
exposed by `console-chat-bridge` (`/v1/{tenant}/...`). Zero dependency on
React, Asgardeo's SDKs, or any of csm-portal/backend, chat-routing-service,
console-chat-bridge's own internals, or any specific product.

This is a **transport client, not a state-management layer**: it holds no
session list, no message history, and no UI bindings. Your product owns its
own UI and state; this package owns only talking to the bridge correctly
(requests, SSE parsing, event normalization, error shapes).

For how this fits into the wider live-engineer-chat framework (the bridge,
csm-portal/backend, chat-routing-service), see
[`../ARCHITECTURE.md`](../ARCHITECTURE.md). For how to onboard a new
product/tenant on the bridge side, see
[`console-chat-bridge`'s tenant onboarding doc](../../console-chat-bridge/backend/docs/TENANT_ONBOARDING.md).

## Installation

Not yet published to npm (see `package.json`'s `"private": true`). Until
then, consume it as a local dependency:

- **Within this monorepo**: `"@wso2/live-chat-client": "file:../live-chat-sdk/sdk-ts"`
  (a relative `file:` path — see `apps/live-chat-demo/package.json` for a
  working example).
- **From a different repo**: pack a tarball and vendor it —
  `pnpm run build && pnpm pack` in this directory, then depend on
  `"file:./vendor/wso2-live-chat-client-<version>.tgz"` in the consuming
  product (see `identity-apps/features/admin.copilot.v1/vendor/README.md`
  for a worked example, including the regeneration steps).

Requires Node 18+ (uses native `fetch`/`ReadableStream`/`AbortController`)
or any modern browser bundler target with the same globals.

## Quick start

```ts
import { createLiveChatClient } from "@wso2/live-chat-client";

const client = createLiveChatClient({
  baseUrl: "https://support.example.com",
  tenant: "my-product",
  getAccessToken: () => auth.getAccessToken()
});

// Start a chat.
const session = await client.startChat({
  message: "I need help configuring SSO.",
  subject: "SSO configuration"
});

// Listen for engineer activity.
const unsubscribe = client.subscribe(session.caseId, (event) => {
  console.log(event);
});

// Send a follow-up message.
await client.sendMessage(session.caseId, { content: "Still waiting on a reply." });

// When the customer is done (or your UI unmounts):
unsubscribe();
await client.completeChat(session.caseId);
```

## API

### `createLiveChatClient(config)`

```ts
function createLiveChatClient(config: LiveChatClientConfig): LiveChatClient;

interface LiveChatClientConfig {
  /** console-chat-bridge's base URL, e.g. "https://support.example.com". */
  baseUrl: string;
  /** This product's tenant slug, as configured on console-chat-bridge
   * (a TENANT_REGISTRY row's Slug). URL-encoded automatically. */
  tenant: string;
  /** Returns the caller's own current access token. May be sync or async.
   * Called fresh before every request this SDK makes, including once per
   * `subscribe()` call -- this SDK never caches a token itself. */
  getAccessToken: () => Promise<string> | string;
}
```

Throws a synchronous `LiveChatError` immediately if `baseUrl`/`tenant` are
empty or `getAccessToken` isn't a function — fails at construction time,
not on the first call.

`getAccessToken` is **your product's existing token provider** — an
Asgardeo SPA client's `getAccessToken()`, an MSAL instance, a plain
closure over a stored token, whatever your app already has. This SDK never
imports or knows about any specific auth library.

### `client.startChat(req)`

```ts
startChat(req: StartChatRequest): Promise<StartChatResult>;

interface StartChatRequest {
  /** The message that starts the chat. Required. */
  message: string;
  /** A stable per-session id you already have (e.g. an existing AI-chatbot
   * conversation this escalates from). Optional -- the backend mints one
   * from the new session's caseId when omitted. */
  conversationId?: string;
  subject?: string;
  /** Display name only -- never used for authorization or attribution.
   * Your caller's own identity (from the access token) is what the
   * backend actually attributes the chat to. */
  customerName?: string;
  /** Prior conversation history for context, NOT including `message`
   * itself (e.g. an AI-chatbot transcript that led to this escalation). */
  priorMessages?: ChatMessage[];
  /** Arbitrary tenant-supplied key/value data. Subject to the backend's
   * own limits (at most 20 keys, 64 bytes/key, 500 bytes/value) and
   * reserved/sensitive-key rejection (e.g. any key containing
   * "token"/"secret"/"password"/"credential"/"apikey") -- a violation
   * surfaces as a 400 LiveChatError. */
  metadata?: Record<string, string>;
}

interface StartChatResult {
  /** This session's case id -- pass to subscribe/sendMessage/completeChat. */
  caseId: string;
  /** Echoes back the conversation id (client-supplied or backend-minted).
   * Deliberately kept distinct from caseId -- your product may have its
   * own notion of "conversation" (e.g. an AI-chat session) that predates
   * and outlives any one escalation's caseId. */
  conversationId: string;
}
```

`POST /v1/{tenant}/chats`. Rejects (via a thrown `LiveChatError`) with
`409` if the caller already has an open chat for this tenant/project, and
`401` if the access token doesn't resolve to a canonical Subject (see the
bridge's own tenant-onboarding doc for what that requires).

### `client.subscribe(caseId, onEvent)`

```ts
subscribe(caseId: string, onEvent: (event: LiveChatEvent) => void): () => void;
```

`GET /v1/{tenant}/chats/{caseId}/events`, an authenticated Server-Sent
Events stream (`fetch` + `ReadableStream`, not the native `EventSource`
API — `EventSource` cannot send an `Authorization` header, and this stream
requires a bearer token like every other call). Returns an **unsubscribe
function** — see "Unsubscribe lifecycle" below.

Every event this SDK delivers is validated at runtime before being handed
to `onEvent` — malformed or unrecognized wire data is silently dropped
(forward-compatible: a future event type your SDK version doesn't know
about yet doesn't crash or surface as an error), never passed through
with a guessed shape.

### `client.sendMessage(caseId, message)`

```ts
sendMessage(caseId: string, message: { content: string }): Promise<void>;
```

`POST /v1/{tenant}/chats/{caseId}/messages`. Sends one customer message on
an already-started chat. Works whether or not an engineer has been
assigned yet (the message is queued server-side either way) — this SDK
doesn't gate it on having seen an `"assigned"` event.

### `client.completeChat(caseId)`

```ts
completeChat(caseId: string): Promise<void>;
```

`POST /v1/{tenant}/chats/{caseId}/complete`. Ends the chat from the
customer's side. Safe to call whether the chat is still queued (removes it
from the queue) or already assigned to an engineer (releases that
engineer's capacity and triggers queue backfill on the bridge's side) —
your UI doesn't need to know which case it's in.

**Always call this before your UI discards a chat**, even if you're also
calling `unsubscribe()` — the two are independent: `unsubscribe()` only
stops *your side* from listening, it does not tell the backend the chat is
over. See "Unsubscribe lifecycle" below for the ordering that matters.

## Event types

Delivered via `subscribe`'s `onEvent` callback, one variant per outcome —
deliberately not collapsed into one generic "completed"/"ended" event,
since a consuming UI needs to tell these apart:

```ts
type LiveChatEvent =
  | { type: "queued"; message: string }
  | { type: "assigned"; engineerEmail: string }
  | { type: "message"; content: string; engineerEmail: string }
  | { type: "disconnected"; engineerEmail: string }
  | { type: "converted"; engineerEmail: string; entityCaseId: string }
  | { type: "expired"; message: string }
  | { type: "error"; message: string };
```

| Type | Meaning | Typical UI reaction |
|---|---|---|
| `queued` | The chat is waiting for an available engineer. `message` is server-supplied, human-readable. | Show a "you're in the queue" message. |
| `assigned` | An engineer accepted the chat. | Show "connected", enable the message input. |
| `message` | The assigned engineer sent a message. `content` is theirs, not yours. | Append to the transcript. |
| `disconnected` | The engineer ended the session without converting it to a case. | Show "chat ended", disable the input. |
| `converted` | The engineer converted the chat into a real support case (`entityCaseId`). | Show "chat ended, case #… opened", link to it if your product can. |
| `expired` | Nobody was ever assigned before the queue gave up (server-side timeout). Distinct from `disconnected`: no engineer was ever connected. | Consider returning to an idle/retry state rather than a terminal "ended" one — the chat never really started. |
| `error` | **Client/transport-level only — never a wire event from the backend.** A failed connection attempt, a non-2xx response opening the stream, or the stream ending unexpectedly. | Surface it; decide whether to retry (see below). |

## Error handling

Two different failure channels, matching the two different transports:

- **`startChat`/`sendMessage`/`completeChat` throw** a `LiveChatError`
  (extends `Error`) on any failure — network failure, non-2xx response, or
  a malformed response body. Check `.status` to branch (e.g. `409` for
  "already have an open chat", `401` for an expired/invalid token) rather
  than parsing `.message` — `.message` is meant for display, not for
  program logic, and its exact wording can change. `.details` carries the
  parsed response body when the server returned one, for forward-
  compatible access to any extra fields a future backend response might
  add; it never contains a token.
- **`subscribe`'s stream failures are delivered as `{type: "error"}`
  events**, never thrown — there's no `await` to catch on a long-lived
  stream. This SDK does **not** auto-reconnect; on an `"error"` event,
  decide in your own product code whether to `subscribe()` again (e.g.
  with backoff) or surface a "connection lost" state to the user.

```ts
import { LiveChatError } from "@wso2/live-chat-client";

try {
  await client.startChat({ message: "Help!" });
} catch (err) {
  if (err instanceof LiveChatError && err.status === 409) {
    // already has an open chat -- point the user at it
  } else {
    // generic failure
  }
}
```

## Unsubscribe lifecycle

`subscribe()`'s return value is a plain `() => void`. Calling it:

- Aborts the underlying connection **immediately** — no further `onEvent`
  calls happen after it returns, and no reader/connection is left open.
- Is **safe to call before the connection has even finished opening** (the
  abort races the in-flight `fetch` and wins) — no need to wait for an
  `"assigned"` event or any other readiness signal first.
- Is idempotent — calling it more than once is a harmless no-op.

**Always unsubscribe before your UI stops caring about a case** — on
unmount, on navigating away, on the user closing the chat panel — to avoid
leaking an open SSE connection. `unsubscribe()` and `completeChat()` are
independent (see above): call `unsubscribe()` first, then
`completeChat()`, matching the order a real consumer (identity-apps'
`admin.copilot.v1` panel) already uses in its own cleanup path.

## Compatibility

This SDK only ever speaks `/v1/{tenant}/...` — it has no knowledge of any
product-specific legacy transport. See the bridge's own
[tenant onboarding doc](../../console-chat-bridge/backend/docs/TENANT_ONBOARDING.md#compatibility)
for how `/v1` relates to console-chat-bridge's legacy `/support/chats`
routes, and what a tenant's IdP needs to provide for `/v1` to authorize a
request at all.

## What this SDK deliberately does not do

- **No state management.** Track whatever `startChat` returns and whatever
  `subscribe` delivers however your own app already manages state (Redux,
  a signal, a plain `useState` — this SDK doesn't care).
- **No token acquisition or refresh.** `getAccessToken` is called fresh
  before every request; if your token can expire mid-session, either
  refresh proactively inside `getAccessToken` or re-`subscribe()`/retry
  after refreshing on a `401`.
- **No auto-reconnect** for the SSE stream (see "Error handling" above).
- **No React (or other framework) bindings.** A future
  `@wso2/live-chat-react` package is meant to sit on top of this client,
  not inside it — this package has zero framework dependency, by design.

## Development

```bash
pnpm install
pnpm run typecheck
pnpm run lint
pnpm run test
pnpm run build   # emits dist/ (ESM + .d.ts)
```

## Status

Framework-agnostic client only (Stage 2 of the live-chat SDK extraction).
Two products currently consume it: identity-apps' Console "Chat with an
Engineer" panel (`features/admin.copilot.v1`, via the `identity-console`
tenant) and `apps/live-chat-demo` (a minimal reference consumer, via the
`live-chat-demo` tenant) — proving the same unmodified SDK integrates with
a genuinely independent second product through configuration alone. No
React bindings yet (`@wso2/live-chat-react` is separate, later work).
