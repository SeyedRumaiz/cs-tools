# til-backend

Backend for "Today I Learned" — a company-wide feed of learnings from
customers, partners, and internal sources. The One WSO2 `/me/til` page is
the only entry point allowed to create an entry — `POST /submissions`
rejects any caller whose verified token identity isn't the webapp's own
Asgardeo client id (`ONE_WSO2_WEBAPP_CLIENT_ID`).

## Run locally

```bash
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
cp .env.example .env   # fill in DB_PASSWORD, ASGARDEO_JWKS_URL / ASGARDEO_ISSUER / TIL_MODERATOR_GROUP / ONE_WSO2_WEBAPP_CLIENT_ID

# Prerequisite, once per database -- db.py only ever verifies this table
# exists, it never creates it (same posture as the PAR Legacy Migration
# Tool's own sql/create_migration_tables.sql):
mysql -h localhost -u root -p -e "CREATE DATABASE IF NOT EXISTS til"
mysql -h localhost -u root -p til < sql/create_til_tables.sql

uvicorn main:app --reload --port 8077
```

Without a real `.env`, every endpoint fails closed with 503 ("Authentication
is not configured") rather than silently allowing requests through — verified
live, not assumed. Starting the app against a database missing
`til_submissions` fails loudly too (a clear `RuntimeError` naming the exact
SQL file to run), rather than silently creating the table itself.

## Test

```bash
python3 -m pytest -q
```

Tests, all passing: validation rules (`test_validation.py`), HTML
sanitizing (`test_sanitize.py`), the MySQL storage layer (`test_db.py`),
and the request-handling rules — including the webapp-only submission gate
— in `test_main.py`. Both DB-touching suites run against a real local
`til_test` database (`conftest.py` creates it and its schema automatically,
and truncates between tests) — the same MySQL server as the real `til`
database, never that database itself.

## API

| Method | Path | Notes |
|---|---|---|
| GET | `/user-info` | `{ email, displayName, canModerate }` |
| GET | `/customers/search?q=` | `[{ id, name }]`; backs the Customer autocomplete, `[]` if `ENTITY_SERVICE_*` isn't configured |
| GET | `/submissions?limit=&cursor=&q=&scope=&dateFrom=&dateTo=&mine=` | Newest first; `nextCursor` is `null` on the last page. `q`/`scope` search (`scope` one of `what`, `title`, `who`, `email`, `whereDetail`; default `what`), `dateFrom`/`dateTo` are `YYYY-MM-DD` (400 if not a valid date), `mine=true` restricts to the caller's own entries |
| POST | `/submissions` | `{ title, who, where, what, whereDetail? }`; 403 unless the caller's verified token identity is the One WSO2 webapp's own Asgardeo client id |
| GET | `/submissions/{id}` | A single entry |
| DELETE | `/submissions/{id}` | 403 unless the caller is in `TIL_MODERATOR_GROUP` or is the entry's own submitter |
| POST | `/uploads` | `multipart/form-data` with a `file` field (image, ≤5 MB); `{ url }`; same webapp-only gate as `POST /submissions` |
| GET | `/health` | For Choreo/liveness checks |

## Design notes

- **Storage**: MySQL (`db.py`, via PyMySQL — same connection-pool pattern as
  the PAR Legacy Migration Tool's own `db.py`). The app never creates
  `til_submissions` itself; `sql/create_til_tables.sql` is a prerequisite an
  operator runs once per database, and `init_db()` just verifies it exists,
  failing loudly (not auto-creating) if it doesn't.
- **Auth**: same pattern as the PAR Legacy Migration Tool's `auth.py` in
  `digiops-hr` — independently verifies the JWT's real signature against
  Asgardeo's own JWKS, never trusts a forwarded header blindly. Fails closed
  (503) if `ASGARDEO_JWKS_URL`/`ASGARDEO_ISSUER`/`TIL_MODERATOR_GROUP` are
  unset, rather than defaulting open.
- **No anonymous entries**: `submittedByEmail` is always derived from the
  verified token, never the request body.
- **Webapp-only submission**: the One WSO2 webapp is the only entry point
  allowed to create an entry — `POST /submissions` checks the caller's
  verified token identity (`aud`/`client_id`/`azp`) against
  `ONE_WSO2_WEBAPP_CLIENT_ID` and rejects everything else with a 403. There
  is no other submission path.
- **Notification**: a successful submission broadcasts into every connected
  employee's own 1:1 DM with Novera (`novera_notify.py`) — the only
  notification channel this service has.

## Still needed before a real deployment (not achievable from this environment)

1. A real Choreo (or equivalent) deployment, with `ASGARDEO_JWKS_URL`,
   `ASGARDEO_ISSUER`, `TIL_MODERATOR_GROUP`, and `ONE_WSO2_WEBAPP_CLIENT_ID`
   set to the real values — the moderator group especially needs an actual
   decision on who moderates.
2. A real MySQL database provisioned per environment (staging/prod), with
   `sql/create_til_tables.sql` run against it once and `DB_HOST`/`DB_PORT`/
   `DB_USER`/`DB_PASSWORD`/`DB_NAME` set to match.
