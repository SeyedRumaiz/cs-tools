# live-chat-demo

A minimal reference consumer proving `@wso2/live-chat-client` integrates with
a second, independent product using nothing but config, the SDK itself, this
product's own access-token, and this product's own (tiny, console-logged)
UI/state.

**Not a real product.** It exists to show that a second, independent
product can use the live-chat framework through configuration alone.

## What it proves

Adding this second product required:

1. A new `console-chat-bridge` `TENANT_REGISTRY` row (tenant slug
   `live-chat-demo`). No SCIM is configured, since this app's tokens are not
   bound to the browser.
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
library (`AsgardeoSPAClient.getAccessToken()`, etc.). This demo doesn't
reimplement a browser OAuth2/PKCE flow; it just needs a token already
obtained from one, for the IdP application registered for this tenant's
`ClientID` on whatever WSO2 IS/IdP instance the tenant's own `Issuer` points
at.

The script starts a chat, waits for an engineer to accept it, sends one
message, waits briefly for a reply, then completes the chat and exits.
Ctrl-C at any point unsubscribes and completes the chat before exiting.

It uses `@wso2/live-chat-client` directly rather than the
`<wso2-live-chat>` web component, so it shows the client API on its own.
Real products use the web component; see `../live-chat-sdk/widget`.
