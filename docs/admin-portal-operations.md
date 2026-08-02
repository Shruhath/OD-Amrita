# Admin portal operations

The web portal serves the authenticated event administration routes at
`/portal/events` (the events list), `/portal/events/:event_id` (one event and
its categories), and `/portal/teachers` (the teacher roster). It uses Supabase
only to broker Microsoft sign-in and obtain a session JWT; all authorization
and event/teacher data decisions remain server-side.

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
  what the browser offers, and the database refuses it independently of the API —
  including against a concurrent archive, because the guard locks the event row
  it checks rather than reading it and hoping.
- **If an event is archived while its detail page is open**, the next add is
  rejected with `409 EVENT_ARCHIVED` and the page re-reads the event, so the add
  form disappears instead of inviting a retry that can never succeed.
- **The detail page reads its event by id** (`GET /api/v1/admin/events/{id}`), so
  it stays correct no matter how many events exist. It does not depend on the
  event appearing in any particular page of the events list.
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

## Teacher profiles

Opening `/portal/teachers` shows the current teacher roster and a form that
elevates an already-signed-in user to teacher, associating one or more branches
in the same request.

Operational rules an administrator will notice:

- **Elevation, not pre-provisioning.** A teacher profile can only be created for
  an email that already has a `users` row — i.e. someone who has signed in to
  the portal at least once. `users.supabase_subject` is the sole identity key
  (`email` is deliberately not unique), so there is no way to create a row ahead
  of a person's first login. An unknown email is rejected with `404
  USER_NOT_FOUND`; the operational workaround is to have the teacher sign in
  once first.
- **`student -> teacher` only.** A user who is already `admin`, `teacher`, or
  `coordinator` is rejected with `409 ROLE_ALREADY_ASSIGNED`, including a
  repeat call for someone already made a teacher. There is no tiered promotion
  path and no role-reversal endpoint.
- **Create + list only.** There is no endpoint to edit an existing teacher's
  branches or to remove a teacher profile once created. Correcting a mistake
  is not currently possible from the portal.
- **One request, one audit row.** The role elevation and every branch
  association from a single request co-commit as exactly one
  `teacher.profile_created` audit row (target table `users`). A rejected create
  — unknown email, already-elevated role, unknown branch — writes nothing.
- **Paginated like events.** The roster scales with institution headcount, so
  `GET /api/v1/admin/teachers` is paginated (`page`/`page_size`/`total`), unlike
  the unpaginated branch/category reference data.

Verify a teacher profile as the migrator/database owner:

```sql
SELECT u.email, u.name, b.code
FROM users AS u
JOIN user_branches AS ub ON ub.user_id = u.id
JOIN branches AS b ON b.id = ub.branch_id
WHERE u.role = 'teacher'
ORDER BY u.email, b.code;
```

## Card populator

Opening `/portal/card-populator` shows a branch/batch/section selector, the
database-backed roster for that section, and per-student **write** and **verify**
actions. It is the surface that puts a sealed token on each student's NFC card
before the event.

**It only works in Chrome on Android, over HTTPS.** Web NFC (`NDEFReader`) exists
in no other browser and is exposed only in a secure context, so the page reports
an explicit red state — *insecure origin* or *unsupported browser* — and disables
both card actions everywhere else. That is deliberate: a populator that silently
looked idle on a laptop would waste an afternoon. Chrome additionally requires
NFC switched on in Android settings, granted permission, and a tapped
NDEF-compatible tag before either operation completes.

Operational rules an administrator will notice:

- **The card carries `{version, token}` and nothing else.** No roll number, no
  name, no student id, no branch/section/batch, no key material. Identity is
  resolved server-side from the token, so a lost card discloses nothing about
  its owner. The HMAC key never reaches the browser.
- **Write and verify are separate steps, on purpose.** A resolved write only
  proves the phone believed it wrote. Verification re-reads the tag and asks the
  server whose token it is; a card is marked verified only when the resolved
  student matches the selected row. Nothing auto-advances — an off-by-one here
  means a student arrives with someone else's card.
- **A mismatch, unknown token, blank tag, or garbled payload is a red row**, kept
  in the list with its write and verify actions still enabled. Failed rows are
  never hidden or skipped.
- **Cards stay rewritable.** `makeReadOnly()` is deliberately not used — locking
  a tag is a one-way hardware action and would make a correction impossible.
- **One student at a time.** Starting an operation cancels any in-flight one, so
  a tap meant for the previous student cannot land on the next.
- **Progress is recoverable but token-free.** Per-student status is kept in
  `sessionStorage` under `od-amrita.card-populator.progress.v1` as status, a
  display note and a timestamp. No token, payload, or key material is ever
  persisted, logged, or included in an error message.
- **Sealing is per student.** There is no bulk export of sealed tokens, so the
  number of raw tokens the browser has ever held is bounded by the cards
  actually written.

### Physical card smoke check

Automated tests fake the radio; only a real tap proves the hardware path. On a
real Android phone, in Chrome, against the deployed HTTPS origin:

1. Enable NFC in Android settings and sign in as an administrator.
2. Open `/portal/card-populator`, pick a branch/batch/section, and load the
   roster. Confirm the students appear in roll-number order.
3. Select a student, press **Write card**, and hold a **blank NTAG** to the phone
   until the row reads *Written, awaiting verify*.
4. Press **Verify card** and tap the same card. The row must show the student's
   own name and roll number.
5. Tap a **different** student's card against the first student's verify action
   and confirm it is refused as belonging to someone else.

Verify the ledger as the migrator/database owner — the raw token is never stored,
only its fingerprint:

```sql
SELECT s.roll_no, s.name, t.version, t.created_at
FROM student_card_tokens AS t
JOIN students AS s ON s.id = t.student_id
ORDER BY s.roll_no;
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
go test -tags integration ./test/event/... ./test/teacher/... -v
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

For the teacher surface, elevate a seeded student-role user by email and branch,
reload and confirm it persists, attempt the same email again and confirm a
`409 ROLE_ALREADY_ASSIGNED` announced on the form, and attempt an unknown email
and confirm `404 USER_NOT_FOUND`. Confirm exactly one `teacher.profile_created`
audit row exists for the elevated user — every rejected attempt must have
contributed none.

For the card populator, run the physical card smoke check above. It is the only
step here that CI cannot stand in for: the browser suites fake the NFC radio, so
a real Chrome-on-Android tap against a blank NTAG is the sole evidence the
hardware path works.

This live smoke check has not been run against a deployed environment; the same
journeys (events, categories, teacher profiles, and the card populator's roster
load / seal / resolve path) are executed against a real API and PostgreSQL by the
`portal-integration` CI job. **The physical NFC tap remains unexercised** — there
is no deployed HTTPS origin yet (that is gated on the paid hosting steps) and no
Android device or blank NTAG in the build environment.
