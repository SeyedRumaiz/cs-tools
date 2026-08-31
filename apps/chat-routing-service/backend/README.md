# Chat Routing Service

A standalone Go service that decides **which engineer** (if any) picks up a
live-chat escalation from the customer portal. It sits between
`csm-portal/backend` — which still owns every WebSocket/SSE connection and
every case/session HTTP route — and PostgreSQL, where engineer presence,
per-customer sticky routing, and the waiting queue are persisted.

Before this service existed, an escalation was broadcast to *every*
connected engineer and "accepting" was first-`PATCH`-wins. This service
replaces that with real state: engineers declare themselves
`AVAILABLE` / `BUSY` / `OFFLINE`, an escalation is routed to exactly one
engineer — preferring whoever that customer last talked to, then falling
back to whoever's least busy today — and anyone who arrives while all
engineers are busy waits in a FIFO queue that drains automatically as
engineers free up.

## Architecture

```mermaid
flowchart TB
    subgraph CUST["Customer side"]
        A["Customer browser<br/>customer-portal webapp<br/>escalate icon under Novera's reply"]
        C["customer-portal/backend-v2<br/>REST :8080 · WS :8082"]
    end

    subgraph ENGS["Engineer side — CSM portal"]
        B["Engineer browser<br/>csm-portal webapp<br/>presence dropdown · alert notification"]
    end

    subgraph CSM["csm-portal/backend :8083 — orchestrator"]
        D["internal/handler/chat.go<br/>Escalate · SetPresence · Decline · CompleteSession<br/><br/>internal/handler/chat_stream.go<br/>SSE hub: per-engineer key + broadcast fallback"]
    end

    subgraph ROUTE["chat-routing-service :9096 — this service"]
        F["internal/router.Router<br/>Escalate · SetPresence · Completed · Decline · GetPresence"]
    end

    subgraph PG["Persistence"]
        G[("chat_routing DB<br/>engineers · customer_engineer_assignments<br/>escalation_queue")]
    end

    H["entity-service :8081<br/>case / conversation CRUD — unrelated, unchanged"]
    I[("entity-service's own DB<br/>cases table")]

    A -->|"1 · click escalate icon"| C
    C -->|"2 · relay, internal token"| D
    D -->|"3 · POST /route/escalate"| F
    F -->|"4 · sticky check, then least-busy pick, or enqueue"| G
    F -->|"5 · engineer email, or queue position"| D
    D -->|"6a · SSE to that one engineer"| B
    D -->|"6b · queued → WS event"| C
    C -->|"7 · 'you're #N in queue'"| A
    B -.->|"presence / decline / complete"| D
    D -.->|"best-effort route calls"| F
    D --> H
    H --> I
```

`chat-routing-service` never touches conversation content and never opens a
socket to a client — every call into it is a synchronous HTTP request from
`csm-portal/backend`, and its response is relayed back through whichever
transport `csm-portal/backend` already owns (SSE to engineers, the existing
WebSocket relay to customers). Case and message content stays entirely in
`entity-service`'s own database; this service owns exactly three tables of
its own — see [Data model](#data-model) below.

## Escalation walkthrough

1. **Customer clicks the escalate icon.** It renders directly under any
   Novera reply — the greeting, a normal answer, or an error bubble — not
   only a successfully completed one.
2. **backend-v2 relays it** to `csm-portal/backend` over its existing
   internal-token-authenticated call.
3. **`csm-portal/backend` calls `POST /route/escalate`** on this service.
4. Inside **one Postgres transaction**, the router picks who gets the case
   — see [Assignment priority](#assignment-priority) — or, if nobody
   qualifies, appends it to `escalation_queue` under a table lock and
   computes its position. Every step runs `FOR UPDATE` / `FOR UPDATE SKIP
   LOCKED`, so concurrent requests can never be handed the same engineer or
   double-count a queue slot.
5. The result — an engineer's email, or `{queued: true, position: N}` — goes
   back to `csm-portal/backend`.
6. `csm-portal/backend` relays the outcome: an assignment becomes an SSE
   push to that **one** engineer's private hub key (not a broadcast); a
   queue placement becomes a WebSocket event to the customer.
7. The customer sees a plain bot message — "you're #N in the queue" —
   appended to the same chat thread.

Presence changes, session completion, and a dismissed alert all follow the
same shape as steps 3–5, just against `POST /route/presence`,
`POST /route/completed`, and `POST /route/decline` respectively.

## Assignment priority

`Router.Escalate` picks an engineer in this order, falling through to the
next tier only when the previous one comes up empty:

| Priority | Rule | Notes |
|---|---|---|
| 1 · Sticky | The engineer this customer was **most recently assigned to**, if that engineer is `AVAILABLE` and idle right now. | Looked up from `customer_engineer_assignments`. A `BUSY` or fully `OFFLINE` sticky engineer is skipped entirely — this reconnects a returning customer to a familiar face *when possible*, it never makes them wait on one specific person. |
| 2 · Least busy | Whichever `AVAILABLE` engineer has been assigned the **fewest chats today**. | "Today" is a lazy, per-row reset (`chats_today_date = CURRENT_DATE`) rather than a scheduled midnight job — a stale count from a previous day is simply treated as `0` the next time anyone looks. |
| 3 · Tie-break | Among engineers tied on chats today, whoever has been `AVAILABLE` the **longest** (`available_since` ascending). | This is the same "came online first" ordering the original FIFO design used for everyone — now it only kicks in on a tie. |
| 4 · Queue | If no engineer is `AVAILABLE` at all, the case joins `escalation_queue` (FIFO tail). | Unaffected by the above — once an engineer frees up, they take the queue head directly (see `SetPresence` / `Completed` below); sticky/least-busy ranking isn't re-applied to queued cases. |

Every successful assignment — sticky match, least-busy pick, a queue drain,
or a `Decline` reassignment — updates that customer's sticky-engineer
record and bumps the assigned engineer's daily count (`Router.
assignCaseToEngineer` does both in the same transaction as the assignment
itself, so they can never drift out of sync with reality). `Decline`'s own
reassignment step reuses tier 2/3 (least-busy, tie-broken by online time)
excluding the declining engineer — it does not re-check the customer's
sticky engineer.

## Presence state machine

| From | Request | Result |
|---|---|---|
| any, idle | `AVAILABLE` | joins the pool (`available_since` set); if the queue is non-empty, immediately pops the head and assigns it (engineer becomes `BUSY`) |
| any, idle | `BUSY` | manual "do not disturb" — out of the pool, no case |
| any, idle | `OFFLINE` | out of the pool |
| mid-session (`current_case` set) | `OFFLINE` requested | **deferred** — `pending_offline` is set, engineer stays `BUSY` until the session ends |
| mid-session | `AVAILABLE`/`BUSY` requested | just clears `pending_offline` if it was set; the session itself is untouched (one dedicated session, can't free early) |

**On session completion:** if `pending_offline` was set, the engineer goes
straight to `OFFLINE` and does **not** rejoin the pool or take a queued
case. Otherwise, the engineer becomes `AVAILABLE`, rejoins the pool, and the
router immediately tries to hand them the queue head.

**On decline** (dismissing an alert before accepting): the declining
engineer is freed exactly like a completion, then the case is either handed
to the next-best available engineer by the same least-busy/tie-break
ranking (excluding the decliner), or pushed back onto the **front** of the
queue — the customer already waited once.

## Data model

Three tables, in their own database (`chat_routing`), separate from
`entity-service`'s database and its flat `cases` table:

| Table | Holds | Ordering / lookup |
|---|---|---|
| `engineers` | `status`, `pending_offline`, `current_case_id` / `current_case` (JSONB), `available_since`, `chats_today`, `chats_today_date` | `available_since` — oldest-idle-first (tier-3 tie-break); `chats_today` (lazily reset per `chats_today_date`) — least-busy-first (tier 2) |
| `customer_engineer_assignments` | one row per customer: `customer_email` → `engineer_email` last assigned | keyed on `customer_email` — the tier-1 sticky lookup |
| `escalation_queue` | waiting cases (`case_id`, `case_info` JSONB) | `order_key` — appended at the tail; a decline re-inserts at the head |

`case_info` / `current_case` are stored as JSONB blobs (`router.CaseInfo` —
`caseId`, `conversationId`, `projectId`, `subject`, `customerEmail`,
`customerName`, `message`) rather than normalized columns: this service
never queries *into* that blob by any field other than `caseId`, and it
means `csm-portal/backend` can add a field without a migration here.
`customer_engineer_assignments.customer_email` is a real column precisely
because it *is* queried on — sticky routing depends on it.

See `migrations/000001_create_engineers.up.sql`,
`migrations/000002_create_escalation_queue.up.sql`, and
`migrations/000003_add_sticky_routing_and_chat_counts.up.sql` for the exact
schema, including the check constraints that keep
`current_case_id`/`current_case` consistent and `available_since`
meaningful only while truly idle-and-free.

## HTTP surface

All routes below `/route/*` require the `X-Routing-Service-Token` header
(shared secret with `csm-portal/backend`'s `internal/routingclient`).
`GET /health` is unauthenticated, for local liveness checks only.

| Method | Path | Body / params | Response |
|---|---|---|---|
| `POST` | `/route/escalate` | `CaseInfo` fields | `{engineerEmail}` or `{queued:true, position:N}` |
| `POST` | `/route/presence` | `{email, status}` | `{applied, pendingOffline?, assignedCase?}` |
| `POST` | `/route/completed` | `{email}` | `{removed, rejoined, assignedCase?}` |
| `POST` | `/route/decline` | `{email, caseId}` | `{reassignedTo?, requeued?}` |
| `GET` | `/route/presence/{email}` | — | `{status}` (defaults to `OFFLINE` for an unseen engineer) |
| `GET` | `/route/debug/state` | — | full dump of `engineers` (incl. today's chat count) + `escalation_queue` — verification only |
| `GET` | `/health` | — | `200 OK` |

A storage/database error on any `/route/*` call returns `502` — the real
error is logged server-side (`slog`) and never sent to the caller.

## Configuration

Loaded from the environment (a `.env` file is read first if present — see
`.env.example`):

| Variable | Meaning |
|---|---|
| `ROUTING_SERVICE_PORT` | bare port number, default `9096` |
| `ROUTING_SERVICE_TOKEN` | shared secret; must match `csm-portal/backend`'s value |
| `DB_HOST`, `DB_PORT` | this service's own Postgres server, default `localhost:5432` |
| `DB_USER`, `DB_PASSWORD`, `DB_NAME` | credentials and database name — use a **dedicated** database (e.g. `chat_routing`), even if it's the same Postgres server `entity-service` already uses |
| `DB_SSLMODE` | default `disable` (local dev) |

Note: "today" for the daily chat count is `CURRENT_DATE` as the database
server sees it — if that server isn't in UTC, the daily reset happens at
that server's local midnight, not the customer's or engineer's.

## Running locally

```bash
# one-time: create this service's own database
psql -U <db-user> -h localhost -c "CREATE DATABASE chat_routing;"

# apply migrations (golang-migrate CLI)
migrate -path migrations \
  -database "postgres://<db-user>:<db-password>@localhost:5432/chat_routing?sslmode=disable" up

# run
go run ./cmd/server
```

`GET /route/debug/state` on a fresh database should show empty `engineers`,
`available`, and `queue`.

## Resilience & known limitations

- **Routing-service-down fallback:** if this service is unreachable,
  `csm-portal/backend` falls back to its old broadcast-to-every-connected-
  engineer SSE key for `Escalate`, so a customer is never silently stranded.
  Presence/completed/decline calls don't need this — they aren't
  "a customer needs to reach someone right now" moments — and are
  best-effort/logged on failure instead.
- **No timeout or reassignment** if an assigned engineer never accepts the
  session or goes unreachable — a prototype gap that predates and is
  unrelated to persistence.
- **Sticky routing is a preference, not a lock.** If a customer's last
  engineer left the company or their email changes, the stale sticky row
  simply never matches `AVAILABLE` and every future escalation from that
  customer falls straight through to tier 2 — no cleanup job needed.
- **Single-process is the only tested topology.** Every state transition is
  a Postgres transaction rather than an in-process mutex, so multiple
  replicas of this service *should* be safe against the same database, but
  that hasn't been load-tested.
- Restarting this service no longer loses in-flight routing state — that's
  the reason it moved off an in-memory map onto Postgres.

## Related

- `apps/csm-portal/backend/internal/routingclient` — the (only) caller of
  this service's HTTP surface.
- `apps/csm-portal/backend/internal/handler/chat.go` and `chat_stream.go` —
  where the SSE fan-out and WebSocket relay this service's decisions ride on
  actually live.
- `docs/architecture.excalidraw` — a hand-drawn version of an earlier cut of
  this diagram, for whiteboard-style walkthroughs (predates tiers 1–3 above
  — the queue/broadcast-fallback shape is unchanged, but it still shows
  plain FIFO assignment).
