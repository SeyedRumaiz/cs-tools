# Live-engineer-chat framework — architecture

How a product's "Chat with an Engineer" feature reaches a real WSO2 support
engineer, and exactly which parts of that path are shared, reusable
infrastructure versus which parts belong entirely to the product using it.

## The chain

```
Product UI/state
   |
   |  (button click, message send, unmount)
   v
@wso2/live-chat-client
   |  startChat / subscribe / sendMessage / completeChat
   v
console-chat-bridge      -- /v1/{tenant}/...
   |                        tenant auth, tenant isolation, CORS, event fan-out
   |  M2M (escalate, customer-message), unmodified per tenant
   v
csm-portal/backend        -- /internal/chat/*
   |
   v
chat-routing-service       -- engineer presence, queue, accept, routing decisions
```

Events (queued, assigned, message, disconnected, converted, expired) flow
back up the same chain: routing decision → csm-portal/backend → bridge's
SSE fan-out → the SDK's `onEvent` callback → product state.

| Layer | Owned by |
|---|---|
| Product UI/state | **The product** |
| `@wso2/live-chat-client` | Shared transport (framework-agnostic) |
| `console-chat-bridge` | Shared, multi-tenant gateway |
| `csm-portal/backend` / `chat-routing-service` | Shared backend (pre-existing, unmodified by onboarding) |

Every layer left of `console-chat-bridge` in this diagram is **shared,
reusable infrastructure** — a new product plugs into it through
configuration and the SDK's own public API, never by adding code to it
(see `console-chat-bridge/backend/docs/TENANT_ONBOARDING.md`'s onboarding
checklist, and `apps/live-chat-demo` for a worked example of exactly that:
a second, independent consumer added with zero changes to any shared
layer).

## Component responsibilities

### Product UI/state — product-owned, not shared

Everything about *how* a chat looks and behaves in a given product: the
button that starts it, the message list, typing indicators, translations,
error copy, and whatever state-management technology that product already
uses (Redux in identity-apps' `admin.copilot.v1`, a plain class in the
`live-chat-demo` reference consumer, or anything else). None of this lives
in, or is dictated by, any shared layer — the SDK and bridge have no
opinion on it whatsoever.

### `@wso2/live-chat-client` — shared transport, framework-agnostic

A thin TypeScript client: four methods (`startChat`, `subscribe`,
`sendMessage`, `completeChat`), an event-normalization layer over Server-
Sent Events, and a typed error model. Holds no session list, no message
history, no framework bindings. Every product embedding it supplies its
own `getAccessToken` (its own existing auth library) and interprets the
events it delivers into whatever state shape that product already uses.
See `sdk-ts/README.md` for the full API.

**What makes it reusable**: it knows nothing about any specific product,
any specific IdP, or any specific tenant beyond the `tenant` slug and
`baseUrl` passed into `createLiveChatClient` at construction time.

### `console-chat-bridge` — shared, multi-tenant gateway

The one service every product's SDK client actually talks to. Per
request, it:

1. Resolves `{tenant}` from the URL path against a `TENANT_REGISTRY`
   entry (or the legacy single-tenant fallback — see below).
2. Validates the caller's bearer token against *that tenant's own* IdP
   (introspection, with optional UserInfo/SCIM Subject resolution — see
   the tenant onboarding doc).
3. Enforces that tenant's own CORS origin allow-list and case ownership
   (no cross-tenant access to another tenant's case, ever).
4. Calls `csm-portal/backend`'s existing, unmodified internal escalation
   endpoints as a plain machine-to-machine client, tagging the case with
   that tenant's own `routingSource`/`channel`/`projectID`.
5. Fans out engineer-side events (assigned, message, disconnected,
   converted, expired) back to the originating product's own SSE
   subscription.

Everything tenant-specific (IdP, CORS origins, routing tags, optional
SCIM) is **configuration** (a `TENANT_REGISTRY` row), not code — adding a
product here never means adding a route, a handler, or SDK-specific
logic.

Also still serves `/support/chats` — a separate, legacy, single-tenant
path that predates `/v1` and exists purely for backward compatibility
(see "Compatibility" in the tenant onboarding doc). New integrations use
`/v1`.

### `csm-portal/backend` and `chat-routing-service` — shared backend, pre-existing

The actual engineer-facing system: presence, queueing, case assignment,
and the CSM portal UI engineers use to accept and answer chats. Neither
of these services has ever needed to change for a new product to onboard
— they only ever see a case tagged with whatever `routingSource`/
`channel`/`projectID` the bridge assigned it, and the M2M contract between
the bridge and `csm-portal/backend` is generic (escalate a chat, send a
customer message, push an engineer event) rather than product-specific.

## One escalation's lifecycle, across every layer

1. **Product UI** calls `client.startChat({message, ...})` (SDK).
2. **SDK** `POST`s `/v1/{tenant}/chats` (bridge), attaching a fresh bearer
   token from the product's own `getAccessToken`.
3. **Bridge** validates the token against that tenant's IdP, resolves a
   canonical Subject, and calls `csm-portal/backend`'s
   `POST /internal/chat/escalate` as an M2M client, tagging the case with
   the tenant's routing/channel/project tags.
4. **csm-portal/backend** creates the case and asks **chat-routing-service**
   to route it — queued if no engineer is immediately available.
5. **Product UI** calls `client.subscribe(caseId, onEvent)` (SDK opens an
   authenticated SSE connection to the bridge).
6. An engineer accepts in the CSM portal → **chat-routing-service** →
   **csm-portal/backend** → the bridge's own event fan-out → the SDK's
   `onEvent` callback delivers `{type: "assigned", engineerEmail}` → the
   **product's own state** transitions to "connected", entirely inside
   product-owned code.
7. Messages flow both directions the same way: `client.sendMessage()` for
   customer → engineer; an `{type: "message"}` event for engineer →
   customer.
8. Either side ends the chat; `client.completeChat()` (customer-initiated)
   or an `{type: "disconnected"}`/`{type: "converted"}` event
   (engineer-initiated) — the **product's own code** decides what its UI
   does with either outcome.

At no point does the product need to know `csm-portal/backend` or
`chat-routing-service` exist — its entire surface is the four SDK methods
and the events `subscribe` delivers.

## Product-owned vs. shared transport — the one-sentence version

**If it's about how a chat *looks* or *behaves* in your product, it's
yours to own. If it's about how a chat *reaches* an engineer at all, it's
already built, tested, and reusable through configuration alone.**
