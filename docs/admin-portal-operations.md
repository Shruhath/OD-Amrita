# Admin portal operations

The web portal serves the authenticated event administration routes at
`/portal/events` (the events list) and `/portal/events/:event_id` (one event and
its categories). It uses Supabase only to broker Microsoft sign-in and obtain a
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

## Event categories

Opening an event from the events list shows the categories it already has and,
for an active event, a form that adds one category per submission. Categories
scope coordinators and OD records to a specific activity within an event
(Procession, Stage Setup, …).

Operational rules an administrator will notice:

- **Create and list only.** There is deliberately no rename, reorder or delete.
  Categories will be referenced by coordinator assignments, OD records, disputes
  and tallies, so they carry the same no-hard-delete posture as events. Correcting
  a mistyped category is not currently possible from the portal.
- **One at a time.** Several categories are added by repeating the command; the
  form stays open and refocuses itself.
- **Unique within the event, not globally.** Names are compared
  case-insensitively after trimming, so `Procession`, `procession` and
  `  Procession  ` are the same category. Two different events may each have
  their own `Procession`. The uniqueness is enforced by a database index, not
  only by the form, so two administrators adding the same name at the same
  moment cannot both succeed.
- **Archiving closes the aggregate.** An archived event keeps showing its
  categories read-only, and the add form is removed entirely rather than
  disabled. The API refuses the write with `409 EVENT_ARCHIVED` regardless of
  what the browser offers, and the database refuses it independently of the API.
- **Every created category writes one audit row** (`category.created`, target
  table `categories`) in the same transaction as the category itself. A rejected
  create writes neither.

Verify the categories of an event as the migrator/database owner:

```sql
SELECT category.name, category.created_at
FROM categories AS category
JOIN events AS event ON event.id = category.event_id
WHERE event.name = 'YOUR EVENT NAME'
ORDER BY category.name, category.id;
```

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

For the category surface, open an active event, add two categories, repeat one of
the names in a different case and confirm it is rejected on the name field,
reload and confirm both survive, then archive the event and confirm the
categories render read-only with no add control. Confirm exactly two
`category.created` audit rows exist for that event — the rejected duplicate must
have contributed none.

This live smoke check has not been run against a deployed environment; the same
journey is executed against a real API and PostgreSQL by the `portal-integration`
CI job.
