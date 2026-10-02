# console-chat-bridge

Lets a product's "Chat with an Engineer" feature escalate into the existing
cs-tools live-engineer-chat framework (`chat-routing-service` /
`csm-portal/backend`) without that product's browser ever calling either
directly, and without any `chat-routing-service`/`csm-portal` credential
ever reaching the browser.

## Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Getting started](#getting-started)
- [Authentication model](#authentication-model)
- [One-time setup](#one-time-setup)
- [API reference](#api-reference)
- [Testing](#testing)
- [Known POC limitations](#known-poc-limitations)

## Overview

Started as a single-purpose bridge for identity-apps' Console "Chat with an
Engineer" button (`features/admin.copilot.v1`); the
[Authentication model](#authentication-model) section describes that
original, still-fully-supported legacy integration. It has since grown into
a genuinely **multi-tenant** gateway — see
[Multi-tenant `/v1` routes](#multi-tenant-v1-routes) below and
[`docs/TENANT_ONBOARDING.md`](docs/TENANT_ONBOARDING.md) for onboarding any
other product.

identity-apps' Console authenticates its own admins against whichever
WSO2 IS/Asgardeo tenant the customer runs — a different issuer/keys/claims
than the WSO2-internal Asgardeo org `chat-routing-service`/`csm-portal`
already trust. Rather than teaching either of those services about
arbitrary customer tenants (a real, unresolved multi-tenant-trust problem,
see the investigation doc's blocker #1, linked below), this bridge is a
small, separate service that:

1. validates the calling product's own token against that tenant's
   configured IdP (for the legacy integration, exactly one **known, pinned**
   WSO2 IS instance), then
2. calls `csm-portal/backend`'s existing, unmodified internal escalation
   endpoints as a normal machine-to-machine client, tagging the case with
   that tenant's `source`/`channel` (`asgardeo`/`ask-ai` for the legacy
   integration).

See the project's `console-ask-ai-engineer-escalation-investigation.md` for
the full investigation the original legacy integration implements, and the
follow-up plan message for the auth/session-model decisions that POC made
concrete.

## Architecture

- [`../../live-chat-sdk/ARCHITECTURE.md`](../../live-chat-sdk/ARCHITECTURE.md)
  — how this bridge fits into the wider live-engineer-chat framework.
- [`docs/TENANT_ONBOARDING.md`](docs/TENANT_ONBOARDING.md) — required
  config fields, auth modes, CORS, tenant isolation, secret handling, and a
  full checklist for onboarding a new product.

## Getting started

### Prerequisites

- Go 1.26.6 or later (see `go.mod`).
- A running `csm-portal/backend` instance to escalate into.
- A WSO2 IS instance to validate caller tokens against — see
  [One-time setup](#one-time-setup) for what needs registering there before
  this service will authenticate anyone.

### Configuration

```bash
cp .env.example .env
```

Fill in the values `.env.example` leaves blank, then see
[One-time setup](#one-time-setup) below for what each credential requires
on the WSO2 IS / `csm-portal/backend` side. `.env.example` is the
authoritative, fully-annotated reference for every variable; the table
below is just a map of what's where.

| Category | Variables | Required |
|---|---|---|
| Browser-facing auth (legacy integration) | `KNOWN_ISSUER_BASE_URL`, `INTROSPECTION_CLIENT_ID`, `INTROSPECTION_CLIENT_SECRET`, `INTROSPECTION_INSECURE_SKIP_VERIFY`, `CORS_ALLOWED_ORIGINS` | Yes |
| Outbound M2M → `csm-portal/backend` | `CSM_PORTAL_BASE_URL`, `CSM_PORTAL_TOKEN_URL`, `CSM_PORTAL_CLIENT_ID`, `CSM_PORTAL_CLIENT_SECRET`, `CSM_PORTAL_SCOPES` | Yes |
| Inbound M2M ← `csm-portal/backend` | `CSM_PORTAL_PUSH_CLIENT_ID` | Yes |
| SCIM-based Subject resolution | `SCIM_CLIENT_ID`, `SCIM_CLIENT_SECRET`, `SCIM_BASE_URL`, `SCIM_TOKEN_URL`, `SCIM_SCOPES` | No — opt-in, see [One-time setup](#one-time-setup) step 3 |
| Multi-tenant `/v1` API | `TENANT_REGISTRY`, `BRIDGE_ROUTING_SOURCE` | No — omit to run single-tenant against `KNOWN_ISSUER_BASE_URL` |
| Server | `PORT` | No — defaults to `8090` |

### Running locally

```bash
go run ./cmd/server
```

`.env` is loaded automatically from the working directory at startup.

## Authentication model

WSO2 IS's product-wide default access token type is **Opaque**, not JWT
(confirmed against the product docs: "Opaque (Default)"), and this repo's
local instance (`wso2is-7.3.0/repository/conf/deployment.toml`) does not
override that default. This bridge is therefore built around **RFC 7662
token introspection**, not JWKS/JWT validation — see `internal/introspect`.

If your Console application's own **Access Token Type** setting
(Console → your app → Protocol → Access Token) has specifically been
switched to JWT, introspection still works (WSO2 IS's introspection
endpoint accepts both token types) but is unnecessarily slow for that case.
The legacy `/support/chats` path (pinned to `KNOWN_ISSUER_BASE_URL`) only
ever validates via `internal/introspect`, with no JWKS alternative wired
up for it. The generic `/v1/{tenant}/...` API doesn't have that
limitation: a `TENANT_REGISTRY` row can declare `validationType: "jwks"`
to verify a JWT-issuing tenant's tokens directly against its own JWKS
instead — see `internal/tokenvalidator.JWKSValidator` and
[`docs/TENANT_ONBOARDING.md`](docs/TENANT_ONBOARDING.md)'s "Auth modes"
section.

## One-time setup

### On the known WSO2 IS instance

1. Create a confidential **Standard-Based Application** (any name, e.g.
   `console-chat-bridge-introspection`) purely to authenticate this
   bridge's introspection calls. Note its Client ID/Secret →
   `INTROSPECTION_CLIENT_ID`/`INTROSPECTION_CLIENT_SECRET`. It does not
   need any scopes or grant types beyond what's needed to call
   `/oauth2/introspect` with HTTP Basic auth.
2. Confirm your Console application's own Access Token Type (see
   [Authentication model](#authentication-model)) — if it's JWT rather than
   the default Opaque, read that section before proceeding.
3. **Optional, but required if your Console application's access tokens are
   token-binding-bound** (WSO2 IS's own built-in "Console" system app is,
   by default, and its Access Token Type cannot be changed — WSO2 IS
   rejects that update for `isSystemReservedApp` applications). When bound,
   OIDC UserInfo rejects this bridge's server-side bearer-only call ("Valid
   token binding value not present in the request"), so a Console admin's
   opaque token can never resolve a Subject via UserInfo alone — see
   `internal/scim`'s own package doc comment for the full mechanics. Fix:
   register a **second, separate** confidential client-credentials
   application (e.g. `console-chat-bridge-scim`; grant type
   `client_credentials` only, no redirect URIs needed), then authorize it
   against that instance's **"SCIM2 Users API"** resource
   (`identifier: /scim2/Users`, the **tenant**-scoped one, not the
   `/o/scim2/Users` organization-scoped one) with only the
   **`internal_user_mgt_list`** scope ("List Users") — least privilege for
   the filtered search this bridge performs (`GET /scim2/Users?filter=
   userName+eq+"..."`); `internal_user_mgt_view` alone is not sufficient
   for a filter/search call. Note its Client ID/Secret →
   `SCIM_CLIENT_ID`/`SCIM_CLIENT_SECRET`. Leaving `SCIM_CLIENT_ID` unset
   keeps this tenant on UserInfo-based resolution exactly as before — SCIM
   is opt-in, never required.

### On csm-portal/backend

1. Register a new OAuth2 client-credentials client (do not reuse
   backend-v2's) carrying only a new, least-privilege scope —
   `internal_console_chat_escalate` — on csm-portal/backend's existing
   OAuth2 IdP application. This is the credential this bridge uses to call
   `POST /internal/chat/escalate` / `POST /internal/chat/customer-message`.
   Note its Client ID/Secret → `CSM_PORTAL_CLIENT_ID`/
   `CSM_PORTAL_CLIENT_SECRET`.
2. Register a second client-credentials client for the reverse direction
   (csm-portal/backend pushing events back into this bridge's
   `POST /internal/chat-events`). Set csm-portal/backend's own
   `CONSOLE_CHAT_BRIDGE_BASE_URL`/`CONSOLE_CHAT_BRIDGE_CLIENT_ID`/
   `CONSOLE_CHAT_BRIDGE_CLIENT_SECRET` (see that service's own
   `cmd/server/main.go`) to this client, and set this bridge's own
   `CSM_PORTAL_PUSH_CLIENT_ID` to the same Client ID.

## API reference

### Legacy routes (single-tenant)

Predates multi-tenancy; see
[Multi-tenant `/v1` routes](#multi-tenant-v1-routes) below for what every
new integration should use instead.

| Method | Path | Caller | Auth |
|---|---|---|---|
| POST | `/support/chats` | Console browser | bearer token, introspected against the known instance, must resolve to a real user |
| POST | `/support/chats/{caseId}/messages` | Console browser | same, plus must be the admin who opened `caseId` |
| GET | `/support/chats/{caseId}/stream` | Console browser | same |
| POST | `/internal/chat-events` | csm-portal/backend | bearer token, introspected against the known instance, `client_id` must equal `CSM_PORTAL_PUSH_CLIENT_ID` |

### Multi-tenant `/v1` routes

Generic routes — what `@wso2/live-chat-client` actually calls. Each product
gets its own `TENANT_REGISTRY` row (its own IdP, CORS origins, routing
tags, and optional SCIM-based Subject resolution), selected at request time
by the `{tenant}` URL segment — not compiled in, not a per-deployment fork.
Two independent products (identity-apps' Console, via the
`identity-console` tenant, and `apps/live-chat-demo`, a minimal reference
consumer, via the `live-chat-demo` tenant) run through this same
deployment side by side today, each fully isolated from the other's cases,
origins, and credentials. See
[`docs/TENANT_ONBOARDING.md`](docs/TENANT_ONBOARDING.md) for everything
needed to add another one.

| Method | Path | Caller | Auth |
|---|---|---|---|
| POST | `/v1/{tenant}/chats` | any onboarded product's browser | bearer token, validated against that tenant's own IdP, must resolve to a canonical Subject |
| POST | `/v1/{tenant}/chats/{caseId}/messages` | same | same, plus `caseId` must belong to `{tenant}` |
| GET | `/v1/{tenant}/chats/{caseId}/events` | same | same |
| POST | `/v1/{tenant}/chats/{caseId}/complete` | same | same |

## Testing

```bash
go vet ./...
go test ./...
```

## Known POC limitations

- `internal/stream.Hub` and `ChatsHandler`'s case-ownership map are
  in-memory, single-process, lost on restart. This is fine as long as this
  service runs as a single instance — restarting just means connected SSE
  clients reconnect (expected behavior; `GET .../history` lets them catch
  up on anything missed) and the ownership cache gets rebuilt from
  `chat_conversation`, the durable source it was always a shortcut for. It
  only becomes a real problem if this service is ever scaled to more than
  one instance: a push event could land on the instance that doesn't hold
  the matching browser's SSE connection. If that need arises, prefer
  sticky sessions at the load balancer (no code change here) over adding a
  shared pub/sub layer (e.g. Redis) — the latter is the textbook fix but is
  unwarranted added infrastructure for this service's current scale.
- No persistence beyond what csm-portal/backend/chat-routing-service
  already durably store — this bridge itself keeps no database.
