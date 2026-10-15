# console-chat-bridge

The gateway between a product's live-chat panel (`<wso2-live-chat>`) and
the WSO2 support system. A product's browser talks only to this service: it
checks the user's login token against that product's own login provider,
asks the product's AI assistant, and forwards engineer chats to
`csm-portal/backend` as a machine-to-machine client. No support-system or AI
credential ever reaches the browser.

## Contents

- [Quick Start](#quick-start)
- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
- [One-time setup](#one-time-setup)
- [API Endpoints](#api-endpoints)
- [Project Structure](#project-structure)
- [Testing](#testing)
- [Known limitations](#known-limitations)

## Quick Start

```bash
# from apps/console-chat-bridge/backend
cp .env.example .env   # then fill in the values, see Configuration
go run ./cmd/server
```

The service listens on port `8090` by default. `GET /health` returns `200`
once it is up.

## Overview

- **One row per product (tenant).** Each product is a row in
  `TENANT_REGISTRY`: its login provider, allowed browser origins, and the
  routing tags its chats carry. The row is chosen per request by the
  `{tenant}` segment of `/v1/{tenant}/...`. Adding a product is
  configuration, not code; see
  [`docs/TENANT_ONBOARDING.md`](docs/TENANT_ONBOARDING.md).
- **Token checks.** A tenant's tokens are verified either as signed JWTs
  against its JWKS endpoint, or by asking its identity provider
  (introspection). The token's `sub` is the chat's owner.
- **Tenant isolation.** A request may only touch chats created under its
  own tenant, and only from that tenant's allowed origins.
- **AI assistant.** The panel answers with an AI first. The bridge calls
  the tenant's assistant (Novera, the product's own AI service, a mock, or
  none) and streams the answer back; see
  [`docs/ASSISTANT_PROVIDERS.md`](docs/ASSISTANT_PROVIDERS.md).
- **Engineer chats.** Starting a chat, messages and ending it are forwarded
  to `csm-portal/backend`'s internal chat API. Engineer events (joined,
  replied, ended, away) are pushed back by csm-portal to
  `POST /internal/chat-events` and streamed to the panel over Server-Sent
  Events.
- **Restore.** A customer who reloads the page, or opens another tab, gets
  their open chat and its transcript back.

How this fits with the other services: [`../../live-chat-sdk/ARCHITECTURE.md`](../../live-chat-sdk/ARCHITECTURE.md).

## Prerequisites

- Go 1.26 or later
- A running `csm-portal/backend` to forward chats to
- Each tenant's login provider reachable from this service (its JWKS or
  introspection endpoint)

## Configuration

Settings are read from environment variables; a `.env` file in this
directory is loaded first if present. `.env.example` documents every
variable in full.

| Area | Variables | Required |
|---|---|---|
| Tenants | `TENANT_REGISTRY` (one row per product), `TENANT_<SLUG>_CLIENT_SECRET` for an introspection tenant, `TENANT_<SLUG>_SCIM_CLIENT_SECRET` for a tenant with SCIM in its row, `BRIDGE_ROUTING_SOURCE` | Yes, for the `/v1` API |
| Outbound to csm-portal | `CSM_PORTAL_BASE_URL`, `CSM_PORTAL_TOKEN_URL`, `CSM_PORTAL_CLIENT_ID`, `CSM_PORTAL_CLIENT_SECRET`, `CSM_PORTAL_SCOPES` | Yes |
| Inbound from csm-portal | `CSM_PORTAL_PUSH_CLIENT_ID` (the client allowed to call `POST /internal/chat-events`) | Yes |
| AI assistant | `ASSISTANT_PROVIDER` (`novera`, `http`, `mock` or `off`), `TENANT_<SLUG>_ASSISTANT_PROVIDER` to override it for one product, `TENANT_<SLUG>_ASSISTANT_HTTP_URL`/`_HTTP_TOKEN` for an `http` provider, `ASSISTANT_DISABLED_TENANTS` | No; see [`docs/ASSISTANT_PROVIDERS.md`](docs/ASSISTANT_PROVIDERS.md) |
| Novera | `NOVERA_WS_BASE_URL`, `NOVERA_TOKEN_URL`, `NOVERA_CLIENT_ID`, `NOVERA_CLIENT_SECRET`, `NOVERA_SCOPES` | Only for the `novera` provider |
| Legacy single-tenant path | `KNOWN_ISSUER_BASE_URL`, `INTROSPECTION_CLIENT_ID`, `INTROSPECTION_CLIENT_SECRET`, `INTROSPECTION_INSECURE_SKIP_VERIFY`, `CORS_ALLOWED_ORIGINS` | Yes: the bridge does not start without the first three and `CORS_ALLOWED_ORIGINS`, even when every product is a `TENANT_REGISTRY` row. They configure `/support/chats` |
| User ID lookup via SCIM, legacy path | `SCIM_BASE_URL`, `SCIM_TOKEN_URL`, `SCIM_CLIENT_ID`, `SCIM_CLIENT_SECRET`, `SCIM_SCOPES` | No; only for `/support/chats`. A `TENANT_REGISTRY` tenant sets SCIM in its own row (fields 14–17) |
| Server | `PORT` | No, defaults to `8090` |

The bridge reads its configuration once at startup and refuses to start on
an invalid tenant row or assistant setting, naming the problem.

A tenant's `routingSource` must match a notifier registered in
csm-portal/backend (leave it blank to use the default), or engineer replies
never reach the product; see "Routing source must match a csm-portal
notifier" in `docs/TENANT_ONBOARDING.md`.

## One-time setup

### On csm-portal/backend

1. Register an OAuth2 client-credentials client for this bridge with the
   `internal_console_chat_escalate` scope. This is how the bridge calls
   csm-portal's internal chat API. Its Client ID/Secret go in
   `CSM_PORTAL_CLIENT_ID`/`CSM_PORTAL_CLIENT_SECRET`.
2. Register a second client for the reverse direction: csm-portal pushing
   engineer events to this bridge's `POST /internal/chat-events`. Set
   csm-portal's `CONSOLE_CHAT_BRIDGE_BASE_URL`/`CONSOLE_CHAT_BRIDGE_CLIENT_ID`/
   `CONSOLE_CHAT_BRIDGE_CLIENT_SECRET` to it, and this bridge's
   `CSM_PORTAL_PUSH_CLIENT_ID` to the same Client ID.

### For an introspection tenant (opaque tokens, e.g. WSO2 IS)

1. In that identity provider, create a confidential application used only
   to authenticate this bridge's introspection calls. Its client ID goes in
   the tenant row; its secret in `TENANT_<SLUG>_CLIENT_SECRET`.
2. If the product's tokens are bound to the browser (WSO2 IS's built-in
   Console app is), the bridge cannot read the user from UserInfo. In that
   case register a second client-credentials application with only the
   `internal_user_mgt_list` scope on the tenant's "SCIM2 Users API". Give
   the tenant row all 17 fields: field 13 blank, then the SCIM client ID,
   SCIM base URL, token URL and `internal_user_mgt_list`. Its secret goes
   in `TENANT_<SLUG>_SCIM_CLIENT_SECRET`. `docs/TENANT_ONBOARDING.md` has
   the details.

A JWKS tenant (signed JWTs, e.g. Devant through Choreo's STS) needs no
setup in its identity provider: the bridge only reads its public keys.

## API Endpoints

Every `/v1` route needs `Authorization: Bearer <user token>` from one of the
tenant's allowed origins. The panel makes these calls; they are listed here
for troubleshooting. The full contract is in [`openapi.yaml`](openapi.yaml).

### Engineer chat

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/{tenant}/chats` | Start a chat. `202` with `caseId` and `conversationId`; `409` when the customer already has an open chat |
| `GET` | `/v1/{tenant}/chats/current` | The customer's open chat (waiting or connected, with the engineer), or `404` when there is none |
| `GET` | `/v1/{tenant}/chats/{caseId}/events` | Engineer events, as Server-Sent Events |
| `POST` | `/v1/{tenant}/chats/{caseId}/messages` | Send a customer message |
| `GET` | `/v1/{tenant}/chats/{caseId}/history` | The transcript so far, for reloads and reconnects |
| `POST` | `/v1/{tenant}/chats/{caseId}/complete` | End the chat from the customer side |

### AI assistant

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/{tenant}/assistant` | Whether the tenant has an assistant; `404` when it has none |
| `GET` | `/v1/{tenant}/assistant/{conversationId}/events` | The assistant's answers, as Server-Sent Events |
| `POST` | `/v1/{tenant}/assistant/{conversationId}/messages` | Ask a question; the answer arrives on the events stream. `409` while still answering |

### Service-to-service and legacy

| Method | Path | Caller |
|---|---|---|
| `POST` | `/internal/chat-events` | csm-portal/backend, pushing engineer events; its client ID must be `CSM_PORTAL_PUSH_CLIENT_ID` |
| `POST` | `/support/chats`, `/support/chats/{caseId}/messages`; `GET` `/support/chats/{caseId}/stream` | The original single-tenant Console integration, kept for backward compatibility. New integrations use `/v1` |
| `GET` | `/health` | Liveness, no authentication |

Status codes: `400` invalid input, `401` missing or unverifiable token,
`403` chat belongs to another tenant, `404` unknown chat or tenant, `409`
an open chat already exists or the chat has ended, `502` csm-portal
unreachable.

## Project Structure

```text
backend/
├── cmd/
│   ├── server/              # The bridge
│   └── example-assistant/   # A runnable example of a product's own AI service
├── internal/
│   ├── tenant/              # TENANT_REGISTRY parsing
│   ├── tokenvalidator/      # JWKS and introspection token checks
│   ├── introspect/          # RFC 7662 introspection client
│   ├── scim/                # Optional user lookup for tokens without a usable sub
│   ├── middleware/          # Tenant resolution, CORS, auth
│   ├── handler/             # /v1 chat and assistant routes, legacy routes, event push
│   ├── assistant/           # Novera, http and mock assistant providers
│   ├── csmchat/             # Client for csm-portal's internal chat API
│   └── stream/              # In-memory event hub behind the SSE streams
├── docs/
│   ├── TENANT_ONBOARDING.md # Adding a product
│   └── ASSISTANT_PROVIDERS.md
└── openapi.yaml
```

## Testing

```bash
go vet ./...
go test ./...
```

To try the `http` assistant provider locally, run the example service
(`go run ./cmd/example-assistant`, port `8095`) and point a tenant at it;
see [`docs/ASSISTANT_PROVIDERS.md`](docs/ASSISTANT_PROVIDERS.md).

## Known limitations

- **Single instance.** Open SSE connections and the case-ownership cache
  are in memory, so the bridge runs as one instance and a restart drops
  live connections. Clients reconnect and reload the transcript, and the
  ownership cache is rebuilt from csm-portal. Running several instances
  would need sticky sessions at the load balancer.
- **No events are replayed.** A client that missed an event reloads the
  transcript and the chat's status instead.
- **Configuration by environment variable.** Adding a tenant or changing an
  assistant needs a restart.
- **No storage of its own.** Chats are stored by csm-portal and
  chat-routing-service; an AI-only conversation is kept by the panel and the
  AI service, not here.
