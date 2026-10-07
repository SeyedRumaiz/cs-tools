# @wso2/live-chat-widget

A drop-in **"Chat with Support"** web component, `<wso2-live-chat>`, that connects a signed-in customer of any WSO2 product to a live support engineer.

- **Same code in every product.** Products differ only in a JSON configuration (colors, position, size, wording), never in code.
- **Works with any framework** (React, Angular, Vue, plain HTML), because it is a standard custom element.
- **Isolated styling.** It renders inside a shadow root, so the host page's CSS cannot break it and its CSS cannot leak out.
- **One package.** It bundles [`@wso2/live-chat-client`](../sdk-ts), so a product installs nothing else.

It talks to `console-chat-bridge`'s `/v1/{tenant}/...` API. Your product needs a tenant row on the bridge first; see `apps/console-chat-bridge/backend/docs/TENANT_ONBOARDING.md`.

## Install

Not yet published to a registry. Build a package file and vendor it into your product:

```bash
cd apps/live-chat-sdk/widget
pnpm install && pnpm run build && pnpm pack
# -> wso2-live-chat-widget-0.1.3.tgz
```

Copy it to `<your-app>/vendor/` and add it to your `package.json`:

```json
"dependencies": {
  "@wso2/live-chat-widget": "file:./vendor/wso2-live-chat-widget-0.1.3.tgz"
}
```

If your `.gitignore` ignores `*.tgz`, add an exception such as `!vendor/*.tgz`.

## Use

```ts
import "@wso2/live-chat-widget"; // registers <wso2-live-chat>
import type { LiveChatWidgetElement } from "@wso2/live-chat-widget";
import liveChatConfig from "./live-chat.config.json";

const chat = document.createElement("wso2-live-chat") as LiveChatWidgetElement;
chat.config = liveChatConfig;                         // look and behaviour
chat.getAccessToken = () => auth.getAccessToken();    // your product's token function
document.body.appendChild(chat);
```

That is all that is required: a launcher button appears in the bottom-right corner and opens the chat.

`getAccessToken` is set in code because a function cannot live in a JSON file. It is called before every request and should return the signed-in user's current access token.

### When the page cannot read the token

Some sign-in libraries keep the token away from page scripts, for example the Asgardeo SDK with web-worker storage. Such a product sets `transport` instead of `getAccessToken`, and the widget sends every request through the product's own HTTP client:

```ts
chat.transport = {
  requestTransport: {
    getJson: (url) => myHttp.get(url),          // resolve { status, text }, even for a non-2xx status
    postJson: (url, body) => myHttp.post(url, body),
  },
  streamTransport: {
    openStream: (url, signal) => myHttp.stream(url, { signal }), // resolve a ReadableStream<Uint8Array>
  },
};
```

The types are exported as `LiveChatTransport`, `LiveChatRequestTransport` and `LiveChatStreamTransport`.

### Ways to pass the configuration

| Way | Example |
|---|---|
| Property (object) | `chat.config = { bridgeUrl: "...", tenant: "my-product" }` |
| Attribute (JSON) | `<wso2-live-chat config='{"bridgeUrl":"...","tenant":"my-product"}'>` |
| JSON file URL | `<wso2-live-chat config-src="/live-chat.config.json">` |

## Configuration

Only `bridgeUrl` and `tenant` are required. Every other key is optional and falls back to its default, key by key. Unknown keys and values of the wrong type are ignored rather than breaking the widget.

```json
{
  "bridgeUrl": "https://support-bridge.example.com",
  "tenant": "my-product",
  "conversationIdPrefix": "my-product-",
  "layout": {
    "position": "bottom-left",
    "showLauncher": true,
    "offsetX": 24,
    "offsetY": 24,
    "width": 380,
    "height": 560,
    "zIndex": 1000
  },
  "theme": {
    "primaryColor": "#2f5fa7",
    "fontFamily": "Gilmer, Arial, sans-serif",
    "borderRadius": "8px"
  },
  "texts": {
    "title": "Chat with Support",
    "launcherLabel": "Ask an engineer"
  }
}
```

### `layout`

| Key | Default | Meaning |
|---|---|---|
| `position` | `"bottom-right"` | `"bottom-right"` or `"bottom-left"`. |
| `showLauncher` | `true` | Show the floating button. Set `false` if your product opens the chat from its own button (see `open()`). |
| `offsetX`, `offsetY` | `24`, `24` | Distance in pixels from the side and bottom of the window. |
| `width`, `height` | `380`, `560` | Panel size in pixels; it shrinks to fit small windows. |
| `zIndex` | `1000` | Raise it if your app has overlays above the chat. |

### `theme`

Any CSS color or font value.

| Key | Default | Used for |
|---|---|---|
| `primaryColor` | `#ff7300` | Launcher, header, buttons, the customer's messages. |
| `onPrimaryColor` | `#ffffff` | Text and icons on the primary color. |
| `backgroundColor` | `#ffffff` | Panel background. |
| `surfaceColor` | `#f3f4f6` | Engineer messages and the message box. |
| `textColor` | `#1f2937` | Main text. |
| `mutedTextColor` | `#6b7280` | Status line and notices. |
| `borderColor` | `#e5e7eb` | Borders and dividers. |
| `errorColor` | `#b91c1c` | Error messages. |
| `fontFamily` | system font stack | Everything. |
| `fontSize` | `14px` | Base size. |
| `borderRadius` | `12px` | Panel and message bubbles. |
| `shadow` | soft drop shadow | Launcher and panel. |

### `texts`

Every visible string can be replaced, for wording or translation: `title`, `subtitle`, `launcherLabel`, `intro`, `contextNotice`, `inputPlaceholder`, `startButton`, `sendButton`, `endButton`, `newChatButton`, `reconnectButton`, `closeLabel`, `statusIdle`, `statusStarting`, `statusQueued`, `statusConnected`, `statusEnded`, `engineerJoined`, `endedByCustomer`, `endedByEngineer`, `convertedToCase`, `expired`, `connectionLost`, `startFailed`, `alreadyOpen`, `sendFailed`, `endFailed`, `notConfigured`, `statusConnectedAnonymous`, `chatResumed`, `alreadyOpenResumed`, `endedWhileAway`. `{engineer}` and `{caseId}` are filled in where they apply. See `DEFAULT_TEXTS` in `src/config.ts` for the defaults.

## Opening it from your own UI

```ts
chat.open();      // open the panel
chat.close();
chat.toggle();
chat.endChat();   // end the current chat from the customer's side
```

To hand over an AI-assistant conversation so the engineer sees it, pass it when opening:

```ts
chat.open({
  priorMessages: [
    { role: "customer", content: "How do I set up RAG?" },
    { role: "assistant", content: "Open Setup RAG Ingestion from the menu." },
  ],
});
```

It is sent with the next chat the customer starts. The widget keeps it within the bridge's limits (20 messages, 4,000 bytes each, 32 KiB in total) by dropping the oldest turns and shortening any longer message.

## Events

The element dispatches DOM events that bubble out of the shadow root:

| Event | `detail` | When |
|---|---|---|
| `live-chat-open` / `live-chat-close` | `{}` | The panel opened or closed. |
| `live-chat-started` | `{ caseId }` | A chat was created. |
| `live-chat-resumed` | `{ caseId }` | A chat still open on the server was restored, for example after a page reload. |
| `live-chat-assigned` | `{ caseId, engineerEmail }` | An engineer accepted it. |
| `live-chat-ended` | `{ caseId, reason, entityCaseId?, transcript }` | The chat ended. `reason` is `customer`, `engineer`, `converted`, `expired`, or `unknown` when it ended while the page was not listening (for example during a reload). |
| `live-chat-error` | `{ message }` | Starting failed or the connection dropped. |

For example, to return the customer to your AI assistant when the chat ends:

```ts
chat.addEventListener("live-chat-ended", () => {
  setTimeout(() => { chat.close(); openAssistant(); }, 2500);
});
```

## Styling beyond the config

The config covers most needs. For more, the host page can:

- override CSS variables on the element, for example `wso2-live-chat { --lc-primary: hotpink; }` (values set through `config.theme` take precedence);
- style named parts with `::part()`: `launcher`, `panel`, `header`, `status`, `messages`, `error`, `composer`, `actions`.

## Behaviour notes

- **Reloads and other tabs.** On load the widget asks the bridge for the customer's open chat and, if there is one, restores its transcript and status and reconnects. The panel reopens only if it was open before the reload. Starting a chat while one is already open (for example in another tab) reopens that chat instead of failing.
- **Ending is shown live on both sides.** When the engineer ends the chat or turns it into a case, the panel shows it immediately and offers "Start a new chat".
- **Reconnect.** If the live connection drops, the panel shows "The connection was lost" with a Reconnect button that reloads the transcript and listens again.
- **Leaving the page** stops listening but does not end the chat; ending is always an explicit action.
- **Security.** Message text is always inserted as text, never as HTML.

## Development

```bash
pnpm install
pnpm run typecheck
pnpm run lint
pnpm run test
pnpm run build   # dist/live-chat-widget.js (ESM, SDK bundled) + .d.ts
```
