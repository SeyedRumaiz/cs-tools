# live-chat-sdk Go SDK

Shared Go code for the live-chat services' calls to each other. Two
packages:

- **`pushevents`**: the `ChatEvent` wire shape for live-chat lifecycle
  events. csm-portal/backend sends these; console-chat-bridge and
  customer-portal/backend-v2 receive them. Every sender and receiver imports
  this one definition, so a field added on one side cannot be silently
  dropped on the other.
- **`m2mclient`**: an HTTP client for machine-to-machine calls. It fetches
  and caches an OAuth2 client-credentials token, refuses redirects, and
  bounds error bodies. csm-portal's `chatnotify`, the bridge's `csmchat` and
  backend-v2's `csmchat` are built on it.

## Event types

`pushevents.Type`, the value of `ChatEvent.Type`:

| Type | Sent to | Meaning |
|---|---|---|
| `queued` | the customer's product | No engineer is free; the chat is waiting |
| `engineer_assigned` | the customer's product | An engineer accepted the chat |
| `engineer_message` | the customer's product | The engineer sent a message |
| `engineer_status` | the customer's product | The engineer is `away` (tab closed and quiet), `back`, or `busy` (tab open but quiet); see `ChatEvent.Status` |
| `engineer_disconnected` | the customer's product | The chat ended from the engineer's side; `Reason` is `inactive` when it was ended because the engineer was away too long |
| `converted_to_case` | the customer's product | The engineer turned the chat into a support case (`EntityCaseID`) |
| `chat_abandoned` | the customer's product | The chat waited in the queue too long and was given up |
| `customer_escalation`, `customer_message`, `session_accepted`, `session_closed`, `session_closed_by_customer`, `case_timed_out` | csm-portal's engineers only | Engineer-side updates; never sent to a product |

`PriorMessage` carries the customer's AI conversation from before the
handover (`role` is `customer` or `assistant`).

## Install

Within this monorepo, add a `require` + `replace` pair to your service's
`go.mod` (there is no published module proxy entry yet — see Versioning
below, and `apps/chat-routing-service/sdk-go`'s own identical convention,
which this mirrors):

```
require github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go v0.0.0-00010101000000-000000000000

replace github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go => ../../live-chat-sdk/sdk-go
```

(adjust the relative path to wherever your service sits relative to
`apps/live-chat-sdk/sdk-go`). Then `go mod tidy`.

## Use

```go
import (
	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/m2mclient"
	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/pushevents"
)

client := m2mclient.NewClient(m2mclient.Config{
	BaseURL:      os.Getenv("TARGET_BASE_URL"),
	TokenURL:     os.Getenv("TARGET_TOKEN_URL"),
	ClientID:     os.Getenv("TARGET_CLIENT_ID"),
	ClientSecret: os.Getenv("TARGET_CLIENT_SECRET"),
})

evt := pushevents.ChatEvent{Type: pushevents.TypeEngineerMessage, CaseID: caseID, Message: "hi"}
payload, _ := json.Marshal(evt)
body, status, err := client.Do(ctx, http.MethodPost, "/internal/chat-events", payload, nil)
```

## Not server-to-server only, unlike `chat-routing-service/sdk-go`

Every existing consumer of this module is a backend process (never
browser-shipped code), but that's a property of who happens to use it
today, not something this module enforces the way `chat-routing-service/
sdk-go`'s shared-secret model does. `m2mclient.Config`'s `ClientSecret`
field is still a real secret — never construct a `Client` in code that
ships to a browser.

## Versioning

Same convention as `chat-routing-service/sdk-go`: a nested Go module inside
the monorepo (`apps/live-chat-sdk/sdk-go`), versioned independently of every
caller. Consumed today via a `replace` directive to the local path (see
Install); a future out-of-monorepo consumer would instead `go get` a git
tag scoped to this subdirectory (e.g. `apps/live-chat-sdk/sdk-go/v0.1.0`)
with no `replace` needed.
