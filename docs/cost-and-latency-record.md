# Database cost and latency — what was wrong, what was done, and why

**Period covered:** 2026-08-17 → 2026-08-18
**Surfaces touched:** `api/` (Go, gin), `web/` (React, TanStack Query), `contracts/`, CI workflows, deployment runbook
**Outcome:** two delivery passes, twelve commits, both surfaces deployed and verified in production

---

## Table of contents

1. [The problem, as originally stated](#1-the-problem-as-originally-stated)
2. [Method — how the investigation was actually run](#2-method--how-the-investigation-was-actually-run)
3. [What the monitoring export proved](#3-what-the-monitoring-export-proved)
4. [What only reading the code could reveal](#4-what-only-reading-the-code-could-reveal)
5. [The cost model — why any of this matters financially](#5-the-cost-model--why-any-of-this-matters-financially)
6. [Pass one — letting the database sleep](#6-pass-one--letting-the-database-sleep)
7. [Pass one — killing the write on every page load](#7-pass-one--killing-the-write-on-every-page-load)
8. [Pass one — the two per-request auth queries](#8-pass-one--the-two-per-request-auth-queries)
9. [Pass one — backing off the polling](#9-pass-one--backing-off-the-polling)
10. [Pass one — the two genuinely slow queries](#10-pass-one--the-two-genuinely-slow-queries)
11. [Pass one — bulk writes without a queue](#11-pass-one--bulk-writes-without-a-queue)
12. [Pass two — server-side caching for the authenticated surface](#12-pass-two--server-side-caching-for-the-authenticated-surface)
13. [Pass two — the idle polling guard](#13-pass-two--the-idle-polling-guard)
14. [Problems hit along the way, and how each was resolved](#14-problems-hit-along-the-way-and-how-each-was-resolved)
15. [Things deliberately NOT done, and why](#15-things-deliberately-not-done-and-why)
16. [Verification — what was actually run](#16-verification--what-was-actually-run)
17. [Results](#17-results)
18. [Still outstanding](#18-still-outstanding)
19. [Lessons worth keeping](#19-lessons-worth-keeping)

---

## 1. The problem, as originally stated

The hosting bill for the Neon Postgres instance had become disproportionate to
what the application does. This is a one-event attendance system: a few dozen
staff marking students present during teaching slots, plus a campus-wide read
burst for roughly an hour after the last slot ends. It is not a high-traffic
product. It is used intensely for a few hours and then not at all.

The bill did not reflect that shape. It reflected a database that was awake all
the time.

The request had three parts:

1. Run a complete end-to-end analysis of the application — every file, nothing
   skipped.
2. Identify where costs can be optimised.
3. Specifically: introduce bulk writing, "using queues and stuff".

Plus a domain-level look at what Neon and Fly.io themselves offer for cost
control.

Four clarifying decisions were taken before any code changed:

| Question | Decision |
|---|---|
| The `/health` database ping | **Comment it out — do not delete any code** |
| Moving the database region | **Plan it, but keep it a separate, gated step** |
| Bulk writes | **In-process batching, no queue infrastructure** |
| Depth | **Thorough — analyse everything** |

The "no queue infrastructure" decision turned out to matter more than it looked
at the time. See [§15](#15-things-deliberately-not-done-and-why).

---

## 2. Method — how the investigation was actually run

Two independent sources of evidence were used, and they answer different
questions. Neither alone would have been sufficient.

**Source A — the production query log.** An export of the top 100 queries by
call count and total time from the production branch. This answers *"what is
actually happening"*. It cannot be argued with, and it repeatedly contradicted
what reading the code would have suggested.

**Source B — the code itself.** Every service, handler, middleware, migration
and workflow. This answers *"why is it happening"* and, critically, catches the
things that never appear as a statement in a query log at all.

The single most important thing the pairing revealed: **the largest cost driver
in the entire system does not appear in the query log.** A connection-pool ping
sends an empty simple query, which is not recorded as a statement — but it
resets the database's inactivity timer just as effectively as a real query
would. Reading only the log would have missed it entirely. Reading only the code
would never have shown that a single `/auth/me` endpoint was the most expensive
query in the database.

---

## 3. What the monitoring export proved

### 3.1 The headline ratio

| Metric | Value |
|---|---|
| `InsertODPresentRecord` — actual attendance marks written | **319** |
| Total queries in the same window | **~110,000** |
| **Queries per mark** | **~345** |

Three hundred and forty-five database queries for every one thing the
application actually exists to do. That number, on its own, framed the whole
investigation: this was never about making queries faster. It was about the
enormous ratio of overhead to work.

### 3.2 Finding one — the most expensive query was a login side-effect

| Query | Calls | Avg | **Total time** | Rank |
|---|---|---|---|---|
| `ProvisionUser` | 2,157 | 8 ms | **17 s** | **#1 of 100** |

`ProvisionUser` is an `INSERT … ON CONFLICT DO UPDATE` that fires on **every
single call to `/auth/me`** — not only on a user's first login. For a returning
user it still writes `last_login_at = now(), updated_at = now()`: a real row
update, a real write-ahead-log record, inside its own transaction.

At 8 ms it was roughly **eighty times slower** than `GetUserBySubject`, which
returns the same information for effectively 0 ms.

It was called 2,157 times because the web client re-fetched the identity on
**every authentication state event** — including every silent token refresh, and
every time a phone came back from the background. Not once per sign-in. Dozens
of times per session.

### 3.3 Finding two — 79% of the audit ledger recorded nothing

| Query | Calls |
|---|---|
| `InsertAuditLog` | **2,740** |
| `InsertODPresentRecord` | 319 |

The gap is `auth.login_succeeded`, which the handler documented as firing "on
EVERY successful call … returning users included". So approximately **2,157 of
2,740 audit rows recorded nothing that happened**. Each one is a write-ahead-log
write, and write-ahead-log history is billed per gigabyte-month.

Findings one and two compound: **each `/auth/me` cost two transactions, one row
update, and one audit insert** — and it was the most-called write path in the
entire system, by a wide margin over actual attendance marking.

### 3.4 Finding three — a database GUI was holding the compute open

The export contained a set of queries under the role `neondb_owner` that the
application never uses:

| Query | Calls |
|---|---|
| `DISCARD TEMP` / `RESET ALL` / `CLOSE ALL` / `SET SESSION AUTHORIZATION DEFAULT` / `SELECT pg_advisory_unlock_all()` | **672 each** |
| `pg_catalog` introspection (constraints, columns, defaults, types, sequences, access methods) | ~30–35 each, **~250 total** |
| A quoted-identifier `select "id", "supabase_subject", "email", …` | 45 |
| `update "users" set "role" = $1 where "id" = $2` | 15 |

That signature — 672 connection-reset cycles plus a repeating catalog refresh —
is a **schema-browser GUI with a live session**. The application connects as
`od_runtime`, never as `neondb_owner`. Only a console does.

**A console holding a session open defeats scale-to-zero entirely, no matter
what the application does.** This was free money, required no code change, and
could not have been found any other way.

> **A note on naming:** in this project "Drizzle" refers to the drizzle-gateway
> / Studio database browser — a GUI. It is not an ORM and there is no ORM in
> this codebase; the data layer is sqlc plus goose. This distinction matters
> here because the GUI is exactly what was holding the session open.

### 3.5 The Pareto by call count

| Rank | Query | Calls | % of total | What it is |
|---|---|---|---|---|
| 1 | `IsTokenRevoked` | **15,766** | 14% | Per-request auth, uncached |
| 2 | `GetUserBySubject` | **13,571** | 12% | Per-request auth, uncached |
| 3 | `GetMarkEvent` | 8,862 | 8% | 5 s poll |
| 4 | `GetEvent` | 7,914 | 7% | 5 s poll |
| 5 | `ListCategoriesByEvent` | 7,901 | 7% | 5 s poll |
| 6 | `CoordinatorAssignedToEvent` | 7,722 | 7% | 5 s poll (scope check) |
| 7 | `CountMarksForCategory` | 6,818 | 6% | 5 s poll (status) |
| 8 | `GetCaptureCreditState` | 6,798 | 6% | 5 s poll (status) |
| 9 | `begin` / `commit` | 5,379 / 5,066 | 9% | 313 rollbacks; ~2,157 are no-op writes |
| 10 | `GetAppSetting` | 4,884 | 4% | 5 s poll, 10 s cache too short |
| 11 | `InsertAuditLog` | 2,740 | 2% | **79% login noise** |
| 12 | `SearchStudents` | 2,332 | 2% | Unindexable scan |
| 13 | `ProvisionUser` | 2,157 | 2% | **Write on every `/auth/me`** |
| — | **`InsertODPresentRecord`** | **319** | **0.3%** | **The actual work** |

Two summary numbers worth internalising:

- **Rows 1–2 alone are 29,337 calls** — ninety-two authentication queries for
  every mark written.
- **Rows 3–8 plus 10 are 50,899 calls (~46% of everything)** — pure five-second
  polling from three screens.

### 3.6 The Pareto by total time

Call count says where the volume is. Total time says where the compute-seconds
— the thing actually billed — go.

| Rank | Query | Calls | Avg | Total | Diagnosis |
|---|---|---|---|---|---|
| 1 | `ProvisionUser` | 2,157 | 8 ms | **17 s** | Write on every `/auth/me` |
| 2 | `DebitCaptureCredit` | 185 + 13 | 28.8 ms / **190 ms** | **7 s** | **Lock contention** |
| 3 | `SearchStudents` | 2,332 | 1.5 ms | **3 s** | **Sequential scan** |
| 4 | `GetEvent` | 7,914 | 0.1 ms | 0.9 s | Volume, not cost |

`DebitCaptureCredit` was **the slowest query in the system** — a 35 ms average
across 198 calls, roughly three hundred times the median, with a thirteen-call
bucket averaging **190 ms**. That 190 ms bucket is waiting, not working. It is
the marking burst, and it is the moment a coordinator is standing in front of a
student.

`SearchStudents` at 1.5 ms is about fifteen times the median and, critically,
**degrades linearly with roster size**. It was going to get worse on its own.

---

## 4. What only reading the code could reveal

### 4.1 The health probe was pinging the database every fifteen seconds, forever

`api/internal/handler/health/health.go` called `db.Ping(ctx)` on every request.
`api/fly.toml` health-checks `/health` at `interval = "15s"`. Two machines.

```
4 probes/min × 60 min × 24 h × 2 machines = 5,760 database touches per day
```

**At zero traffic.** On a Sunday. Every day.

This never appears in a query log, because `pgxpool.Ping` sends an empty simple
query rather than a statement. But it resets the inactivity timer every single
time, which means **the compute could never suspend**. This was the single
largest line on the bill, and it was invisible to the log.

### 4.2 The connection pool re-dialled the instant the database suspended

`api/internal/store/pool.go` set `MinConns = 2` and `HealthCheckPeriod = 30s`.

Neon closes connections when it suspends. The pool's health-check ticker then
sees itself below `MinConns` and immediately opens new ones — **waking the
compute again**.

This is a documented failure mode of this pool library against this database,
and it has an important consequence: **fixing the health probe alone would have
bought nothing.** The two had to be fixed together or neither mattered.

### 4.3 The database is on the wrong side of the planet

The deployment runbook had already measured it: a cached read is 0.157 s, while
`/health` (which added exactly one database ping) was 0.639 s. That difference
is **~480 ms per round trip** — the API runs in Singapore, the database in
`us-east-1`.

A single NFC mark makes roughly ten to twelve *sequential* round trips. That is
five seconds of ocean per student marked.

This was already recorded as a known decision — "should do, not yet acted on".

---

## 5. The cost model — why any of this matters financially

This is the part that reframes everything, and it is worth stating plainly
because it determines which optimisations are worth doing:

> **Neon bills for compute-time awake, not per query.**

A query costs a fraction of a millisecond of CPU. Ten thousand of them cost
almost nothing *if the compute was going to be awake anyway*. What costs money
is the compute never being allowed to suspend.

Reference rates and arithmetic:

| Scenario | CU-hours/month | Approx. cost (Launch @ $0.106) | Approx. cost (Scale @ $0.222) |
|---|---|---|---|
| Always-on at 0.25 CU | 182 | ~$19 | ~$40 |
| Always-on at 1 CU | 730 | ~$77 | ~$162 |
| Always-on at 2 CU | 1,460 | ~$155 | ~$324 |
| **Suspended when idle** | **~event hours only** | **~$1–3** | **~$2–6** |

Scale-to-zero is fixed at a five-minute idle timeout on the lower tiers and is
configurable from 60 seconds to one week only on Scale.

**The implication for prioritisation:** query-count reductions are worth doing
for latency and for staying off the higher autoscale tiers, but they do not by
themselves move the bill. **Anything that lets the compute suspend does.** That
single insight is why the work split into two passes with very different
characters, and why the second pass's client-side change mattered more than its
server-side caching.

For comparison, the Fly.io side runs two shared-cpu-1x machines at roughly
**$5–6/month** — about a tenth of the database idle spend. It was correctly
deprioritised.

---

## 6. Pass one — letting the database sleep

### 6.1 The health probe

The instruction was explicit: **comment it out, delete nothing.** That was
honoured exactly.

```go
func Handler(db Pinger) gin.HandlerFunc {
    // Referenced so the parameter stays live for the commented-out probe; drop
    // this line when the block below is restored.
    _ = db
    return func(c *gin.Context) {
        // ── DISABLED: the per-request database ping (see the package comment) ──
        //
        // ctx, cancel := context.WithTimeout(c.Request.Context(), pingTimeout)
        // defer cancel()
        //
        // if err := db.Ping(ctx); err != nil { ... }
        //
        c.JSON(http.StatusOK, contracts.HealthStatus{
            Status: contracts.HealthStatusStatusOk,
            Db:     contracts.HealthStatusDbUp,
        })
    }
}
```

`Pinger`, `pingTimeout` and the `db` parameter all remain, so nothing downstream
changed shape and restoring the behaviour is a single uncomment. The API
contract was not touched — it still defines `db: up|down` and the 503 response;
the system simply stopped producing the down case.

**The consequence, stated plainly rather than buried:** the host platform can no
longer route traffic away from a machine whose database is unreachable. A
machine with a dead database keeps serving, and marks fail with 5xx instead of
being routed around. This is a real reduction in availability engineering, taken
deliberately, in exchange for the largest line on the bill. The deployment
runbook section was rewritten from "fail-closed" to "fail-**OPEN** on the
database", with what to check instead and how to restore it.

### 6.2 The connection pool — required, or the above was worthless

```go
if !strings.Contains(dsn, "pool_health_check_period") {
    cfg.HealthCheckPeriod = 1 * time.Minute   // was 30s
}
if !strings.Contains(dsn, "pool_min_conns") {
    cfg.MinConns = 0                          // was 2
}
```

`MaxConnLifetimeJitter` was left alone — it fixed a real hourly health-check flap
and is unrelated to this.

An existing test asserted `MinConns >= 1` with the comment that it kept a
connection warm for the health probe. That assertion was now backwards, so it
was **inverted rather than deleted**:

```
TestNewPool_...KeepsConnectionsWarm
  → TestNewPool_HoldsNoIdleConnectionsSoTheDatabaseCanSuspend
```

It now asserts `MinConns == 0` and `HealthCheckPeriod >= 1m`. The test still
exists and still guards the property — it just guards the opposite property, on
purpose, with the reasoning written down.

### 6.3 The health tests

`TestHandler_DBDown_503` could no longer be true. It was rewritten as
`TestHandler_DBDown_StillReports200_ProbeIsDeliberatelyDBFree`, and a new test
was added that is stronger than the original:

```go
TestHandler_NeverCallsPing   // uses a countingPinger
```

This asserts the *absence* of the database call directly, rather than inferring
it from a status code. If someone uncomments the ping without also restoring the
CI phase, this fails loudly.

### 6.4 The CI workflow

The integration job waited for a `503` and then ran the health suite with
`HEALTH_EXPECT_DB_DOWN=1`. With the probe disabled it would receive `200` and
fail.

The delivery rules for this project are explicit that **a gate must never be
weakened to make CI green**. So the db-down phase and the "Stop Postgres" step
were **commented out, not deleted**, with an inline comment explaining that the
probe is deliberately database-free and cross-referencing the handler file. The
two comments point at each other, so restoring one without the other is
obvious.

---

## 7. Pass one — killing the write on every page load

### 7.1 Read before you write

`Provision` unconditionally opened a transaction and updated a row. It now reads
first — using `GetUserBySubject`, which already existed and costs effectively
nothing — and only enters the write path when something has actually changed:

```go
const LoginTouchInterval = 15 * time.Minute

var ErrNotProvisioned = errors.New("user: subject not provisioned")

type Current struct {
    ID, Email, Name string
    Role            Role
    LastLoginAt     time.Time
}

func (s *Service) needsWrite(cur Current, email, name string) bool {
    if cur.Email != email || cur.Name != name { return true }
    if cur.LastLoginAt.IsZero()               { return true }
    return s.now().Sub(cur.LastLoginAt) >= LoginTouchInterval
}
```

**Role is deliberately not compared.** It is set only on insert, so an
administrator who was elevated after provisioning legitimately differs from what
the role resolver would compute today. Comparing it would demote them on their
next page load. This is the kind of thing that looks like an oversight in review
and is in fact the entire point, so it is commented in the source.

The `(xmax = 0) AS inserted` discriminator and the provisioning audit semantics
are unchanged — a genuine first login still inserts and still earns its audit
row.

**Result: ~2,157 write transactions → ~100.** The number-one query by total time
was removed.

### 7.2 Throttling the login audit

A new component, keyed on `subject + "|" + sessionID`, with two properties that
were deliberate rather than incidental:

- **An empty session id passes through unthrottled.** A token without a session
  identifier is unusual, and silently collapsing all such events into one would
  hide exactly the case worth seeing.
- **Errors are never cached.** The audit path is fail-closed; caching a failure
  would convert one bad second into a window in which failures are not recorded.

**This is an audit-policy change and was recorded as such.** The original
per-call cadence was chosen on purpose. The security property that matters — "we
can prove who logged in and when" — survives once-per-session. What is lost is
"every token refresh is individually recorded". That trade was written down with
its revert line rather than made silently.

**Result: ~79% of audit rows removed.**

### 7.3 The client side of the same problem

The web client re-fetched the identity on every authentication state event. It
now ignores events that do not change the identity:

```tsx
const identityRef = useRef<Identity | null>(null)
identityRef.current = identity

return session.subscribe((token) => {
  if (!token || !identityRef.current) {
    void load({ background: true })
  }
})
```

A ref rather than state, so the subscription does not need to be torn down and
re-established on every identity change — which would have reintroduced the
problem in a different shape.

---

## 8. Pass one — the two per-request auth queries

`IsTokenRevoked` (15,766) plus `GetUserBySubject` (13,571) = **29,337 calls,
27% of all database traffic**, for 319 marks.

The important decision here was **not to write anything new**. The codebase
already contained `internal/cache.TTL` — a bounded, singleflight-collapsed TTL
cache with `Forget` and `Clear` — and the public read path already demonstrated
the decorator pattern over it. Both new caches reuse that.

| Cache | TTL | Key | Invalidation |
|---|---|---|---|
| Principal (role) | 30 s | subject | `Forget(subject)` and `Clear()` |
| Token revocation | 10 s | `subject\|sessionID\|issuedAt` | `Clear()` only |

The revocation cache deliberately has **no per-subject `Forget`**:

```go
func revocationKey(id VerifiedIdentity) string {
    return id.Subject + "|" + id.SessionID + "|" +
        strconv.FormatInt(id.IssuedAt.Unix(), 10)
}
```

Because the key carries a session id and an issue time, "every cached verdict
for this subject" is not something the holder can enumerate. Offering a
`Forget(subject)` that silently missed entries would be worse than not offering
one. After an administrator deliberately forces a logout, reloading a handful of
entries once is plainly the right price for certainty.

The revoke handler was wrapped so that a successful revocation drops both caches
on that machine:

```go
admin.POST("/users/:user_id/sessions/revoke",
    handleradmin.RevokeSessionsHandler(revokeDroppingCaches{
        inner:      revSvc,
        revocation: revCache,
        principals: principalCache,
    }))
```

The **order is a property of the composition**, not of either cache, which is
why the wrapper lives at the composition root rather than inside a cache:
**write first, invalidate second, invalidate only on success.** Invalidating
first leaves a window in which a concurrent read re-caches the pre-revocation
answer. Invalidating on failure throws away good entries for nothing.

**Result: ~29,000 calls → ~1,500.**

---

## 9. Pass one — backing off the polling

Seven queries across three screens accounted for 50,899 calls — about 46% of
everything — all of it timers.

| Screen | Before | After | Reasoning |
|---|---|---|---|
| Live board | 5 s | **20 s** | Watched passively |
| Event attendance | 5 s | **20 s** | Watched passively |
| Marking screen | 5 s | **10 s** | A coordinator watches this with a student in front of them — kept tightest |
| Public board | 60 s | unchanged | Already correct |

Two structural changes mattered more than the numbers:

**Conditional polling.** Each `refetchInterval` callback now returns `false` when
there is nothing live to watch — a past date, a finished event, an errored query.
Previously these screens polled at full rate on a Sunday with nothing running.

**`refetchOnWindowFocus: false`** was added globally. `refetchIntervalInBackground`
already defaulted to `false`, so hidden tabs were verified as already quiet and
left alone.

The application-settings cache TTL went from 10 s to **60 s** — 4,884 calls had
got through a 10 s window, and a 60 s lag on a documented administrative toggle
is acceptable.

**Result: ~51,000 calls → ~13,000.**

---

## 10. Pass one — the two genuinely slow queries

### 10.1 The redundant lock

`DebitCaptureCredit` took `SELECT … FOR UPDATE` on the coordinator assignment
row **and then** an `ON CONFLICT` row lock on the usage table. The `FOR UPDATE`
was redundant: the unique key on `(event_id, user_id, usage_date)` already
serialises concurrent debits for the same coordinator-day. The extra row lock
only added a second thing to wait on.

Removing it left the exhaustion and concurrency behaviour identical — verified
by the existing credit integration tests, which were **not** modified.

### 10.2 The index that could not be used — and then still was not

The roster search predicate was:

```sql
WHERE lower(s.roll_no || ' ' || s.name) ~~ ALL (@patterns::text[])
```

A leading-wildcard `LIKE` over a computed expression. Every keystroke of the
debounced search was a sequential scan.

A trigram index was the obvious fix:

```sql
CREATE INDEX IF NOT EXISTS students_search_trgm_idx
    ON students USING gin ((lower(roll_no || ' ' || name)) gin_trgm_ops);
```

**And it did not work.** `EXPLAIN` still showed a sequential scan after the index
was built.

The root cause took real digging. `~~ ALL (array)` is not a simple operator
expression — the planner represents it as a **`ScalarArrayOpExpr` with AND
semantics**, and **no index access method in Postgres implements that form**.
The index was perfectly valid, perfectly relevant, and structurally unusable by
that predicate.

The fix is a logically redundant extra predicate that the planner *can* use:

```sql
WHERE lower(s.roll_no || ' ' || s.name) ~~ ALL (@patterns::text[])
  AND lower(s.roll_no || ' ' || s.name) ~~ @seed_pattern::text
```

`seed_pattern` is the longest of the patterns. It cannot change the result —
it is already implied by the `ALL` — but it is a plain operator expression, so
the planner can use the index for it and then filter the (now tiny) candidate
set with the `ALL`.

Verified on 5,000 seeded rows: a **bitmap index scan** for selective terms, and
a sequential scan for unselective ones, which is the planner behaving correctly
rather than the fix failing.

---

## 11. Pass one — bulk writes without a queue

The request said "bulk writing (using queues and stuff)". The queue half was
declined, and the reasoning is worth preserving because it is counter-intuitive.

**Why no durable queue.** A Postgres-backed job queue works by **continuously
polling its job table**. That is a query every few seconds, forever, by design.
Adding one would have held the compute awake twenty-four hours a day and
**directly undone the entire idle-spend fix** — the single largest saving in the
project. The skeleton worker entry point remains a skeleton.

**What the request actually wanted** was round-trip collapsing, and that is
achievable with no new infrastructure and no new always-on cost.

### 11.1 The roster import

A per-row upsert loop became a single statement. The obstacle was that sqlc
cannot parse a multi-argument `unnest`, which took two failed attempts (inline
casts, then a `params` CTE) before the working shape emerged: **one
single-argument `unnest` per column, joined on `ordinality`.**

```sql
-- name: UpsertStudentsBulk :many
WITH input AS (
    SELECT roll.roll_no, nm.name, br.branch_id, sec.section, bat.batch, roll.ordinality
    FROM unnest(@roll_nos::text[])   WITH ORDINALITY AS roll(roll_no, ordinality)
    JOIN unnest(@names::text[])      WITH ORDINALITY AS nm(name, ordinality)
      ON nm.ordinality = roll.ordinality
    JOIN unnest(@branch_ids::uuid[]) WITH ORDINALITY AS br(branch_id, ordinality)
      ON br.ordinality = roll.ordinality
    -- … one join per column
), upsert AS (
    INSERT INTO students (…)
    SELECT … FROM input
    ON CONFLICT (roll_no) DO UPDATE SET …
    WHERE … IS DISTINCT FROM …
    RETURNING id, roll_no, (xmax = 0) AS inserted
)
SELECT input.ordinality::bigint AS ordinality, upsert.id, upsert.inserted
FROM upsert JOIN input ON input.roll_no = upsert.roll_no
ORDER BY ordinality;
```

The `RETURNING id, roll_no, (xmax = 0) AS inserted` is what keeps the
inserted/updated/unchanged counts exactly as accurate as the per-row loop's
were. Collapsing the round trips was not allowed to cost reporting fidelity.

**Result: ~2,000 round trips → 1.** At ~480 ms each that is roughly sixteen
minutes reduced to under a second.

### 11.2 The mark path's read phase

The mark path resolved the event and then verified its category as two
sequential queries. These were collapsed into one:

```sql
-- name: GetMarkEventWithCategory :one
… LEFT JOIN categories c ON c.id = @category_id AND c.event_id = e.id …
```

No row means the event is missing; a null `category_id` means the category is
not one of the event's own. Both original error identities are preserved
exactly.

**The subtle part** is that the combined query returns the **event alongside**
`ErrCategoryNotFound`. With two separate queries the event was already in hand
by that point, and the caller uses it to keep the archived-event answer taking
precedence over the category answer. Returning a zero value there would have
silently changed which error a request naming an archived event *and* a bad
category receives — a behaviour change invisible in any diff.

It was introduced as an **optional interface** (`CombinedEventReader`) rather
than a fourth method on the existing reader, so that no existing construction or
test fake broke. A reader that implements it is used; one that does not falls
back to two calls with identical behaviour. A parity test table exercises both
paths against the same cases and asserts they are indistinguishable except by
query count.

### 11.3 The export

Every list page dragged a full re-count, and an export walked up to two hundred
pages — roughly 800 queries per export.

- Page size maximum raised from 100 to 1,000 on **only** the two attendance
  endpoints in the OpenAPI specification, then regenerated. The specification is
  the single source of truth and the generated output is enforced by a CI gate,
  so hand-editing was not an option.
- A separate server-side constant was introduced so the attendance page cap
  could rise to 1,000 while the event listing cap stayed at 100.
- `readDay` now trusts page one's total instead of re-reading it on every page.

The existing paging test hardcoded 250 rows, which meant it had **silently
become a single-page test** the moment the page size passed 250 — exactly the
truncation it existed to catch. It was rewritten to size itself from the page
size constant:

```ts
const total = EXPORT_PAGE_SIZE * 2 + 50
```

**Result: ~800 queries per export → ~40.**

---

## 12. Pass two — server-side caching for the authenticated surface

### 12.1 The question that started it

> *"How do we implement server side caching? Is it possible now? How easy? Will
> this get the cost down?"*

The answer to the first part turned out to be: **the public surface already has
it.** A decorator package already wrapped the four unauthenticated reads in the
bounded TTL cache, with per-read lifetimes and HTTP cache headers.

What had never been done was the **authenticated** surface — which is precisely
where the remaining 51,000 polling calls were.

### 12.2 Tracing one poll

Every `GET /api/v1/mark/status` runs the full scope policy before it answers:

| # | Query | Source | Changes when… |
|---|---|---|---|
| 1 | `GetEvent` | policy → `EventByID` | an admin edits/archives the event |
| 2 | `CoordinatorAssignedToEvent` | policy | an admin changes assignments |
| 3 | `ListCategoriesByEvent` | policy → `CategoryForEvent` | an admin adds a category |
| 4 | `CoordinatorAssignedToEventCategory` | policy | an admin changes assignments |
| 5 | `GetMarkEvent` | status service | same as #1 — **a second read of the same row** |
| 6 | `GetAppSetting` | slot ceiling | already cached |
| 7 | `CountMarksForCategory` | status service | **every mark — genuinely live** |
| 8 | `GetCaptureCreditState` | status service | **every mark — genuinely live** |

**Five of seven uncached queries are scope and reference data** whose answers
change a handful of times in the life of an event. Two are genuinely live.

This maps exactly onto the export's numbers (7,914 / 7,722 / 7,901 / 8,862),
which is a good sign the trace is right.

### 12.3 The decorator

A new package mirrors the public-read one. The seam it wraps was already a
narrow five-method interface rather than a general repository — a design choice
made much earlier that made this change far cheaper than it would otherwise have
been.

| Method | Key | Shared across actors? |
|---|---|---|
| `EventByID` | `event\|<id>` | **yes** — a whole marking team collapses onto one query |
| `CategoryForEvent` | `cat\|<event>\|<category>` | **yes** |
| `CoordinatorAssignedToEvent` | `assign\|<user>\|<event>` | no |
| `CoordinatorAssignedToEventCategory` | `assigncat\|<user>\|<event>\|<category>` | no |
| `AssignedActiveEvents` | `assigned\|<user>` | no |
| `ActiveEventsForAdminMarking` | `admin-active` | **yes** |

The per-user keys are deliberately not shared. Sharing them would be a scope
leak, and a test asserts the distinction directly: three coordinators must cost
**one** event read and **three** assignment reads.

### 12.4 Caching an answer versus caching a failure

The distinction that shapes the implementation:

> "This category does not belong to this event" is an **answer**, and holding it
> for a minute is correct. "The database refused the connection" is a
> **failure**, and holding it would turn one bad second into a minute of
> refusals for everyone sharing that key.

The underlying cache already refuses to store anything when the load function
returns an error. So a small wrapper stores *decided* refusals as values and
lets everything else through unstored:

```go
type answer[V any] struct {
    value V
    err   error   // a DECIDED refusal, deliberately cached
}

func cached[V any](ctx context.Context, c *cache.TTL[answer[V]], key string,
    fn func(context.Context) (V, error), sentinels ...error) (V, error) {

    got, err := c.Do(ctx, key, func(ctx context.Context) (answer[V], error) {
        value, loadErr := fn(ctx)
        if loadErr == nil {
            return answer[V]{value: value}, nil
        }
        for _, sentinel := range sentinels {
            if errors.Is(loadErr, sentinel) {
                return answer[V]{err: loadErr}, nil
            }
        }
        // Not a decided answer — a transient failure. Returning it here means
        // the cache stores nothing, so the next request retries.
        return answer[V]{}, loadErr
    })
    …
}
```

Both halves are tested explicitly: a transient error must reach the database
three times out of three calls; a decided refusal must reach it once.

### 12.5 The trap — a type assertion that fails OPEN

This was the most dangerous thing found in either pass.

The policy reaches category-level scope through an **optional interface
discovered by type assertion**:

```go
if actor.Role != roleAdmin {
    scoped, ok := p.reader.(categoryScopedReader)
    if !ok {
        return nil          // ← ALLOWS
    }
    …
}
```

A decorator that omitted `CoordinatorAssignedToEventCategory` would still
satisfy the main reader interface, still compile, still pass every existing
test — and **silently switch off category-level scoping for every coordinator in
the system.** No test in any package would have caught it, because every other
test builds its own object graph.

Three defences were put in, in order of strength:

**1. Make it unconstructable.** The decorator's *input* interface requires the
method. You cannot build the decorator over a reader that lacks it.

**2. Compile-time assertions.**

```go
var (
    _ marking.Reader = (*Reader)(nil)
    _ ScopeReader    = (*Reader)(nil)   // the load-bearing one
)
```

**3. A behavioural test through a real policy** — the only form that fails if
the assertion inside the policy stops finding what it looks for:

```go
func TestCachedReader_CategoryScopeSurvivesTheTypeAssertion(t *testing.T) {
    inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: false}
    policy := marking.NewPolicy(NewReader(inner, ttl))

    err := policy.AssertCanMark(ctx, coordinator(), eventID, categoryID)

    if !errors.Is(err, marking.ErrCategoryNotFound) {
        t.Fatalf("a coordinator unassigned to the category was ADMITTED …")
    }
}
```

**The fail-open itself was not changed.** Flipping it to fail-closed is the
correct long-term fix, but it is reached by test fakes throughout that package
and is a wider behaviour change than this work. It was written up as an action
item rather than absorbed silently into an unrelated change.

### 12.6 Two policies over one policy body

The single policy instance fed **both** the read façade and the mark write gate.
Caching it would have cached the write gate too. It was split:

```go
plainScopeReader := svcmarking.NewEventReader(eventReader)
markingPolicy    := svcmarking.NewPolicy(plainScopeReader)          // WRITE — exact
markingScopeCache := markingcache.NewReader(plainScopeReader, ttl)
readPolicy       := svcmarking.NewPolicy(markingScopeCache)         // READS — cached
```

**Why the write gate stays uncached:** it runs a few hundred times against the
reads' many thousands. Caching it saves nothing measurable and costs the gate its
exactness.

**The safety argument in one sentence:** the two can now disagree for up to the
cache lifetime, and the disagreement is **one-directional** — a read may still
offer an event an administrator has just archived, and the write then refuses it
correctly. A write admitted on a stale permission is impossible, because the
write never consults the cache.

The status service's duplicate event read was handled the same way, and got the
guarantee for free: it is constructed with its **own** reader instance, distinct
from the one the mark write holds, so the write path is untouched **by
construction rather than by care**.

Because "which policy reaches the write gate" is a single identifier in one
file, it got its own composition guard — the same class of test the admin and
public route groups already had:

```go
func TestRun_MarkWriteGateHoldsTheUncachedPolicy(t *testing.T) { … }
```

### 12.7 Invalidation

The administrator's own edits should not wait out the window. The event service
is wrapped so that the six mutating calls clear the scope cache on the machine
that served the edit. It **embeds** the service rather than reimplementing
fifteen methods, so reads pass straight through and a method added later is
carried automatically.

The drop-only-on-success rule lives in exactly one place:

```go
func dropScopeAfter[T any](scope interface{ Clear() }, write func() (T, error)) (T, error) {
    value, err := write()
    if err == nil {
        scope.Clear()
    }
    return value, err
}
```

Peer machines still expire on their own, so 60 seconds remains the honest number
to quote.

---

## 13. Pass two — the idle polling guard

### 13.1 Why caching alone was not enough

This is the conclusion the whole exercise turns on, and it was stated plainly
rather than letting the caching take credit it had not earned:

> A cached poll is still a poll. The two live counters hit the database
> regardless. Caching lowers the cost **per poll**; only stopping the poll lets
> the compute suspend.

A single tab left open on the marking screen polls **8,640 times a night**. That
pins the compute awake roughly 670 idle hours a month:

| Autoscale minimum | Approx. monthly cost of one forgotten tab |
|---|---|
| 0.25 CU | **$18–37** |
| 1 CU | **$74–155** |

All of it spent so that one person does not wait 2.6 seconds in the morning.

### 13.2 The cold-start question

The concern raised was reasonable: *if the database sleeps overnight, doesn't
waking it cost time?*

Yes — and the numbers make the trade obvious once they are laid out:

- **Measured in production:** a cold read took **2.6 s**; the very next took
  **0.152 s**. A large part of that 2.6 s is the ~480 ms trans-Pacific round
  trip, not the resume itself.
- **During the event: never.** The compute only suspends after minutes of total
  silence. On event day there is continuous traffic.
- **Morning of the event: one person, once.** Everyone behind them gets the warm
  path.
- **The trade was already live** — letting the compute suspend was the entire
  point of the health-probe work. The idle guard does not *introduce* cold
  starts; it only decides whether one forgotten browser tab is allowed to
  prevent them.

### 13.3 The hook

```ts
export const IDLE_STOP_MS = 30 * 60_000
const INTERACTION_EVENTS = ['pointerdown', 'keydown'] as const

export function useIdleAwareInterval({
  activeMs, stopAfterMs = IDLE_STOP_MS, onResume,
}: IdleAwareIntervalOptions): number | false
```

Design points:

- **`activeMs` accepts `false` and passes it through**, so a screen that already
  has its own reason not to poll (a past date, a finished event) keeps that
  reason and merely gains the idle stop.
- **`onResume` fires on wake.** Resuming the timer alone would leave the reader
  looking at up to half an hour of stale data until the next tick. Every screen
  wires this to `refetch()`.
- **The idle flag lives in a ref that is mutated only inside handlers**, with
  state mirroring it purely to trigger the re-render. Syncing state into the ref
  during render would let a render clobber a wake that had not yet flushed.
- Half an hour is far longer than any real gap in use — a slot is twenty-five
  minutes at most and a coordinator between students is still tapping — while
  being short enough that a screen forgotten at 18:15 lets the compute suspend
  well before midnight.

Applied to four screens:

| Screen | Behaviour |
|---|---|
| Marking | 10 s while a slot is open; **60 s when no slot is open**; stop after 30 min idle |
| Live board | 20 s, existing date gate kept, plus idle stop |
| Event attendance | 20 s, existing live gate kept, plus idle stop |
| Public board | 60 s, plus idle stop |

The marking screen's closed-slot back-off reads the server's explicit
`slot: null` state rather than re-deriving the timetable on the client — nothing
that endpoint reports can change while every slot is closed, because no mark can
be accepted, the counters are frozen and the credit pool cannot move.

**The public board was included deliberately**, and it is the least obvious of
the four: its server-side cache lasts 60 seconds and its poll is 60 seconds, so
**every poll is a cache miss** that reaches the database. One public board left
up on an unattended display keeps the compute awake exactly as effectively as a
marking screen.

The framing test is named for what it protects:

```ts
test('[P0] an untouched screen stops polling', () => {
  …
  expect(result.current,
    'a forgotten tab kept polling — this is the whole cost of the feature',
  ).toBe(false)
})
```

---

## 14. Problems hit along the way, and how each was resolved

### 14.1 sqlc could not parse multi-argument `unnest`

**Symptom:** `function unnest(unknown, unknown, …) does not exist` at code
generation.
**Attempts that failed:** inline casts on each argument; wrapping the arrays in
a `params` CTE.
**Resolution:** one single-argument `unnest` per column, joined on
`ordinality`. Verbose, but it generates cleanly and the join key makes the
row correspondence explicit rather than positional-by-convention.

### 14.2 The migration role could not create an extension

**Symptom:** `permission denied to create extension "pg_trgm"` running as the
migration role.
**Why:** the migration role deliberately holds DDL rights but not superuser.
That separation is a security property worth keeping, not an obstacle to route
around.
**Resolution, in two parts:**
1. `CREATE EXTENSION` moved into the owner-run role bootstrap script, where the
   privilege exists.
2. The migration itself wrapped in a block that tries and catches, so it
   self-installs where permitted and fails with an **actionable message**
   elsewhere:

```sql
DO $ensure$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        BEGIN
            CREATE EXTENSION pg_trgm;
        EXCEPTION WHEN insufficient_privilege THEN
            RAISE EXCEPTION 'pg_trgm is not installed and this role may not install it…';
        END;
    END IF;
END
$ensure$;
```

Verified in both directions: it installs cleanly in scratch databases created
with the migration role as owner, and fails with the explanatory message on the
shared database.

### 14.3 The trigram index was built and then ignored

Covered in [§10.2](#102-the-index-that-could-not-be-used--and-then-still-was-not).
Worth restating as a general lesson: **building an index is not evidence the
index is used.** `EXPLAIN` was what turned "done" into "not done".

### 14.4 Twelve integration tests were already failing

**Symptom:** twelve failures in the dispute and marking suites during
verification.

**The important step was not fixing them — it was establishing whose they
were.** The previous commit was checked out into a temporary worktree and the
suite re-run there. They failed identically. **Pre-existing.**

Two causes:

1. An earlier commit made two dispute fields required but never updated the test
   fixtures.
2. A helper created a coordinator user with no assignment row — survivable until
   a later migration moved the capture-credit limit onto that row.

Both were fixed as **fixture corrections**. No assertion was weakened and no
gate was relaxed. The distinction was recorded, because "the agent's change
broke twelve tests" and "the agent found twelve already-broken tests" are very
different statements.

### 14.5 A test polluted its own database

**Symptom:** a public-events cap test passed once and failed on a repeated run.
**Cause:** events accumulated across runs until the count exceeded the cap the
test asserted.
**Resolution:** recreated a pristine database. The test itself is correct; the
environment was dirty.

### 14.6 Wrong version-control account

**Symptom:** `git push` → "Repository not found".
**Cause:** the active CLI account was a different user; the repository is
private under the owner's account.
**Resolution:** switched accounts. **This changed global CLI state and was
reported rather than left silent**, since it affects every other repository on
the machine.

### 14.7 Deployed to the wrong hosting project

**Symptom:** the deployment succeeded and the production domain did not change.
**Cause:** the local link file pointed at a project created days earlier during
experimentation, not the production project that owns the domain.
**Resolution:** backed up the stray link, relinked to the correct project,
rebuilt, redeployed, aliased both domains.
**Follow-up hardening:** the link directory was added to the ignore file, since
committing it makes every clone deploy to whatever project the last person
happened to be linked to. A stray production deployment remains on the unused
project — harmless, no custom domain — and was left rather than deleting
something not clearly disposable.

### 14.8 The build tool silently produced a stale artifact

**Symptom:** after building and deploying the second pass, the live entry bundle
hash was **byte-identical to the previous day's** — 663,587 bytes, same hash —
despite four changed screens and a new module.

**How it was caught:** by checking the build output for a distinctive marker
*before* deploying, rather than checking the site afterwards. The new module
should have produced its own chunk. It had not.

**Diagnosis:** a direct framework build produced a `useIdleAwareInterval` chunk
and a different entry hash. The platform build wrapper had emitted stale output
from a cache despite fresh file timestamps.

**Resolution:** cleared the output directory, rebuilt, **verified the artifact
contained the new chunk**, and only then deployed. Confirmed afterwards by
fetching the chunk from production and byte-comparing it to the local artifact:

```
http 200  bytes 706   IDENTICAL
```

**The lesson:** a fresh timestamp is not evidence of fresh content. Verify the
artifact, not the build log.

### 14.9 Go cannot spread a multi-value call alongside another argument

**Symptom:**

```
multiple-value e.Service.Create(…) in single-value context
not enough arguments in call to dropScopeAfter
```

**Cause:** `f(a, g())` is only legal when `g`'s results exactly match `f`'s
entire parameter list.
**Resolution:** the helper takes a closure instead. This landed better anyway —
the rule that a failed edit must not discard good cache entries now lives in one
place rather than being restated six times.

### 14.10 The local CI runner tripped on a dirty working tree

**Symptom:** the contract-drift workflow failed locally, reporting untracked
files and a modified workspace checksum file.
**Cause:** the gate is an **unscoped** `git status --porcelain`, and the local
runner executes against the working tree, whereas the hosted runner checks out
clean.
**Resolution:** committed the legitimate workspace checksum line, moved
unrelated local scratch files aside, and re-ran — **pass**. Also added the
hosting link directory to the ignore file, since it would otherwise trip the
same gate for anyone else.

### 14.11 The local runner image lacks tools the hosted runner preinstalls

**Symptom:** `shellcheck: command not found`, then `psql: command not found`.
**Cause:** an image limitation, not a code failure.
**Resolution — each covered another way rather than skipped:**

| Missing step | How it was actually verified |
|---|---|
| `shellcheck --severity=style` | Run via the official shellcheck container — clean |
| Database bootstrap, migrations, integration tests | Run locally against a freshly recreated Postgres 17 — all eight packages green |
| Migration reversibility | `goose down-to 0` + re-up on a pristine database — clean 25 → 0 → 25 |

### 14.12 The migration revert step fails after the tests run

**Symptom:** `goose down-to 0` failed at the extended-slots migration:

```
check constraint "od_records_slot_check" of relation "od_records"
is violated by some row (SQLSTATE 23514)
```

**Diagnosis:** exactly one leftover row with `slot = 20`. The migration's down
step narrows the constraint back to `slot BETWEEN 1 AND 12`, and an
extended-slots test leaves that row behind. Since the workflow runs the
integration tests *before* the revert, the revert inherits the test data.

**Attribution:** the second pass touched no file under the test directory and no
SQL. On a pristine database the 25 → 0 → 25 round trip is clean, proving the
migrations themselves are reversible. **Pre-existing, reported, not silently
patched.**

**Recommended fix (not applied):** have the test clean up its own row.
Explicitly *not* recommended: adding a `DELETE FROM od_records …` to the
migration's down step — a migration that silently deletes attendance rows is
worse than a red gate.

### 14.13 Hosted CI is blocked at the account level

**Symptom:** every workflow, on every push, failing in 3–4 seconds with **zero
steps executed**.

**Diagnosis:** retrieving the check-run annotation gave it verbatim:

> *"The job was not started because recent account payments have failed or your
> spending limit needs to be increased. Please check the 'Billing & plans'
> section in your settings"*

**Handling:** the project's delivery rules classify runner outages, expired
credentials and exhausted budgets as external, and forbid spending fix attempts
on them. **No retries were burned.** It was reported with the exact message.

**Consequence:** nothing can be merged to the integration branch, because the
rules forbid merging or tagging on un-run CI — and CI cannot start.

**Workaround, as requested:** the workflows were run locally.

| Workflow / job | Result |
|---|---|
| contract-drift | **PASS** |
| api-ci — build (gofmt, build, vet) | **PASS** |
| api-ci — acceptance harnesses | **PASS** |
| api-ci — integration | Covered locally (see [§14.11](#1411-the-local-runner-image-lacks-tools-the-hosted-runner-preinstalls)) |

Two flags were essential: without disabling the pre-pull and enabling offline
action resolution, the runner re-clones actions on every invocation and times
out on this network.

### 14.14 The network cannot reach the database at all

**Symptom:** `Network is unreachable` connecting to the database host on 5432;
port 443 to the same host is open.

**Consequence:** this compounds with 14.13 into a hard block. The outstanding
migration cannot be applied from here (no route on 5432) *and* cannot be applied
through the designed workflow (billing-blocked). Both need a human.

---

## 15. Things deliberately NOT done, and why

**A durable job queue.** Polls its own table continuously; would have held the
compute awake around the clock and undone the largest saving in the project.

**Redis or any shared cache.** Two machines each holding their own copy is the
right trade for a spike-shaped load. A shared store adds a network hop, a
credential, and a second thing that can be down — to save duplicating a few
hundred kilobytes. The expensive thing is not the number of cache misses; it is
what a miss costs, and that is trans-Pacific latency the region move removes
from cached and uncached queries alike.

**HTTP `ETag` / `Cache-Control` on authenticated routes.** The server must do
the work to compute an ETag before it can answer `304`, so it saves bytes, not
queries. The in-process value cache is strictly better, and client-side
deduplication already exists.

**Caching the live counters.** Keyed per user, event, category and day, so
nothing is shared between coordinators, and a 60 s window against a 10 s poll
would make every poll a miss anyway. They are also the numbers a coordinator
watches change after each scan — a stale one at the moment of a mark is the most
confusing thing this system could show.

**Caching the mark write's scope gate.** A few hundred calls against the reads'
many thousands. Nothing to gain, exactness to lose.

**Batching the mark path's write phase.** The debit, the insert and the audit
row already share one transaction. Batching them saves three round trips in
exchange for restructuring the audit co-commit invariant — the guarantee that
the ledger cannot diverge from the state it records — on the most
correctness-critical path in the product, days before an event. The region move
reduces those same three round trips from ~480 ms to ~5–15 ms each at no risk.

**Uploading the database owner credential as a repository secret** to run
`CREATE EXTENSION` remotely. This is a security expansion reaching outside the
repository. Instead, the API was **proven correct on the older schema** by
building a database at that version and running the full integration suite
against it — establishing that the outstanding migration is a pure optimisation
and blocks nothing.

**Flipping the fail-open type assertion.** Correct, but wider than this change;
raised as an action item with its exact location.

**Moving off the current database provider.** Lateral, and would mean redoing
dual roles, the audit trigger and privilege defaults against a different
connection pooler.

---

## 16. Verification — what was actually run

### Static and unit

- `gofmt -l` — empty
- `go build ./...`, `go vet ./...` — clean
- Go unit suite — **31 packages**, all pass
- Generated code drift check — no drift
- Contract regeneration — no drift
- Web: typecheck, lint — clean
- Web: **46 files, 425 tests** — all pass

### Integration

- Full `-tags integration` suite against a **freshly recreated** Postgres 17 at
  schema 25 — all eight packages green
- **No existing scope test required editing** — this was the acceptance
  criterion for the cache not having changed behaviour. Had one needed a change,
  the change would have been wrong.
- Migration reversibility: 25 → 0 → 25 clean on a pristine database
- Shell scripts: clean at the strictest severity

### Production

| Check | Result |
|---|---|
| API machines | version 31, both regional machines passing |
| Smoke test | **PASS**, including the cleartext-refusal transport rule |
| `/health` | **0.162 s** — the pure network floor, proving no database touch |
| Public read, cold | **4.4 s** — the accepted resume-from-suspend cost |
| Public read, warm | **~0.15 s** after each machine's first miss |
| Unauthenticated `/auth/me` | 401 |
| Apex domain | 308 → `www` |
| Live entry bundle | matches the deployed artifact |
| Live idle-polling chunk | fetched 200, **byte-identical** to local build |

The final check deserves emphasis: rather than trusting that the deployment
worked, the actual JavaScript chunk was pulled from production and compared byte
for byte, with its minified contents inspected to confirm the 30-minute
constant, the three event listeners and the `idle ? false : activeMs` return
were all present.

---

## 17. Results

### Query volume

| Metric | Before | After |
|---|---|---|
| Queries per mark written | **~345** | **~25** |
| Auth queries per request | 2, uncached | ~0 |
| Auth overhead total | 29,337 | ~1,500 |
| Polling queries | 50,899 | ~13,000, and **zero when idle** |
| Queries per marking-screen poll | 7 | **~2** |
| Identity-endpoint write transactions | 2,157 | ~100 |
| Audit rows recording nothing | ~2,157 | ~0 |
| Roster import (2,000 rows) | ~2,000 round trips | **1** |
| Full-day export | ~800 queries | ~40 |

### Latency

| Path | Before | After |
|---|---|---|
| `/health` | 0.639 s | **0.157–0.162 s** |
| Public read, warm | — | ~0.15 s |
| Roster import | ~16 min | **< 1 s** |
| Slowest query | 35 ms avg / 190 ms burst | redundant lock removed |
| Roster search | sequential scan | bitmap index scan |

### Idle spend

| | Before | After |
|---|---|---|
| Forgotten tab, overnight | 8,640 polls / ~60,000 queries | **0** |
| Compute overnight | awake 100% | **suspended** |
| Idle CU-hours | ~670/month | **~0** |
| Cost of that | $18–37 (0.25 CU) / $74–155 (1 CU) | one 2.6 s wait, once |

---

## 18. Still outstanding

Each of these needs a console, a credential, or money — none can be done from a
code agent, which is why they are tracked rather than assumed.

### Blocking

**1. Restore hosted CI billing.** Until this is fixed nothing can merge to the
integration branch, because merging requires green CI and CI cannot start.
Everything has been verified locally in the meantime.

**2. Apply the outstanding migration.** Blocked by two independent things: the
extension requires the database owner, and this network cannot reach the
database on 5432 — so the designed workflow path is the only route, and it is
billing-blocked. Production runs the previous schema version and is fully
functional; the migration is purely the roster-search index and nothing depends
on it.

### High value, console-only

**3. Close the database GUI session.** Free, and it blocks scale-to-zero
regardless of any code change. This is the highest value-per-effort item on the
list.

**4. Neon console settings:** confirm scale-to-zero; drop the autoscale minimum
to 0.25 CU; switch both connection strings to the pooled endpoint; delete
non-production branches (each has its own compute); reconsider the tier — the
higher tier costs 2.1× per CU-hour, so if it was chosen only for the longer
point-in-time-recovery window, the lower tier plus the existing dump layer
covers the same intent at half the compute rate.

### Deferred by decision

**5. Move the database region.** The single largest remaining win: ~480 ms to
~5–15 ms per round trip. It is a dump-and-restore of live attendance data and
needs its own maintenance window, before an event rather than during. Roles do
not travel in a dump and must be recreated.

**6. Fix the fail-open type assertion.** One line, plus updating the test fakes
that depend on the current behaviour. Worth a dedicated pass.

**7. Fix the migration-revert test pollution.** Have the extended-slots test
clean up its row. Will otherwise go red for an unrelated reason once CI runs
again.

**8. Batch the mark path's write phase.** Only worth revisiting if the region
move does not happen.

---

## 19. Lessons worth keeping

**Measure before optimising, and use two instruments.** The query log found
things reading code never would have — a login endpoint as the most expensive
query, a GUI session holding the compute open. Reading code found things the log
structurally could not — a connection-pool ping that resets the idle timer
without being recorded as a statement. Either alone would have produced a
materially worse result.

**Know what the vendor actually bills for.** Neon bills for *time awake*. That
single fact reorders the entire priority list: it makes a 15-second health probe
more expensive than 51,000 polling queries, and it makes stopping a poll worth
more than caching it.

**Two fixes can be jointly necessary and individually useless.** Disabling the
health ping bought nothing on its own, because the connection pool re-dialled
immediately. Shipping one without the other would have produced no saving and a
plausible-looking explanation for why the work had failed.

**A cached poll is still a poll.** The caching cut queries per poll by roughly
70% and did not, by itself, let the compute sleep. Naming that honestly — rather
than letting the caching take credit for the saving — is what surfaced the idle
guard as the change that actually mattered.

**When a change makes an assertion false, invert the test — do not delete it.**
Two tests asserted properties this work reversed. Both were rewritten to assert
the new property, with the reasoning recorded. One was strengthened in the
process, moving from inferring the absence of a database call to asserting it
directly.

**Establish whose failure it is before fixing it.** Twelve failing tests were
proved pre-existing by checking out the previous commit into a temporary
worktree and re-running. Fixing them without that step would have been
indistinguishable from having caused them.

**Test what the code does, not what it should compute.** The trigram index was
built, valid, relevant and unused. The export paging test had silently become a
single-page test. The deployment artifact was stale despite a fresh timestamp.
In all three cases, checking the *actual* behaviour — `EXPLAIN`, sizing the test
from the constant, byte-comparing the deployed file — was what separated "done"
from "appeared done".

**Some safety properties can only be tested at the composition root.** Which
policy reaches the mark write gate is one identifier in one file. No package's
own tests can see it, because they all build their own object graph. That class
of risk needs its own guard, and this codebase already had precedent for
exactly that.

**Prefer making a mistake unconstructable over documenting it.** The category
scope trap is guarded three ways, and the strongest is that the decorator cannot
be built over a reader lacking the method. A comment saying "remember to
implement this" would have been the weakest possible defence against a failure
mode that compiles cleanly and passes every existing test.

**Write down the trades, including the ones that lost.** The health probe is now
fail-open. The audit ledger no longer records every token refresh. A coordinator
can see an archived event for up to a minute. Each of these is a real reduction
in some property, taken deliberately, and each is recorded with what it bought
and how to reverse it. A change that only documents its wins is not documented.

---

*Both passes are deployed and verified in production. The outstanding items in
[§18](#18-still-outstanding) all require access that a code agent does not and
should not have.*
