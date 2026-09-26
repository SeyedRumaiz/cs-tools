/**
 * live-chat-demo -- a minimal reference consumer proving @wso2/live-chat-client
 * integrates with a second, independent product using only:
 *
 *   1. a new console-chat-bridge TENANT_REGISTRY entry (tenant slug
 *      "live-chat-demo", no SCIM configured since this app's own tokens
 *      aren't token-binding-bound -- see internal/tenant's own doc comment)
 *   2. this package's own @wso2/live-chat-client dependency, unmodified
 *   3. this file's own getAccessToken -- this product's "existing
 *      access-token provider". A real product supplies this from its own
 *      auth library (AsgardeoSPAClient.getAccessToken(), etc.); this demo
 *      reads an already-obtained token from LIVE_CHAT_DEMO_ACCESS_TOKEN so
 *      it doesn't need to reimplement a browser OAuth2/PKCE flow just to
 *      prove the SDK contract -- getting that real token (via this same
 *      WSO2 IS instance's own login form) is a one-time, out-of-band step,
 *      documented in README.md.
 *   4. this file's own DemoState -- this product's "own UI/state". A real
 *      product renders this in its UI framework of choice; this demo just
 *      logs every transition to the console instead.
 *
 * NOT a real product, and never meant to become one -- it exists only to
 * prove that adding a second consumer to the existing live-engineer-chat
 * framework requires none of: new csm-portal/backend handlers,
 * chat-routing-service logic, console-chat-bridge endpoints, or
 * @wso2/live-chat-client internals. See the project's Stage 4 report for
 * the full investigation this demo is the output of.
 */

import { createLiveChatClient, type LiveChatEvent, type LiveChatClient } from "@wso2/live-chat-client";

const BASE_URL = process.env.LIVE_CHAT_DEMO_BRIDGE_URL ?? "http://localhost:8090";
const ACCESS_TOKEN = process.env.LIVE_CHAT_DEMO_ACCESS_TOKEN;

if (!ACCESS_TOKEN) {
  console.error("LIVE_CHAT_DEMO_ACCESS_TOKEN is not set -- see README.md for how to obtain one.");
  process.exit(1);
}

type DemoStatus = "idle" | "queued" | "connected" | "ended";

/** This product's own UI/state -- a real product renders this; this demo logs it. */
class DemoState {
  status: DemoStatus = "idle";
  engineerEmail: string | null = null;
  messages: { from: "customer" | "engineer"; content: string }[] = [];

  log(label: string): void {
    console.log(`[state] ${label} ->`, {
      status: this.status,
      engineerEmail: this.engineerEmail,
      messageCount: this.messages.length
    });
  }
}

const state = new DemoState();

const client: LiveChatClient = createLiveChatClient({
  baseUrl: BASE_URL,
  tenant: "live-chat-demo",
  getAccessToken: () => ACCESS_TOKEN
});

function handleEvent(event: LiveChatEvent): void {
  console.log("[event]", event);
  switch (event.type) {
    case "queued":
      state.status = "queued";
      state.log("queued");
      break;
    case "assigned":
      state.status = "connected";
      state.engineerEmail = event.engineerEmail;
      state.log("assigned");
      break;
    case "message":
      state.messages.push({ from: "engineer", content: event.content });
      state.log("engineer message");
      break;
    case "disconnected":
      state.status = "ended";
      state.log(`ended by ${event.engineerEmail}`);
      break;
    case "converted":
      state.status = "ended";
      state.log(`converted to case ${event.entityCaseId} by ${event.engineerEmail}`);
      break;
    case "expired":
      state.status = "idle";
      state.log("expired");
      break;
    case "error":
      console.error("[error]", event.message);
      break;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForStatus(want: DemoStatus, timeoutMs: number): Promise<void> {
  const start = Date.now();
  while (state.status !== want) {
    if (Date.now() - start > timeoutMs) {
      throw new Error(`timed out after ${timeoutMs}ms waiting for status "${want}" (currently "${state.status}")`);
    }
    await sleep(500);
  }
}

async function main(): Promise<void> {
  console.log(`live-chat-demo: starting a chat against tenant "live-chat-demo" at ${BASE_URL}`);

  const result = await client.startChat({
    message: "Hello from the live-chat-demo reference consumer.",
    conversationId: `demo-${Date.now()}`
  });
  console.log("[startChat]", result);
  state.status = "queued";
  state.log("startChat");

  const unsubscribe = client.subscribe(result.caseId, handleEvent);

  let shuttingDown = false;
  process.on("SIGINT", () => {
    if (shuttingDown) return;
    shuttingDown = true;
    console.log("\nlive-chat-demo: shutting down -- unsubscribing before completing (SSE cleanup).");
    unsubscribe();
    client
      .completeChat(result.caseId)
      .catch((err: unknown) => console.error("completeChat on shutdown failed (may already be complete):", err))
      .finally(() => process.exit(0));
  });

  // Wait for an engineer to accept, then send one message -- in a real UI
  // this is a text box's onSubmit; here it's driven by state.status
  // reaching CONNECTED.
  console.log(`live-chat-demo: waiting for an engineer to accept caseId=${result.caseId}...`);
  await waitForStatus("connected", 120_000);

  await client.sendMessage(result.caseId, { content: "This is the live-chat-demo customer's reply." });
  state.messages.push({ from: "customer", content: "This is the live-chat-demo customer's reply." });
  console.log("[sendMessage] sent");

  // Give the engineer a moment to reply, then end the chat from this side
  // -- unsubscribe (SSE cleanup) strictly before completeChat, exactly as
  // identity-apps' own clearCopilotChatWithApi thunk does.
  await sleep(5_000);
  unsubscribe();
  await client.completeChat(result.caseId);
  console.log("[completeChat] done -- SSE unsubscribed before completing.");
  process.exit(0);
}

main().catch((err: unknown) => {
  console.error("live-chat-demo failed:", err);
  process.exit(1);
});
