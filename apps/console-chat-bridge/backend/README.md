# console-chat-bridge

Lets identity-apps' Console "Chat with an Engineer" button (in the Ask AI
panel, `features/admin.copilot.v1`) escalate into the existing cs-tools
live-engineer-chat framework (`chat-routing-service` / `csm-portal/backend`)
without the Console's browser ever calling either directly, and without any
`chat-routing-service`/`csm-portal` credential ever reaching the browser.

See the project's `console-ask-ai-engineer-escalation-investigation.md` for
the full investigation this implements, and the follow-up plan message for
the auth/session-model decisions this POC makes concrete.

## Why this exists (in one paragraph)

identity-apps' Console authenticates its own admins against whichever
WSO2 IS/Asgardeo tenant the customer runs -- a different issuer/keys/claims
than the WSO2-internal Asgardeo org `chat-routing-service`/`csm-portal`
already trust. Rather than teaching either of those services about
arbitrary customer tenants (a real, unresolved multi-tenant-trust problem,
see the investigation's blocker #1), this bridge is a small, separate
service that: (1) validates the Console admin's own token against exactly
one **known, pinned** WSO2 IS instance, then (2) calls
`csm-portal/backend`'s existing, unmodified internal escalation endpoints
as a normal machine-to-machine client, tagging the case
`source="asgardeo"`, `channel="ask-ai"`.

## Token type: read this before touching auth code

WSO2 IS's product-wide default access token type is **Opaque**, not JWT
(confirmed against the product docs: "Opaque (Default)"), and this repo's
local instance (`wso2is-7.3.0/repository/conf/deployment.toml`) does not
override that default. This bridge is therefore built around **RFC 7662
token introspection**, not JWKS/JWT validation -- see
`internal/introspect`.

If your Console application's own **Access Token Type** setting
(Console → your app → Protocol → Access Token) has specifically been
switched to JWT, introspection still works (WSO2 IS's introspection
endpoint accepts both token types) but is unnecessarily slow for that case;
swapping in a JWKS-based validator instead is a contained change scoped to
`internal/introspect/validator.go` alone -- nothing else in this service
assumes one or the other.

## One-time setup on the known WSO2 IS instance

1. Create a confidential **Standard-Based Application** (any name, e.g.
   `console-chat-bridge-introspection`) purely to authenticate this
   bridge's introspection calls. Note its Client ID/Secret ->
   `INTROSPECTION_CLIENT_ID`/`INTROSPECTION_CLIENT_SECRET`. It does not
   need any scopes or grant types beyond what's needed to call
   `/oauth2/introspect` with HTTP Basic auth.
2. Confirm your Console application's own Access Token Type (see above) --
   if it's JWT rather than the default Opaque, see the note above before
   proceeding.
3. **Optional, but required if your Console application's access tokens are
   token-binding-bound** (WSO2 IS's own built-in "Console" system app is,
   by default, and its Access Token Type cannot be changed -- WSO2 IS
   rejects that update for `isSystemReservedApp` applications). When bound,
   OIDC UserInfo rejects this bridge's server-side bearer-only call ("Valid
   token binding value not present in the request"), so a Console admin's
   opaque token can never resolve a Subject via UserInfo alone -- see
   `internal/scim`'s own package doc comment for the full mechanics. Fix:
   register a **second, separate** confidential client-credentials
   application (e.g. `console-chat-bridge-scim`; grant type
   `client_credentials` only, no redirect URIs needed), then authorize it
   against that instance's **"SCIM2 Users API"** resource
   (`identifier: /scim2/Users`, the **tenant**-scoped one, not the
   `/o/scim2/Users` organization-scoped one) with only the
   **`internal_user_mgt_list`** scope ("List Users") -- least privilege for
   the filtered search this bridge performs (`GET /scim2/Users?filter=
   userName+eq+"..."`); `internal_user_mgt_view` alone is not sufficient
   for a filter/search call. Note its Client ID/Secret ->
   `SCIM_CLIENT_ID`/`SCIM_CLIENT_SECRET` (see `.env.example`). Leaving
   `SCIM_CLIENT_ID` unset keeps this tenant on UserInfo-based resolution
   exactly as before -- SCIM is opt-in, never required.

## One-time setup on csm-portal/backend

1. Register a new OAuth2 client-credentials client (do not reuse
   backend-v2's) carrying only a new, least-privilege scope --
   `internal_console_chat_escalate` -- on csm-portal/backend's existing
   OAuth2 IdP application. This is the credential this bridge uses to call
   `POST /internal/chat/escalate` / `POST /internal/chat/customer-message`.
   Note its Client ID/Secret -> `CSM_PORTAL_CLIENT_ID`/
   `CSM_PORTAL_CLIENT_SECRET`.
2. Register a second client-credentials client for the reverse direction
   (csm-portal/backend pushing events back into this bridge's
   `POST /internal/chat-events`). Set csm-portal/backend's own
   `CONSOLE_CHAT_BRIDGE_BASE_URL`/`CONSOLE_CHAT_BRIDGE_CLIENT_ID`/
   `CONSOLE_CHAT_BRIDGE_CLIENT_SECRET` (see that service's own
   `cmd/server/main.go`) to this client, and set this bridge's own
   `CSM_PORTAL_PUSH_CLIENT_ID` to the same Client ID.

## Routes

| Method | Path | Caller | Auth |
|---|---|---|---|
| POST | `/support/chats` | Console browser | bearer token, introspected against the known instance, must resolve to a real user |
| POST | `/support/chats/{caseId}/messages` | Console browser | same, plus must be the admin who opened `caseId` |
| GET | `/support/chats/{caseId}/stream` | Console browser | same |
| POST | `/internal/chat-events` | csm-portal/backend | bearer token, introspected against the known instance, `client_id` must equal `CSM_PORTAL_PUSH_CLIENT_ID` |

## Known POC limitations (see the investigation doc for the full list)

- Single known IS instance only -- not multi-tenant.
- `internal/stream.Hub` and `ChatsHandler`'s case-ownership map are
  in-memory, single-process, lost on restart.
- No persistence beyond what csm-portal/backend/chat-routing-service
  already durably store -- this bridge itself keeps no database.
