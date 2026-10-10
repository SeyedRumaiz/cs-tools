# Internal API users

`internal/auth` provides PBKDF2-hashed (10000 iterations, random salt) service-account credentials backed by the `integration_users` PostgreSQL table. sre-alert-ingestion-service checks vendor webhooks against them (`AUTH_ENABLED`); this service only owns the table and the `cmd/user` tool.

`POST /alertz` does not use these users. It takes the shared `ALERT_CORE_WAKE_TOKEN` instead (see [Wake token](#wake-token)).

sre-alert-ingestion-service doesn't query `integration_users` per request. It keeps an in-memory copy and reloads it every `postgres.auth_refresh_interval` (30s by default), so a user you create, disable, enable or rotate here takes effect within that interval. If reloads keep failing, the last good copy is used for up to `postgres.auth_max_stale` (15m), after which requests get 503 until a reload succeeds.

Secrets are never stored in plaintext; only the PBKDF2 hash and salt live in PostgreSQL. Each row also tracks who provisioned it, when it was last modified, and when its secret was last rotated, so accounts behave closer to real identity records rather than a bare credential pair. There's no admin API or startup seeding, so accounts are managed one at a time with `cmd/user`, run against the same PostgreSQL database and `PG*` env vars the server itself uses.

## cmd/user

```bash
go run ./cmd/user create -username <name> [-secret <value>] [-created-by <who>]
go run ./cmd/user list [-username <name>]                    # summary table, or one user's full detail
go run ./cmd/user enable -username <name>                    # re-enable a disabled user
go run ./cmd/user disable -username <name>                   # disable a user
```

Load `PG*` from `.env` first (it's gitignored):

```bash
set -a && source .env && set +a
```

## Creating a user

```bash
go run ./cmd/user create -username webhook-integration-user
```

With no `-secret`, one is generated and printed once:

```
created internal user "webhook-integration-user"
secret (shown once, store securely): <generated-secret>
```

Copy it into your secrets manager immediately; it is not recoverable afterwards, only the hash is stored. A stable `id` (UUID) is generated for the account at this point and never changes again, even across rotations.

By default `created_by` is your `$USER` env var (falling back to `"unknown"`); pass `-created-by <who>` to set it explicitly, e.g. for an automated pipeline.

Users never expire: a user stays valid until it is disabled or its secret is rotated.

## Setting a specific secret

```bash
go run ./cmd/user create -username webhook-integration-user -secret 'my-chosen-secret'
```

Prefer reading the value from a file or env var (e.g. `-secret "$(cat secret.txt)"`) over typing it inline, since inline arguments land in your shell history.

## Rotating a secret

Re-running `create` for the same `-username` overwrites that row (upsert), but preserves its identity: `id`, `created_at`, and `created_by` stay exactly as they were (`created_by` only changes if you explicitly pass `-created-by` again). Only `secret_hash`, `salt`, `secret_rotated_at`, and `updated_at` change. To rotate: generate or pick a new secret, re-run the command, then update the caller's stored credential to match. sre-alert-ingestion-service picks the new secret up at its next reload (within `postgres.auth_refresh_interval`, 30s). Until then the old secret still works and the new one doesn't, so update the caller about 30s after rotating, and expect a few 401s if it switches earlier.

## Listing users

```bash
go run ./cmd/user list
```

```
USERNAME                       ENABLED  CREATED_BY
webhook-integration-user       true     thevindu
```

For full detail on one user (including `id`, `updated_at`, `secret_rotated_at`, and `last_used_at`):

```bash
go run ./cmd/user list -username webhook-integration-user
```

```
username:           webhook-integration-user
id:                 3f9c1e2a-...
enabled:            true
iterations:         10000
created_at:         2026-09-29T10:00:00Z
created_by:         thevindu
updated_at:         2026-09-29T10:00:00Z
secret_rotated_at:  2026-09-29T10:00:00Z
last_used_at:       -
```

`last_used_at` stays `-` (unset); nothing currently writes to it. Only metadata is shown in either view; `secret_hash`/`salt` are never printed.

## Enabling / disabling a user

```bash
go run ./cmd/user disable -username webhook-integration-user
go run ./cmd/user enable -username webhook-integration-user
```

sre-alert-ingestion-service rejects any webhook from a disabled user with a generic 401, from its next reload of `integration_users` (within 30s). Disabling keeps the row (and its hash) intact, so re-enabling doesn't require issuing a new secret. `updated_at` is bumped either way.

## Authenticating

Vendor webhooks to sre-alert-ingestion-service can authenticate with either header form:

```bash
# Bearer, base64("username:secret")
TOKEN=$(printf '%s:%s' webhook-integration-user '<secret>' | base64 | tr -d '\n')
curl -X POST https://<ingestion-host>/api/wso2/v1/sre_alert_api/<source> -H "Authorization: Bearer $TOKEN" -d @payload.json

# Basic, via curl's -u
curl -X POST https://<ingestion-host>/api/wso2/v1/sre_alert_api/<source> -u webhook-integration-user:<secret> -d @payload.json
```

## Wake token

`POST /alertz` is called only by sre-alert-ingestion-service, so it is guarded by one shared secret rather than a user. Generate it once:

```bash
openssl rand -hex 32
```

Set the output as `ALERT_CORE_WAKE_TOKEN` (a secret) on **both** services, per environment. Ingestion sends it as `Authorization: Bearer <token>`, only over https. It must be at least 32 characters, or this service won't start. Without it, every wake gets a 401, and alerts are still picked up by the `poll.interval` backstop, just later.

```bash
curl -X POST https://<core-host>/alertz -H "Authorization: Bearer $ALERT_CORE_WAKE_TOKEN"   # 202
```

To rotate, set the new value on both services and redeploy them together; wakes in between get 401 and fall back to the poll.

## Schema

```sql
CREATE TABLE IF NOT EXISTS integration_users (
  username          text PRIMARY KEY,
  id                uuid NOT NULL DEFAULT gen_random_uuid(), -- stable identity id, generated once at creation
  secret_hash       text NOT NULL, -- base64 PBKDF2-SHA256 derived key
  salt              text NOT NULL, -- base64 random salt, unique per user
  iterations        int  NOT NULL, -- PBKDF2 iteration count used for this row
  enabled           boolean NOT NULL DEFAULT true,
  created_at        timestamptz NOT NULL DEFAULT now(), -- account creation time, stable across rotations
  created_by        text NOT NULL DEFAULT '', -- operator who provisioned it
  updated_at        timestamptz NOT NULL DEFAULT now(), -- bumped on every create/rotate/enable/disable
  secret_rotated_at timestamptz NOT NULL DEFAULT to_timestamp(0), -- bumped only when secret_hash/salt actually change
  last_used_at      timestamptz NOT NULL DEFAULT to_timestamp(0) -- nothing writes it; always unset today
);
```
