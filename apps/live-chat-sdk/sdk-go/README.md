# live-chat-sdk Go SDK

Shared Go plumbing behind the live-engineer-chat feature's service-to-service
calls — extracted from three independently hand-rolled copies of the same
code: `customer-portal/backend-v2/internal/csmchat`,
`console-chat-bridge/backend/internal/csmchat`, and
`csm-portal/backend/internal/chatnotify` each implemented their own "OAuth2
client-credentials token, refuse redirects, bound the error body" HTTP
client, and csm-portal/backend's own `chatEvent` struct was independently
hand-copied by every receiver of it — with real, observed drift (a field
csm-portal/backend added that a receiver's own copy silently dropped on
decode, since `encoding/json` never errors on an unknown field).

Two packages:

- **`pushevents`** — the one shared `ChatEvent` wire shape every sender and
  receiver of a live-chat lifecycle event (`queued`/`engineer_assigned`/
  `engineer_message`/etc.) now imports, instead of hand-copying.
- **`m2mclient`** — the shared OAuth2-client-credentials HTTP client
  plumbing. Each of the three original packages keeps its own typed methods
  and package identity, now built on `m2mclient.Client` instead of a fourth
  copy of the same boilerplate.

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
