# Admin portal operations

The web portal serves the authenticated event administration routes at
`/portal/events` (the events list), `/portal/events/:event_id` (one event, its
categories, and its coordinators), and `/portal/teachers` (the teacher roster).
It uses Supabase only to broker Microsoft sign-in and obtain a session JWT; all
authorization and event/teacher data decisions remain server-side.

The same app also serves the **coordinator marking surface** at the root route
`/mark`, which is coordinator-only and deliberately outside `/portal`. See
[Coordinator marking surface](#coordinator-marking-surface).

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
group OD records under a specific activity within an event (Procession, Stage
Setup, ...).

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

## Event coordinators

The same event detail page lists event-level coordinator assignments and, for an
active event, accepts a provisioned user's email address as the assignment input.
One assignment row authorizes that user to mark across all categories in the
event.

Operational rules an administrator will notice:

- **Existing user only.** The assignee must already have signed in once so a
  `users` row exists. Unknown email is rejected with `404 USER_NOT_FOUND`.
- **`student -> coordinator`, or existing coordinator.** Assigning a student
  atomically promotes the user to `coordinator` and creates the event assignment.
  A user already holding `coordinator` can be assigned to another event without a
  role change. `admin` and `teacher` are rejected with `409
  ROLE_ALREADY_ASSIGNED`.
- **Event-level scope.** There is no category selector in this MVP workflow. A
  coordinator assigned to an active event can read that event, its categories,
  and its mark status, and can submit marks for categories that belong to that
  event. Direct URL or API attempts against unassigned events are rejected.
- **Duplicate is a conflict, not idempotent success.** Repeating the same
  user/event assignment returns `409 COORDINATOR_ASSIGNMENT_EXISTS`; concurrent
  duplicate requests are also rejected by the database uniqueness constraint.
- **Archived events are read-only.** Existing assignments remain visible after
  archive, but the assignment form is removed. The API and database both refuse
  new assignments for archived events with `409 EVENT_ARCHIVED`.
- **One successful assignment writes one audit row.** The role elevation if any,
  the assignment row, and a `coordinator.assigned` audit row targeting
  `event_coordinator_assignments` commit in the same transaction. Rejected
  assignment requests write no audit row.

Verify coordinators for an event as the migrator/database owner:

```sql
SELECT u.email, u.name, assignment.created_at
FROM event_coordinator_assignments AS assignment
JOIN users AS u ON u.id = assignment.user_id
JOIN events AS event ON event.id = assignment.event_id
WHERE event.name = 'YOUR EVENT NAME'
ORDER BY assignment.created_at, u.email, assignment.id;
```

## Fallback credits

The event detail page also shows **Fallback credits** for the same assigned
coordinators. This is one event-wide cap per coordinator for the fallback mark
paths: **Scan camera** and **Search** share the same pool. **Tap card/NFC never
uses this pool** and remains available when fallback credits are exhausted.

Operational rules an administrator will notice:

- **Default cap is 5.** Until an administrator saves a value for the event, each
  assigned coordinator effectively has 5 fallback credits.
- **Cap 0 is valid.** Setting the cap to 0 live-disables camera and search for
  that event without redeploying. Raising the cap takes effect on the next mark
  status read and the next mark request.
- **Lowering below usage is allowed.** If a coordinator has consumed 4 credits
  and the cap is lowered to 3, their remaining value is 0 and camera/search stay
  disabled until a reset or later raise.
- **Reset is per coordinator.** Resetting one coordinator sets only that
  coordinator's consumed count to 0. The UI applies the server response without
  a page reload.
- **Archived events are read-only.** Existing credit state remains visible for
  review, but cap changes and resets are removed from the UI and rejected by the
  API/database with `409 EVENT_ARCHIVED`.
- **Rejected writes do not audit.** Unknown events, non-admin callers, invalid
  caps, unknown users, and users not assigned to the event write no credit audit
  row.
- **Successful admin writes are audited.** A cap change writes exactly one
  `capture_credit.cap_changed` row targeting `event_capture_credit_config`.
  A reset writes exactly one `capture_credit.reset` row targeting
  `event_capture_credit_usage`.

Verify fallback credits for an event as the migrator/database owner:

```sql
SELECT u.email,
       COALESCE(usage.consumed, 0) AS consumed,
       COALESCE(config.credit_limit, 5) AS cap,
       GREATEST(COALESCE(config.credit_limit, 5) - COALESCE(usage.consumed, 0), 0) AS remaining
FROM event_coordinator_assignments AS assignment
JOIN users AS u ON u.id = assignment.user_id
JOIN events AS event ON event.id = assignment.event_id
LEFT JOIN event_capture_credit_config AS config ON config.event_id = assignment.event_id
LEFT JOIN event_capture_credit_usage AS usage
  ON usage.event_id = assignment.event_id AND usage.user_id = assignment.user_id
WHERE event.name = 'YOUR EVENT NAME'
ORDER BY assignment.created_at, assignment.id;
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

## Coordinator marking surface

The marking screen lives at the root route **`/mark`**, not under `/portal`.
`/portal/*` is the administration surface and its navigation is admin-only, so a
coordinator can open none of it; a signed-in coordinator landing on `/` or an
unknown path is redirected to `/mark` rather than to an "Access denied" screen.

**Coordinator role only.** A teacher, admin or student who opens `/mark` sees an
explicit access state and the screen issues no request at all — not the scoped
reads, not the mark, not the counter poll. Teacher and admin marking is a later
story: it needs an explicit slot picker that this screen deliberately does not
have, so admitting an admin here would put them on a screen whose every mark the
server then rejects.

Production provisions every first-time user as `student`. An administrator must
assign that provisioned user on an active event detail page before the user can
mark as a coordinator. The first assignment promotes the user to `coordinator`;
later assignments add more event scopes. A coordinator can see only assigned
active events on `/mark`, and mark writes are server-checked against the same
assignment table. Roster search remains role-gated rather than event-scoped; a
search hit does not authorize a mark.

### Device setup

The coordinator's phone, before the event:

1. **Android with Chrome.** Web NFC is Chrome-on-Android only and is not
   Baseline. iOS and desktop browsers cannot tap cards; they can still use the
   camera and search modes where supported.
2. **NFC switched on** in Android settings. Feature detection cannot prove the
   radio is on — a promise simply never settles until it is.
3. **The deployed HTTPS origin**, not an IP or a plain-HTTP page. Both Web NFC
   and `getUserMedia` are secure-context-only. The screen reports an insecure
   origin distinctly from an unsupported browser, because the fixes differ.
4. **Camera permission** granted when first prompted. A refusal is an explicit
   red state with a retry, never a silently dead button.
5. **Signed in** with the university Microsoft account whose `users.role` is
   `coordinator`.

### Using the screen

Pick the event and category — both lists come from the server and contain only
what this coordinator may mark for — then choose a capture mode:

| Mode | What it reads | What is sent |
|---|---|---|
| **Tap card** | The `{version, token}` written to the NFC tag by the card populator | The raw payload, `capture_method: nfc`. The token is never resolved, hashed or stored on the device. |
| **Scan camera** | The barcode **printed on the physical ID card**, which carries the **roll number** — not a sealed token | The roll is resolved to a student through the search endpoint, then `student_id` with `capture_method: camera`. |
| **Search** | Typed roll number or name prefix, minimum 3 characters | `student_id` with `capture_method: search`. |

Camera and search consume one shared fallback credit after the server has
accepted the coordinator's event/category scope and before the OD row is written.
That debit, the OD row, and the `od.created` audit row are one transaction, so a
duplicate, closed slot, unknown student, archived event, validation failure, or
downstream write failure does not burn credit. An exhausted camera/search
attempt returns `CAPTURE_CREDIT_LIMIT_REACHED`, writes no OD row, consumes no
credit, and writes one `scan.blocked` audit row with
`reason = capture_credit_exhausted`.

The tap mode runs one continuous scan: several students in a row each get their
own confirmation with no button press between them, and an identical token
repeated within about two seconds fires only once.

### Reading the banner

The banner is the only signal that matters, and it never auto-dismisses.

- **Green** means the server committed the mark and returned 201 — nothing
  earlier. It carries the student's display name, roll number and the slot. There
  is no optimistic green: a mark the server refused never shows green at all.
- **Red** means the mark was **not recorded**, with the specific reason —
  already marked, card not recognised, slot closed, event archived, unknown
  student, not permitted, or a network failure.
- **There is no amber.** A timeout or a network failure is red. Nothing is
  queued, nothing retries in the background, and nothing is saved locally; the
  coordinator retries by hand.

Two counters, the fallback credit state, and the slot clock are all
server-computed and refresh about every five seconds: the coordinator's own
marks, the combined total for the same event and category across all
coordinators, remaining fallback credits for camera/search, and the time left in
the active slot. Outside the slot windows the screen says **"No slot is open
right now"** rather than showing a countdown, and the server rejects marks
attempted in that state.

Nothing about a card, token, search result or roster is written to
`localStorage`, `sessionStorage` or IndexedDB. Only the selected event and
category may persist — an event id is public, not identity data.

### Physical marking smoke check

Automated tests fake both the radio and the camera; only real hardware proves
those paths. On a real Android phone in Chrome, against the deployed HTTPS
origin, **inside a slot window on the event date** (slots run 08:00–18:15 IST —
outside them the server correctly refuses every mark):

1. Sign in as a coordinator and open `/mark`. Confirm the event and category
   selectors are populated and all three modes are offered.
2. Tap a card written by the card populator. Confirm a **green** banner carrying
   that student's own name, and that the "marked by you" counter increases by one.
3. Tap the **same** card again. Confirm a **red** already-marked banner and that
   neither counter moves.
4. Switch to camera mode and scan the barcode printed on a real ID card. Confirm
   the same green confirmation for the right student and one fewer fallback
   credit.
5. From the event detail page, set the fallback cap to 0. Confirm camera/search
   are disabled on `/mark`, while NFC remains selectable. Raise the cap or reset
   the coordinator and confirm camera/search return without redeploying.
6. Turn airplane mode on and tap a card. Confirm a **red** "not recorded" state
   with a retry — never a queued or amber state.

Verify the marks as the migrator/database owner:

```sql
SELECT s.roll_no, s.name, o.slot, o.capture_method, o.server_received_at
FROM od_records AS o
JOIN students AS s ON s.id = o.student_id
WHERE o.event_id = 'YOUR-EVENT-ID'
ORDER BY o.server_received_at;
```

`capture_method` is persisted truthfully, so a camera mark reads `camera` and
never `search`; both camera and search consume the same fallback credit pool,
while `nfc` consumes none.

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
