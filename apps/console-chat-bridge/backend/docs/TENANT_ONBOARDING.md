# Tenant onboarding

How to add a new product ("tenant") to `console-chat-bridge`'s generic
`/v1/{tenant}/...` live-engineer-chat API. See
[`../../../live-chat-sdk/ARCHITECTURE.md`](../../../live-chat-sdk/ARCHITECTURE.md)
for how this bridge fits into the wider framework, and
[`@wso2/live-chat-client`'s own docs](../../../live-chat-sdk/sdk-ts/README.md)
for the client SDK your product actually calls.

**Every example value in this document is a placeholder.** Nothing here is
a real credential, client ID, or hostname — replace every angle-bracketed
value before using any of this configuration for real.

## Required tenant fields

A tenant is one row in the `TENANT_REGISTRY` environment variable (see
`internal/tenant.BuildTable`'s own doc comment for the authoritative field
order — this document summarizes it). Rows are `;`-separated; fields
within a row are `|`-separated.

| # | Field | Required? | Meaning |
|---|---|---|---|
| 1 | `slug` | **Yes** | Identifies this tenant in the URL path (`/v1/{slug}/...`). Case-sensitive. |
| 2 | `validationType` | No (defaults to `introspection`) | `introspection` (RFC 7662 — the only kind actually implemented) or `jwks` (accepted, but currently always rejects every request — see "Auth modes" below). |
| 3 | `issuer` | **Yes**, for `introspection` | This tenant's trusted token issuer. Also the base URL introspection/UserInfo/SCIM endpoints derive from when their own override fields are left blank. |
| 4 | `introspectionURL` | No | Overrides the derived introspection endpoint (`{issuer}/oauth2/introspect`) — needed for an IdP whose introspection endpoint lives elsewhere. |
| 5 | `jwksURI` | Only for `jwks` | Unused today (see "Auth modes"). |
| 6 | `audience` | Only for `jwks` | Unused today. |
| 7 | `clientID` | **Yes**, for `introspection` | Authenticates this bridge to the tenant's introspection endpoint via HTTP Basic auth (RFC 7662 §2.1). A technical credential — see "Secret handling" for where its secret lives. |
| 8 | `insecureSkipVerify` | No (defaults to `false`) | Literal string `true` to skip TLS certificate verification for this tenant's IdP calls. **Local development only** — see the `.env.example` warning. |
| 9 | `allowedOrigins` | No | This tenant's own CORS allow-list — see "CORS/origin configuration". `,`-separated for more than one origin. |
| 10 | `routingSource` | No (defaults to `BRIDGE_ROUTING_SOURCE`) | Tags every case this tenant escalates — becomes chat-routing-service's `CaseInfo.Source`, and so csm-portal/backend's `chatNotifiers` routing key. |
| 11 | `channel` | No | Tags every case's `CaseInfo.Channel`. |
| 12 | `projectID` | No | Tags every case's `CaseInfo.ProjectID` — this is the **duplicate-open-chat scope**: two tenants sharing a `projectID` would see each other's "you already have an open chat" conflicts, so give each tenant its own. |
| 13 | `userinfoURL` | No | Overrides the derived OIDC UserInfo endpoint (`{issuer}/oauth2/userinfo`). Optional even when present — see "Optional UserInfo/SCIM resolution". |
| 14 | `scimClientID` | No | Enables SCIM-based Subject resolution for this tenant — see below. Blank (the default) means SCIM is not configured. |
| 15 | `scimBaseURL` | No | Overrides where this tenant's SCIM2 API lives — defaults to `issuer` when blank. |
| 16 | `scimTokenURL` | No | Overrides this tenant's SCIM client-credentials token endpoint — defaults to `{issuer}/oauth2/token` when blank. |
| 17 | `scimScopes` | No | `,`-separated OAuth2 scopes for the SCIM client-credentials grant (e.g. `internal_user_mgt_list` for a WSO2 IS SCIM2 tenant). |

**Field count matters**: a row is exactly 12, 13, or 17 fields — never
14–16. Fields 14–17 (SCIM) are one atomic block: either all four are
present (even if some are blank) or none are. This keeps old rows written
before SCIM support existed parsing identically to before.

## Auth modes

- **`introspection`** (the only kind actually implemented): validates the
  bearer token via RFC 7662 token introspection against the tenant's own
  IdP, then optionally enriches the result with a canonical Subject (see
  below) when introspection alone doesn't carry one.
- **`jwks`**: the row shape exists so a JWT-issuing tenant can already be
  *configured* without a `TENANT_REGISTRY` parse error, but actual
  JWT/JWKS signature verification is not implemented yet — every request
  to a `jwks`-type tenant fails. Don't configure a real tenant with this
  type yet; it's reserved for future work.

## Canonical Subject requirement

Every `/v1/{tenant}/...` route requires the validated `Identity` to carry
a non-empty **Subject** — there is no fallback to a Username claim the way
console-chat-bridge's legacy `/support/chats` path has (see
"Compatibility" below). A token that introspects successfully but yields
no Subject and no way to resolve one is rejected with `401`, regardless of
what other claims it carries.

This is deliberate: `/v1` is the API new tenants should build against, and
Subject (a stable, IdP-issued identifier) is the only claim reliably
suitable as a case's owner across every IdP shape this bridge might ever
support — Username is optional, mutable, and IdP-dependent in a way
Subject isn't.

## Optional UserInfo/SCIM resolution

Some IdPs (opaque-token deployments especially) don't put `sub` on their
introspection response at all — only on `/oauth2/userinfo`, or nowhere
directly resolvable at all without an extra call. When introspection alone
yields no Subject but does yield a `Username`, this bridge tries to
resolve a canonical Subject two ways, in order of preference:

1. **SCIM** (if `scimClientID` is configured for this tenant): looks the
   introspected username up via the IdP's own SCIM2 Users API
   (`GET /scim2/Users?filter=userName+eq+"..."`), authenticated with this
   tenant's own, separate, least-privilege client-credentials grant —
   never the caller's own token. Returns that user's immutable SCIM id.
   **Use this when your IdP application's access tokens are
   token-binding-bound** (see WSO2 IS's own built-in "Console" application
   for an example) — UserInfo then rejects a bearer-only server-side call
   with "Valid token binding value not present in the request", something
   SCIM's own separate client-credentials grant is entirely unaffected by.
2. **UserInfo** (the default, when SCIM isn't configured): calls the IdP's
   OIDC UserInfo endpoint with the caller's own bearer token forwarded.
   Works for any IdP whose UserInfo endpoint doesn't require anything this
   bridge (a server-side process with no browser session/cookie) can't
   provide.

Both are opt-in in the sense that a tenant with neither working still
functions for any IdP that puts `sub` directly on introspection (see
"JWT/introspection-sub fast path" below) — resolution is only ever
*attempted* when introspection's own Subject came back empty.

**Username is a lookup key only, never a substitute Subject.** Whichever
path resolves, the *resolved value* (a UserInfo `sub` or a SCIM immutable
user id) becomes the canonical Subject — the introspected `username`
itself is never used as one.

## CORS/origin configuration

Each tenant carries its own `allowedOrigins` (field 9) — **not** one
global list shared across tenants. This matters: a global union would let
tenant A's configured browser origin send authenticated cross-origin
requests against tenant B's data, which the browser's own same-origin
policy would otherwise have prevented.

- Must be the **exact** origin your product's browser code is served from
  (what shows up as the tab's URL / the request's `Origin` header) — not
  your IdP's own origin, and not this bridge's own origin. Those are
  routinely different hosts and it's easy to copy the wrong one.
- Matched by exact string equality — no trailing slash, no wildcard.
- **Fail-closed**: an empty/unset allow-list for a tenant allows no
  browser request through at all, rather than reflecting any `Origin`
  back.
- CORS is resolved *after* tenant resolution (even for a bare `OPTIONS`
  preflight) — an unknown tenant slug 404s before CORS ever runs, so a
  preflight for a tenant that doesn't exist has nothing to answer about.

## Tenant isolation

Beyond auth and CORS being per-tenant, every case-scoped `/v1` route
(message send, event stream, complete) durably checks that the case being
acted on actually belongs to the tenant resolved from the URL path — a
token valid for tenant A can never read, message, or complete a case that
belongs to tenant B, even if the caseId is guessed or leaked. A
cross-tenant attempt is rejected with `403`.

This isolation needed **no new code** to extend to a second tenant — it
was built generically from the start (see `internal/handler/chats_v1.go`'s
`requireTenantCase`), and adding a tenant is purely a `TENANT_REGISTRY`
config change.

## Secret handling

**No secret ever lives in `TENANT_REGISTRY` itself** — Choreo's config UI
and this registry's own flat, row-based design are both a poor fit for a
value that must stay confidential. Every tenant's secrets are resolved
separately, by naming convention, from the process environment:

| Secret | Env var | Purpose |
|---|---|---|
| Introspection client secret | `TENANT_<SLUG>_CLIENT_SECRET` | Basic-auth secret for field 7's `clientID`. |
| SCIM client secret | `TENANT_<SLUG>_SCIM_CLIENT_SECRET` | Client-credentials secret for field 14's `scimClientID`, when SCIM is configured. |

`<SLUG>` is the tenant's own `slug` (field 1), upper-cased with every
character outside `[A-Z0-9]` replaced by `_` — e.g. slug `acme-corp` reads
`TENANT_ACME_CORP_CLIENT_SECRET` / `TENANT_ACME_CORP_SCIM_CLIENT_SECRET`.

Register a **separate, least-privilege** client-credentials application
for SCIM — never reuse the introspection client's credentials. The
introspection client only ever needs to call `/oauth2/introspect`; the
SCIM client needs a client-credentials-grant token carrying the IdP's
SCIM2-view scope (e.g. WSO2 IS's `internal_user_mgt_list` — not
`internal_user_mgt_view`, which doesn't cover a filtered *search* call)
and nothing else.

## Example configs

Both examples below use placeholder values only — `<...>` markers are not
meant to be used literally.

### `identity-console`-style tenant (SCIM configured, token-binding-bound IdP)

```
TENANT_REGISTRY=identity-console|introspection|<https://your-wso2-is-instance>||||<INTROSPECTION_CLIENT_ID>|false|<https://your-console-origin>|asgardeo|ask-ai|console-ask-ai||<SCIM_CLIENT_ID>|||internal_user_mgt_list
TENANT_IDENTITY_CONSOLE_CLIENT_SECRET=<introspection client secret>
TENANT_IDENTITY_CONSOLE_SCIM_CLIENT_SECRET=<SCIM client secret>
```

- 17 fields: SCIM is configured (fields 14–17), since this tenant's IdP
  application issues token-binding-bound access tokens.
- `scimBaseURL`/`scimTokenURL` (fields 15–16) left blank — both derive
  from `issuer` (field 3), since SCIM2 and the token endpoint live on the
  same instance as introspection here.
- `routingSource`/`channel`/`projectID` (`asgardeo`/`ask-ai`/
  `console-ask-ai`) match this tenant's specific product-integration
  tags — pick your own for a different product.

### Generic tenant (no SCIM — UserInfo resolution, or introspection already has `sub`)

```
TENANT_REGISTRY=<your-product-slug>|introspection|<https://your-idp-instance>||||<INTROSPECTION_CLIENT_ID>|false|<https://your-product-origin>||<your-channel-tag>|<your-project-tag>
TENANT_<YOUR_PRODUCT_SLUG>_CLIENT_SECRET=<introspection client secret>
```

- 12 fields: no `userinfoURL` override, no SCIM. If this tenant's IdP puts
  `sub` on introspection directly (see "JWT/introspection-sub fast path"
  below), no further resolution is ever attempted. If it doesn't but its
  UserInfo endpoint works for a bearer-only server-side call, UserInfo
  resolution is tried automatically with zero extra config.
- `routingSource` left blank — defaults to `BRIDGE_ROUTING_SOURCE`.

### Multiple tenants together

Rows are `;`-separated in one `TENANT_REGISTRY` value:

```
TENANT_REGISTRY=<tenant-1 row>;<tenant-2 row>;<tenant-3 row>
```

Each tenant's own secret env vars are independent and named by its own
slug, as above.

## Compatibility

- **Legacy `/support/chats` remains fully supported, unchanged.** It
  predates the generic `/v1` API, is hardcoded to one pinned IdP instance
  via `KNOWN_ISSUER_BASE_URL`/`INTROSPECTION_CLIENT_ID`/
  `INTROSPECTION_CLIENT_SECRET` (not `TENANT_REGISTRY`), and authorizes by
  **Subject-or-Username** (`RequireUser`) — the one place this bridge
  falls back to Username, preserved deliberately so this path's own
  failure semantics never changed while `/v1`/SCIM were built out around
  it. Nothing in this document applies to it.
- **`/v1/{tenant}/...` is what every new integration should use.** It's
  the only path with per-tenant isolation, per-tenant CORS, and the
  SCIM/UserInfo resolution options above; `/support/chats` will never gain
  any of them.
- **JWT/introspection-sub fast path**: when a tenant's IdP issues
  JWT-typed access tokens, RFC 7662 introspection of them returns `sub`
  directly (JWT introspection is self-contained claim decoding, unlike an
  opaque token's DB-record lookup) — Subject resolution stops right there,
  and neither UserInfo nor SCIM is ever called. This is the cheapest and
  simplest path when available; whether your tenant's IdP application can
  use it is a per-application IdP setting (e.g. WSO2 IS's own
  Applications → Protocol → Access Token → Token Type), and some
  IdP-reserved system applications cannot have it changed at all (see
  "Optional UserInfo/SCIM resolution" above for why SCIM exists in the
  first place).
- **Opaque-token Subject resolution order**: introspection's own `sub` →
  (if configured) SCIM → (otherwise) UserInfo → `401` if none produce one.
  Always in that order, always short-circuiting at the first success.
- **Known limitation: email-as-username tenants.** SCIM resolution strips
  the tenant-domain suffix from an introspected username (the segment
  after the *last* `@`) before searching — this matches the default
  behavior of WSO2's own `MultitenantUtils.getTenantAwareUsername`
  (verified against the actual product bytecode, not just its docs) when
  that IdP's "email as username" feature is **disabled**. When a tenant's
  IdP has email-as-username *enabled*, the real WSO2 utility applies a
  different, string-shape-dependent heuristic this bridge cannot replicate
  server-side (whether that mode is even on is an IdP configuration this
  bridge has no API to query). If your tenant's IdP uses email-as-username,
  confirm SCIM resolution actually matches your real usernames before
  relying on it in production — UserInfo resolution (which doesn't parse
  the username at all) is unaffected by this limitation.

## New-tenant onboarding checklist

1. Confirm your IdP's token introspection response for a real user token —
   does it carry `sub` directly? If yes, you're on the fast path; skip to
   step 5.
2. If not, confirm whether your IdP application's access tokens are
   token-binding-bound (check its own "Access Token" / token-binding
   setting). If bound, plan on SCIM (step 3); if not, plan on UserInfo
   (skip to step 4).
3. **SCIM path**: register a separate, least-privilege client-credentials
   application on your IdP, scoped only to its SCIM2 (or equivalent)
   user-search capability. Note its client ID/secret.
4. **UserInfo path**: confirm your IdP's UserInfo endpoint answers a
   bearer-only server-side call (no browser cookie) for a real user token
   — if it 400s/401s the same way a token-binding-bound app would, you
   actually need step 3 instead.
5. Register (or confirm) a confidential application for this bridge's own
   RFC 7662 introspection calls. Note its client ID/secret.
6. Decide this tenant's `slug`, `routingSource`, `channel`, and
   `projectID` — each must be unique enough that this tenant's
   duplicate-open-chat scope (`projectID`) and csm-portal routing
   (`routingSource`) don't collide with an existing tenant's.
7. Write the `TENANT_REGISTRY` row (12, 13, or 17 fields per "Required
   tenant fields" above) and the matching `TENANT_<SLUG>_CLIENT_SECRET` /
   `TENANT_<SLUG>_SCIM_CLIENT_SECRET` env vars.
8. Set this tenant's `allowedOrigins` to the exact browser origin your
   product is served from.
9. Point your product's own code at `@wso2/live-chat-client`
   (`createLiveChatClient({baseUrl, tenant: "<your-slug>", getAccessToken})`)
   using your product's own existing token provider — see that package's
   own README for the full client API.
10. Verify end to end: `startChat` succeeds (`202`), an engineer can
    accept the case, messages flow both directions, `completeChat` works,
    and a token from a *different* tenant is rejected (`403`) against this
    tenant's cases.

No code change on `csm-portal/backend`, `chat-routing-service`,
`console-chat-bridge`'s own route handlers, or `@wso2/live-chat-client`
itself should ever be required to complete this checklist — if one
becomes necessary, that's a sign of a hidden, product-specific assumption
somewhere in this framework worth raising, not a normal part of onboarding
a new tenant.
