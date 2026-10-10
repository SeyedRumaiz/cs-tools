# TIL Backend

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](../../LICENSE)
[![GitHub last commit](https://img.shields.io/github/last-commit/wso2-open-operations/cs-tools/main?path=apps%2Ftil-backend)](https://github.com/wso2-open-operations/cs-tools/commits/main/?path=apps/til-backend)
[![GitHub issues](https://img.shields.io/github/issues/wso2-open-operations/cs-tools.svg)](https://github.com/wso2-open-operations/cs-tools/issues)

TIL Backend is the FastAPI service behind "Today I Learned" — a company-wide
feed of learnings sourced from customers, partners, and internal work. Two
entry points call this one service: the One WSO2 `/me/til` page, and (once
registered) a Google Chat App's `+` Dialog.

## Why TIL?

Knowledge shared one-on-one in a customer call, a partner conversation, or a
hallway chat tends to stay with the person who learned it. TIL gives it a
place to land — a short, moderated, searchable feed anyone in the company can
post to and read, without turning it into a wiki page or a Slack thread no
one can find again.

## Features

- **Lightweight submissions** — `who` / `where` / `what`, sanitized server-side,
  no anonymous entries (the submitter is always the verified, signed-in user).
- **Customer-name autocomplete** — the "Where: Customer" field resolves against
  real customer names via [entity-service](../../entity-service)'s account
  search, instead of free-typed names drifting out of sync with the system of
  record.
- **Moderation** — deletion is restricted to a configured Asgardeo group, not
  open to every signed-in user.
- **Chat notifications** — new entries can post a card into a Google Chat
  Space via a plain Incoming Webhook, no Chat App required for that part.
- **Independent JWT verification** — every request's token is checked against
  Asgardeo's own JWKS; nothing is trusted from a forwarded header alone.

## Project Structure

```bash
.
└── backend                  # FastAPI service — submissions, moderation, customer search
    ├── chat_app/             # Google Chat App source (Code.gs) for the "+" Dialog entry point
    ├── sql/                  # Schema this service verifies (never auto-creates) at startup
    └── README.md             # Detailed backend documentation
```

There is no separate `webapp/` here — the primary UI is the One WSO2 `/me/til`
page, which lives in the One WSO2 webapp's own repository, not this one.

## Setup

See the [backend README](./backend/README.md) for local run instructions,
the full API surface, and the environment variables required for a real
deployment.
