# chat-routing-service Go SDK

A small, dependency-free Go client for [chat-routing-service](../backend) —
the in-memory engineer availability/queue service that decides which
engineer (if any) a live-chat escalation is routed to.

This module is the same client csm-portal/backend uses in production,
extracted so other internal services can call chat-routing-service without
copy-pasting the HTTP plumbing and wire types by hand.

## Install

Within this monorepo, add a `require` + `replace` pair to your service's
`go.mod` (there is no published module proxy entry yet — see Versioning
below):

```
require github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go v0.0.0-00010101000000-000000000000

replace github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go => ../../chat-routing-service/sdk-go
```

(adjust the relative path to wherever your service sits relative to
`apps/chat-routing-service/sdk-go`). Then `go mod tidy`.

## Use

```go
import "github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"

client := routingclient.NewClient(routingclient.Config{
	BaseURL:       os.Getenv("ROUTING_SERVICE_BASE_URL"), // e.g. http://localhost:9096
	InternalToken: os.Getenv("ROUTING_SERVICE_TOKEN"),
})

result, err := client.Escalate(ctx, routingclient.CaseInfo{
	CaseID:         caseID,
	ConversationID: conversationID,
	CustomerEmail:  customerEmail,
})
```

See `routingclient/client.go`'s doc comments for every method
(`Escalate`, `SetPresence`, `Completed`, `Decline`, `Accept`, `GetPresence`,
`CreateWorkItem`, `AddComment`) and their exact request/response shapes.

## Server-to-server only

`InternalToken` is a static shared secret (`X-Routing-Service-Token`) meant
for backend-to-backend calls. **Never** construct a `Client` in code that
ships to a browser, and never proxy this token out to one. A frontend that
needs routing state should call its own backend's own proxy endpoints —
see `apps/csm-portal/backend/internal/handler/chat.go` for the reference
implementation (`HandleEscalate`, `HandleSetPresence`, `HandleGetPresence`,
`HandleDeclineSession`) — and have that backend hold this `Client` and
`InternalToken` server-side, exactly as csm-portal/backend already does.

## Versioning

This is a nested Go module inside the monorepo
(`apps/chat-routing-service/sdk-go`), versioned independently of both
chat-routing-service itself and its callers. Two ways to consume it:

- **Within this monorepo** (current state): a `replace` directive per the
  Install section above, pointing at the local path. Every caller always
  builds against the SDK version currently checked out.
- **From another repo, or to pin a version even inside this monorepo**:
  push a git tag scoped to this subdirectory, e.g.
  `apps/chat-routing-service/sdk-go/v0.1.0`, then `go get` that module path
  at that version like any normal Go dependency — no `replace` needed. Go's
  module resolution understands a tag prefixed with a nested module's own
  path as belonging to that module, standard practice for Go monorepos.

A breaking change to chat-routing-service's HTTP API (a route's request or
response shape) should land here as a new type or method, or a major
version bump, rather than silently changing an existing one out from under
pinned callers.
