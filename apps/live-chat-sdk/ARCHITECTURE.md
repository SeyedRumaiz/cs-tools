# Live-chat framework — architecture

How a customer in a WSO2 product gets one support panel, where an AI
assistant answers first and a WSO2 support engineer takes over in the same
panel, and which parts of that path are shared versus owned by the product.

## The chain

```
Product page
   |  creates <wso2-live-chat>, supplies the user's token (or its own HTTP client)
   v
@wso2/live-chat-widget        the panel: AI chat, engineer chat, queue, restore
   |
   v
@wso2/live-chat-client        typed calls + Server-Sent Events, no UI
   |  HTTPS /v1/{tenant}/...  (user's bearer token)
   v
console-chat-bridge           tenant auth, CORS, tenant isolation,
   |         \                AI assistant calls, event fan-out
   |          \--> AI assistant (Novera, the product's own service, mock or off)
   |  machine-to-machine (client credentials)
   v
csm-portal/backend            engineers' app, idle rules, Google Chat alerts
   |
   v
chat-routing-service          presence, queue, assignment, timeouts
```

Engineer-side events travel back up the chain: csm-portal/backend pushes
them to the bridge (`POST /internal/chat-events`), and the bridge forwards
them to the panel's open event stream.

| Layer | Owned by |
|---|---|
| Product page | The product |
| `@wso2/live-chat-widget` | Shared UI, configured per product with a JSON file |
| `@wso2/live-chat-client` | Shared client, framework-agnostic |
| `console-chat-bridge` | Shared, multi-tenant gateway; one configuration row per product |
| AI assistant | Novera (WSO2), or the product's own service |
| `csm-portal`, `chat-routing-service` | Shared support back end |

A product plugs in through configuration and the widget's public API. It
never adds code to a shared layer: see
`console-chat-bridge/backend/docs/TENANT_ONBOARDING.md` for the onboarding
checklist.

## Packages in this directory

| Package | What it is |
|---|---|
| `widget/` (`@wso2/live-chat-widget`) | The `<wso2-live-chat>` web component. What products install. |
| `sdk-ts/` (`@wso2/live-chat-client`) | The TypeScript client the widget is built on. Usable directly by a product that wants its own UI. |
| `sdk-go/` | Shared Go code for the services: `m2mclient` (client-credentials HTTP client) and `pushevents` (the engineer event shape csm-portal pushes to the bridge). |

## Components

### Product page — product-owned

The product decides where the panel opens from (a floating button, a header
button, a help menu) and how it looks, through `live-chat.config.json`
(layout, theme, texts, assistant settings). It supplies either:

- `getAccessToken`, a function returning the signed-in user's token; or
- `transport`, its own HTTP functions, when its sign-in library keeps the
  token away from page scripts (the Asgardeo SDK in web-worker mode).

It can listen for the widget's DOM events (`live-chat-started`, `-assigned`,
`-resumed`, `-ended`, `-open`, `-close`, `-error`). It writes no chat UI.

### `@wso2/live-chat-widget` — the panel

One web component in every product, with Shadow DOM so the product's styles
don't leak in. It:

- answers with the AI assistant first, when the bridge has one for the
  tenant, and offers "Chat with an Engineer" (always, or after the assistant
  fails a few times);
- passes the AI conversation to the engineer as prior messages, trimmed to
  the bridge's limits;
- shows the queue, the engineer joining, messages both ways, and the end of
  the chat, then returns to the assistant;
- restores an open chat after a page reload or in a second tab;
- tells the customer when the engineer seems to have stepped away, is back,
  or is still looking into it.

See `widget/README.md` for every configuration option.

### `@wso2/live-chat-client` — the client

Typed calls over the bridge's `/v1/{tenant}` API and its two event streams:

| Method | Purpose |
|---|---|
| `startChat` | Start an engineer chat |
| `subscribe` | Engineer chat events (SSE) |
| `sendMessage` | Send a customer message |
| `completeChat` | End the chat from the customer side |
| `getHistory` | The transcript so far, for reloads |
| `getCurrentChat` | The customer's open chat, if any |
| `isAssistantAvailable` | Whether the tenant has an AI assistant |
| `subscribeAssistant` | AI answer events (SSE) |
| `askAssistant` | Ask the assistant a question |

Engineer chat events: `queued`, `assigned`, `message`, `engineerStatus`
(`away`, `back`, `busy`), `disconnected` (with `reason: "inactive"` when the
engineer was away too long), `converted`, `expired`, and `error`.
Assistant events: `status`, `token`, `done`, `error`, `disconnected`.

The client holds no UI state and knows nothing about any product beyond the
`baseUrl` and `tenant` it is created with. See `sdk-ts/README.md`.

### `console-chat-bridge` — the gateway

The only service a product's browser talks to. For each request it:

1. Resolves `{tenant}` from the URL against `TENANT_REGISTRY`.
2. Verifies the user's token against that tenant's login provider, by
   JWKS (signed JWT) or introspection (opaque token).
3. Checks the browser origin against the tenant's allow-list, and that the
   chat belongs to the tenant.
4. Calls csm-portal/backend's internal chat API as a machine-to-machine
   client, tagging the chat with the tenant's routing source, channel and
   project.
5. Asks the tenant's AI assistant and streams the answer back.
6. Forwards engineer events to the panel's event stream.

Everything tenant-specific is configuration: the tenant row, and
`TENANT_<SLUG>_ASSISTANT_PROVIDER` to choose the AI assistant
(see `docs/ASSISTANT_PROVIDERS.md`). The legacy single-tenant
`/support/chats` path is still served for backward compatibility; new
integrations use `/v1`.

### `csm-portal` and `chat-routing-service` — the support back end

- **csm-portal** is the engineers' app: pop-up alerts with sound, accept,
  reply, end and Convert to Case, plus a Google Chat card per event. Its
  back end sends each reply back through the bridge, and applies the idle
  rules: when an engineer's tab is closed, the customer is warned after
  2 minutes and the chat ends after 5; when the tab is open, the customer
  is told the engineer is still looking into it, and the chat stays open.
- **chat-routing-service** tracks presence, gives each chat to the
  least-busy free engineer, queues chats when nobody is free, and passes on
  chats not accepted within 90 seconds.

Neither changes when a product is added: they see a chat tagged with the
tenant's routing source, channel and project.

## One chat, end to end

1. The customer opens the panel. The widget checks `getCurrentChat` and,
   if the customer already has an open chat, restores it.
2. The customer asks the AI assistant. The bridge calls the tenant's
   assistant and streams the answer over the assistant event stream.
3. The customer chooses "Chat with an Engineer". The widget calls
   `startChat` with the AI conversation as prior messages.
4. The bridge verifies the token and calls csm-portal's
   `POST /internal/chat/escalate`. csm-portal asks chat-routing-service for
   a free engineer, who gets the pop-up, or queues the chat.
5. The widget subscribes to the chat's events. When the engineer accepts,
   csm-portal pushes `assigned` to the bridge, which forwards it.
6. Messages flow both ways: `sendMessage` up, `message` events down.
7. The chat ends from either side, by Convert to Case, or by the idle rules.
   The widget returns to the assistant, and the engineer's slot goes to the
   next queued chat.

The product never calls csm-portal or chat-routing-service, and needs no
service credentials or AI credentials of its own.

## The one-sentence version

**How the chat looks and where it opens belongs to the product; everything
else — the panel, the AI, getting the chat to an engineer — is shared and
added through configuration.**
