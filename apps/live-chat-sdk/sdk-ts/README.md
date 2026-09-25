# @wso2/live-chat-client

Framework-agnostic TypeScript client for the generic live-engineer-chat API
exposed by `console-chat-bridge` (`/v1/{tenant}/...`). No dependency on
React, Asgardeo's SDKs, or any of csm-portal/backend, chat-routing-service,
console-chat-bridge's own internals, Identity Console, or Ask AI.

See `src/index.ts`'s own doc comment for the full usage example and design
notes (what this SDK owns vs. what the consumer owns, the SSE transport
choice, and a known IdP-integration caveat). Short version:

```ts
import { createLiveChatClient } from "@wso2/live-chat-client";

const client = createLiveChatClient({
  baseUrl: "https://support.example.com",
  tenant: "my-product",
  getAccessToken: () => auth.getAccessToken()
});

const session = await client.startChat({ message: "I need help with SSO." });

const unsubscribe = client.subscribe(session.caseId, (event) => {
  console.log(event);
});

await client.sendMessage(session.caseId, { content: "Hello" });

unsubscribe();
await client.completeChat(session.caseId);
```

## Commands

```bash
pnpm install
pnpm run typecheck
pnpm run lint
pnpm run test
pnpm run build   # emits dist/ (ESM + .d.ts)
```

## Status

Stage 2 of the live-chat SDK extraction: the plain TypeScript client only.
No React bindings yet (`@wso2/live-chat-react` is separate, later work) and
no product has been migrated onto this yet (Identity Console's Ask AI panel
still calls console-chat-bridge's legacy `/support/chats` routes directly).
