# live-chat-demo

A minimal reference consumer proving `@wso2/live-chat-client` integrates with
a second, independent product using nothing but config, the SDK itself, this
product's own access-token, and this product's own (tiny, console-logged)
UI/state.

**Not a real product.** It exists solely to prove Stage 4's reuse claim for
the live-engineer-chat framework -- see the project's Stage 4 report for the
full investigation.

## What it proves

Adding this second consumer required:

1. A new `console-chat-bridge` `TENANT_REGISTRY` row (tenant slug
   `live-chat-demo`) -- no SCIM configured, since this app's own IdP
   application isn't token-binding-bound.
2. `@wso2/live-chat-client`, unmodified, as a normal dependency
   (`file:../live-chat-sdk/sdk-ts`).
3. This file's own `getAccessToken` (see below).
4. This file's own `DemoState` (see `src/index.ts`).

No changes to `csm-portal/backend`, `chat-routing-service`,
`console-chat-bridge`'s handlers, or the SDK itself.

## Running it

```bash
pnpm install
LIVE_CHAT_DEMO_ACCESS_TOKEN=<a real access token for the live-chat-demo IdP app> pnpm start
```

`LIVE_CHAT_DEMO_ACCESS_TOKEN` stands in for a real product's own auth
library (`AsgardeoSPAClient.getAccessToken()`, etc.) -- this demo doesn't
reimplement a browser OAuth2/PKCE flow, it just needs a token already
obtained from one, for the IdP application registered for this tenant's
`ClientID` on whatever WSO2 IS/IdP instance the tenant's own `Issuer` points
at.

The script starts a chat, waits for an engineer to accept it, sends one
message, waits briefly for a reply, then completes the chat and exits.
Ctrl-C at any point unsubscribes and completes the chat before exiting.
