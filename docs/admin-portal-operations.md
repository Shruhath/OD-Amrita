# Admin portal operations

The web portal serves the authenticated event administration route at
`/portal/events`. It uses Supabase only to broker Microsoft sign-in and obtain a
session JWT; all authorization and event data decisions remain server-side.

## Browser environment

Copy `web/.env.example` to `web/.env.local` and configure:

| Variable | Purpose |
|---|---|
| `VITE_SUPABASE_URL` | Public Supabase project URL used by the browser SDK. |
| `VITE_SUPABASE_ANON_KEY` | Public publishable/anonymous key. Never use a service-role key in the browser. |
| `VITE_API_BASE_URL` | HTTPS origin of the Go API, without an endpoint path. |

The API must list the portal origin exactly in `PORTAL_ALLOWED_ORIGINS`. Supply
multiple origins as a comma-separated list. Wildcards, paths, query strings,
and credentialed CORS are intentionally rejected.

For local development, a typical pairing is:

```dotenv
# web/.env.local
VITE_SUPABASE_URL=https://YOUR-PROJECT-REF.supabase.co
VITE_SUPABASE_ANON_KEY=YOUR_PUBLISHABLE_OR_ANON_KEY
VITE_API_BASE_URL=https://localhost:8443

# api/.env
PORTAL_ALLOWED_ORIGINS=http://localhost:5173
```

Run the portal with:

```bash
pnpm --filter od-amrita-web dev
```

## Identity prerequisite

An authenticated user must have a provisioned `users` row whose stored base
role is `admin`. The API does not trust role claims or browser state. Use the
existing bootstrap-admin configuration for the first approved administrator;
do not place a real administrator address in a committed environment file.

## Branch-data prerequisite

Event creation requires at least one row in `branches`. Migrations deliberately
create the normalized schema without inventing an institutional branch roster.
Before enabling the portal in an environment, an operator must import or insert
the authoritative branch codes and names from the institution's approved data
source.

Verify readiness as the migrator/database owner:

```sql
SELECT id, code, name
FROM branches
ORDER BY code, id;
```

An empty result is a valid but not operationally ready state. The portal reports
that no branches are available and disables event creation; it does not fall
back to free-text or browser-owned branch data.

## Verification commands

The default local verification does not require a database:

```bash
go test ./...
pnpm --filter od-amrita-web typecheck
pnpm --filter od-amrita-web lint
pnpm --filter od-amrita-web test
pnpm --filter od-amrita-web test:e2e
pnpm --filter od-amrita-web build
```

The database lifecycle suite uses a disposable database and drops it after the
run, including its immutable audit rows. It requires PostgreSQL roles, `goose`,
and all three DSNs:

```bash
SUPERUSER_DSN='postgres://...' \
DATABASE_URL_MIGRATOR='postgres://...' \
DATABASE_URL_RUNTIME='postgres://...' \
go test -tags integration ./test/event/... -v
```

CI supplies these values and executes this suite after bootstrapping the roles.
For a live smoke check, sign in as the approved administrator, create an event
with two branches, reload, archive it, reload again, and confirm one
`event.created` plus one `event.archived` audit row for that event. Also call an
admin event endpoint with a provisioned Student token and confirm `403
RBAC_FORBIDDEN`.
