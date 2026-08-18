# The domain event stream — design, implementation and verification

**Date:** 19 August 2026
**Branch:** `0-event-mvp`
**Commit:** `52eebe1` — `feat(events): mirror committed domain events onto a Kafka stream`
**Status:** implemented, tested, committed. **Not pushed. Not enabled in production.**
**Size:** 29 files, +3,878 / −22 lines; ~1,780 lines of implementation, ~1,780 of tests.

---

## 0. The one-paragraph version

Every state change in this system already commits an audit row inside the same transaction as the
business write. This change hangs a Kafka publish off that single point, *after* the commit, so the
entire domain event stream comes from one seam rather than from publish calls scattered through
fifteen services. It also adds a cache-invalidation bus, which fixes a limitation the code already
documented: a cache clear reached only the machine that took the write. The whole feature is **off
unless `KAFKA_BROKERS` is set**, and off means genuinely inert — no client, no goroutine, no
connection, every publish a no-op.

---

## 1. What was asked for, and the concern I raised first

The instruction was to implement Kafka wherever possible, without asking for input, committing but
not pushing.

I flagged one thing before starting, and I would flag it again: **Kafka is by a wide margin the
heaviest piece of infrastructure in this stack, and it arrives immediately after a release spent
removing cost.** The previous work cut idle Neon compute, stopped unattended tabs polling, and
removed a health-check database ping — savings on the order of $18–155/month. A hosted broker is a
new recurring line item that would eat a chunk of that, plus a new dependency that can be down, for
a system whose measured peak load is **319 attendance marks in a day**.

That concern is recorded, and the work was built anyway, as asked. What it changed was not *whether*
to build it but *how*: the entire design is arranged so that the feature can ship without being
switched on, and so that switching it on can never degrade the marking path. Section 3 is that
argument in detail.

---

## 2. Why this seam, and not fifteen others

### 2.1 The invariant that made a single publish site possible

`api/internal/store/tx.go` enforces something strong, and it enforces it at runtime rather than by
convention:

> The runtime connection pool plus the same-transaction write helper and the audit insert are the
> **ONLY** write path into the database — no endpoint or worker job writes without co-committing an
> audit row.

`WithAudit` rejects an empty or unregistered `domain.Action` *before opening a transaction*, so a
business write cannot commit under an unnamed action. `domain.AllActions` is a closed registry of 24
actions, and `events_test.go` fails if a declared constant is missing from it.

The consequence is the whole design: **a write that skipped the audit seam is already impossible**,
so a stream attached to that seam has complete coverage by construction. It cannot miss an event
without the ledger missing it too.

### 2.2 The alternative I rejected

The obvious alternative is a `publisher.Publish(...)` call in each service — in the mark path, the
event admin service, the dispute resolver, the auth auditor, and so on. It is more explicit and it
reads more directly at each call site.

It is also **guaranteed to drift.** The first person to add an endpoint adds the write and forgets
the publish, and nothing fails: no compile error, no test failure, no log line. The stream simply
becomes quietly incomplete, and a consumer built on it becomes quietly wrong. A single seam behind an
invariant that is already enforced cannot develop that gap.

The cost of the choice is that the wire envelope is the audit row's shape rather than a typed
per-event struct. Consumers parse `payload` rather than reading a typed field set. That is the right
trade here — see §4.2.

---

## 3. The three rules that make this safe to merge switched off

### 3.1 Off by default, and off means inert

`KAFKA_BROKERS` unset is the entire switch. `kafka.Config.Enabled()` is false, `eventstream.New`
returns a runtime holding `events.Nop{}`, no `kgo.Client` is constructed, no goroutine starts, and no
socket is opened.

This is not a staging convenience. It is how a new dependency gets added to a system that has a real
event to run on 28 July. The code path production takes today is byte-for-byte the one it took
before this change, and that claim is tested three ways: a unit test that the default `store.New`
carries `events.Nop`, a composition test that a disabled runtime is inert, and an actual boot of
`cmd/api` with `KAFKA_BROKERS` unset — `/health` returned 200 and the log contained **zero**
mentions of Kafka or the event stream.

### 3.2 Publish after the commit, never inside the transaction

```go
if err := tx.Commit(ctx); err != nil {
    return fmt.Errorf("store: commit: %w", err)
}

// COMMITTED. Only now does anything leave this process.
if changed {
    s.publish(ctx, evt)
}
```

A produce inside the transaction would hold a Postgres transaction open across a network round trip
to a broker. On Neon that is **billed, awake, connection-holding time added to every write in the
system** — precisely the cost the previous release was spent removing — and a slow broker would turn
into long-running transactions and lock contention on the marking path.

The publish is also governed by the **same `if changed`** that decides whether an audit row is
written. So the stream and the ledger agree on what counted as an event: a duplicate scan that
changed nothing writes no audit row and announces nothing.

### 3.3 Publishing cannot fail or slow a request

`events.Publisher.Publish` returns **nothing**. That is the load-bearing part of the interface, and
the doc comment says why:

> Every caller is on a request path that has ALREADY COMMITTED. The write succeeded, the audit row is
> durable, the student is marked. There is no honest thing a caller could do with a delivery error at
> that point: it cannot un-commit, and returning 500 for a successful mark because a broker was
> briefly unreachable would turn an optional mirror into a new way for the marking path to fail on the
> one day it must not.

And when the buffer is full, `AsyncPublisher.Publish` **drops** rather than blocking:

```go
select {
case p.queue <- envelope:
default:
    if n := p.dropped.Add(1); n == 1 || n%100 == 0 {
        p.logf("events: buffer full, dropped %d event(s) — the audit ledger is unaffected; ...", n, ...)
    }
}
```

Blocking would mean a dead broker applies backpressure through the buffer, into `Publish`, into the
handler, and out to a coordinator standing in front of a student with a phone that will not respond.
There is deliberately **no timeout branch** either — "wait a little" on the marking path is the same
mistake with a smaller number on it.

Drops are counted (`Dropped()`, `Failed()`, `Sent()`) and logged on the first and every hundredth,
because a log line per dropped event would bury an outage in its own symptoms while silence would
hide it entirely.

**A malformed `KAFKA_*` setting fails the boot loudly. An unreachable broker deliberately does
not** — that would convert an optional dependency into a hard one, which is the single thing this
design exists to avoid. franz-go dials lazily and reconnects, so an unreachable broker at boot simply
means events are dropped until it returns.

---

## 4. Architecture

```
                    ┌──────────────────────────────────────────┐
   HTTP request ───►│ service (mark / event / dispute / auth)  │
                    └────────────────────┬─────────────────────┘
                                         │
                    ┌────────────────────▼─────────────────────┐
                    │ store.WithAudit — ONE transaction        │
                    │   business write + audit row co-commit   │
                    └────────────────────┬─────────────────────┘
                                         │ COMMIT
                    ┌────────────────────▼─────────────────────┐
                    │ store.publish (publish.go)               │  ← the only publish site
                    └────────────────────┬─────────────────────┘
                                         │ events.Envelope
                    ┌────────────────────▼─────────────────────┐
                    │ events.AsyncPublisher                    │
                    │   bounded buffer · batch · drop-on-full  │
                    └────────────────────┬─────────────────────┘
                                         │ events.Sink
                    ┌────────────────────▼─────────────────────┐
                    │ kafka.Sink (franz-go)                    │
                    └────────────────────┬─────────────────────┘
                                         │
             ┌───────────────────────────┼───────────────────────────┐
             ▼                           ▼                           ▼
     od.audit.v1 (30d)          od.marks.v1 (7d)           od.cache.v1 (1h)
             │                           │                           │
             └──────────┬────────────────┘                           │
                        ▼                                            ▼
             cmd/worker — consumer GROUP                 every api machine — BROADCAST
             (partitions split, once each)               (no group, all hear everything)
```

### 4.1 Package layout

| Package | Lines | Responsibility |
|---|---:|---|
| `internal/events` | 443 | Transport-free: the envelope, topic routing, the buffering publisher |
| `internal/kafka` | 610 | franz-go: sink, consumer, SASL, topic provisioning, config |
| `internal/cachebus` | 221 | Cross-machine cache invalidation |
| `internal/eventstream` | 155 | Assembly, and the one place "off" is decided |
| `internal/store/publish.go` | 85 | The bridge from the audit boundary to the stream |
| `cmd/streamctl` | 104 | Operator CLI: provision topics, tail the stream |
| `cmd/worker` | 112 | A real consumer group (replaced a no-op skeleton) |

The split between `events` and `kafka` is what makes the buffering, batching and drop rules testable
without a broker. Every rule in §3.3 is proven against a fake `Sink` in a plain `go test`.

### 4.2 The envelope

```go
type Envelope struct {
    ID            string          `json:"id"`
    Type          string          `json:"type"`             // the domain.Action wire name
    SchemaVersion int             `json:"schema_version"`
    OccurredAt    time.Time       `json:"occurred_at"`
    Source        string          `json:"source"`           // Fly machine id
    ActorAccount  string          `json:"actor_account,omitempty"`
    ActorRole     string          `json:"actor_role,omitempty"`
    TargetTable   string          `json:"target_table,omitempty"`
    TargetID      string          `json:"target_id,omitempty"`
    Payload       json.RawMessage `json:"payload,omitempty"`
}
```

The field set is **deliberately the audit row's field set and nothing more**. A richer, per-event-type
Go struct was the obvious alternative and is the wrong trade: it would mean a new type, marshaller and
test for each of two dozen actions, all to restate what the action's own payload already carries.
Consumers that need specifics parse `Payload` — which is the *same bytes* the ledger stored, so a
consumer and an auditor reading the same event can never disagree about what it said.

Three fields carry non-obvious decisions:

- **`ID` is unique per publish, not per fact.** Two publishes of the same audit row (a retry, a
  replay) carry different IDs and the same `Type`/`TargetID`, so a consumer deduplicates on what it
  actually cares about rather than on a number the producer minted twice.
- **`OccurredAt` is the producer's clock, not the ledger's.** The database `created_at` is
  authoritative, but reading the row back after committing would cost a query per write — the exact
  cost this stream exists to avoid. So it reports what it knows and lets a consumer needing exact
  ledger time go read the ledger.
- **`Source` is load-bearing, not cosmetic.** It is what lets a machine ignore its own broadcast on
  the cache bus. `FLY_MACHINE_ID` is set by the Fly runtime and nothing else, so it answers "which
  machine am I" without inventing a secret an operator must remember to set — the same reasoning the
  rate limiter already uses for `FLY_APP_NAME`.

### 4.3 Partition keys

Kafka orders records **within a partition and nowhere else**, so `Envelope.Key()` decides what "in
order" means:

1. **`TargetID`** — the ordering that can actually be got wrong is per-row: a create and a later
   removal of the same `od_records` row must not be consumed backwards.
2. **`ActorAccount`** — keeps one coordinator's stream coherent when there is no target (the auth
   events have none).
3. **`Type`** — last resort. Keeps a whole event type on one partition: slow, but never wrong.

### 4.4 Topics

| Topic | Carries | Partitions | Retention | Why |
|---|---|---:|---:|---|
| `od.audit.v1` | every committed domain event | 3 | 30 days | Long enough for a consumer to be down a weekend and catch up |
| `od.marks.v1` | attendance events only (`od.*`) | 3 | 7 days | Its consumers (a live board, an export) must never fall behind a slow archival consumer |
| `od.cache.v1` | cache invalidations | 1 | **1 hour** | Instructions acted on in milliseconds or worthless; one partition makes ordering total |

`od.marks.v1` is a **separate stream, not a filter** over the audit topic. An `od.created` event is
produced twice — Kafka has no server-side fanout — which costs two small records on a path that
already batches, and buys consumers that never have to filter a firehose.

Routing matches on the `"od."` **prefix** rather than an enumerated list of the three current actions,
so a new `od.*` action added later reaches the marks stream automatically. A missed routing entry
would be silent: the event would still be on the audit topic and nobody would notice until a board
went stale.

The `.v1` suffix versions the **stream**; `SchemaVersion` on the envelope versions the **record**. A
consumer that meets a version it does not understand skips the record and says so, rather than letting
`json.Unmarshal` fill in the fields it recognises and silently drop the rest.

---

## 5. The cache bus — fixing a limitation the code already documented

### 5.1 The problem, in the codebase's own words

`cache.TTL.Clear`'s doc comment has said this all along:

> For the case where a change invalidates entries the caller cannot NAME. […] with several machines
> serving, only the one that took the write knows anything the others do not, and pretending otherwise
> would make this machine confident about state it has no more authority on than its neighbours.
> Dropping means it re-reads; the others expire on their own.

That was the honest trade when there was no way to tell the others. In practice it means an
administrator who archives an event, or forces a user out, watches **their own** screen update
immediately while the **other** Fly machine keeps serving the pre-change answer for the rest of the
cache window — 60 s for the marking scope, 30 s for principals, 10 s for revocations.

Which answer a given request gets depends on which machine it lands on. So the symptom is
**intermittent and machine-dependent**, and reads as a bug in the cache rather than a property of it.

### 5.2 The fix

The bus publishes an invalidation naming a closed set of scopes, and every machine applies it:

```go
const (
    ScopeRevocation   Scope = "revocation"
    ScopePrincipal    Scope = "principal"
    ScopeMarkingScope Scope = "marking_scope"
    ScopePublicRead   Scope = "public_read"
)
```

A closed set rather than a free string, so a typo cannot produce an invalidation no consumer listens
for — the failure that would present as "the cache bus works, except for revocations", found weeks
later.

**Local first, then broadcast.** The local clear is synchronous and certain; the broadcast is neither.
Doing the certain thing first means the machine that took the write is never the one serving a stale
answer — which is the case an administrator actually observes, because they are looking at the screen
served by the machine they just wrote through.

**A machine ignores its own broadcast**, matched on `Source`. It already cleared synchronously;
re-clearing on the round trip would discard entries repopulated in the interim for no benefit.

**An invalidation naming no scope means everything, not nothing.** Over-clearing costs a reload;
under-clearing serves a stale permission. Those are not comparable prices.

### 5.3 The adapter that made this a small diff

`cmd/api` already hands `interface{ Clear() }` values to `revokeDroppingCaches` and
`eventEditsDroppingScopeCache`. `Bus.Clearing(reason, scopes...)` returns exactly that interface, so
those two types never learn a broker exists:

```go
stream.Cache.Register(cachebus.ScopeRevocation, revCache)
stream.Cache.Register(cachebus.ScopePrincipal, principalCache)
admin.POST("/users/:user_id/sessions/revoke",
    handleradmin.RevokeSessionsHandler(revokeDroppingCaches{
        inner:      revSvc,
        revocation: stream.Cache.Clearing("session revoked", cachebus.ScopeRevocation),
        principals: stream.Cache.Clearing("session revoked", cachebus.ScopePrincipal),
    }))
```

What changes is the **reach** of a clear, not its meaning. With no broker configured the bus clears
locally and publishes to `events.Nop` — byte-for-byte the behaviour these lines had before it existed.

### 5.4 Broadcast, not a consumer group — and why that distinction is a trap

A consumer group delivers each message to **exactly one** member. For the cache bus that is the precise
opposite of what is wanted, and it would leave the fleet *worse* than before: with two machines in a
group, each invalidation reaches one of them, so half the fleet keeps serving stale answers and the
symptom is intermittent by machine.

So the api's cache consumer joins **no group** and starts at the **end** of the log — a notification
sent before this process existed is not news. The worker, whose job really is work, uses a group.

This is also why `streamctl -tail` is groupless: a diagnostic must not take partitions away from the
worker for as long as somebody leaves a terminal open.

---

## 6. Transaction-scoped pending events

`Store.Audit` writes a **second** audit row inside a transaction the caller already opened. The
dispute-approval path is the case that forced it into existence: the decision (`dispute.resolved`) and
the attendance record it created (`od.created`) are different facts about different tables.

That row cannot be published when it is written — its transaction has not committed and may still roll
back. So it is **held**, keyed on the transaction:

```go
pendingMu sync.Mutex
pending   map[pgx.Tx][]AuditEvent
```

Keyed on the transaction because that is exactly the scope the question is asked at: *did the tx these
belong to commit?* A field on the `Store` would be wrong under concurrency — two coordinators marking
at once would publish each other's events — and passing a collector down through the write callback
would change the signature every service implements.

`WithAudit` publishes them after a successful commit, and `defer s.dropPending(tx)` runs on **every**
exit, so a rolled-back transaction leaves nothing behind — both because those events did not happen,
and because a map keyed on transactions that is only cleaned on the success path is a slow leak.

Without this, the marks topic would be missing every dispute-created record: a **silent, partial
stream**, which is worse than no stream because it looks complete.

---

## 7. Configuration

All of it optional. Unset is valid and yields a working default; **set-but-garbage fails loudly** —
the same rule the rest of this codebase's config follows.

| Variable | Default | Notes |
|---|---|---|
| `KAFKA_BROKERS` | *(empty)* | **Empty = the whole feature is off.** Comma-separated seed brokers |
| `KAFKA_TLS` | `true` | Every hosted broker requires it; a local container opts out |
| `KAFKA_SASL_MECHANISM` | *(empty)* | `plain`, `scram-sha-256`, `scram-sha-512` |
| `KAFKA_SASL_USER` / `_PASSWORD` | *(empty)* | Server-side only; never shipped to web/mobile |
| `KAFKA_CLIENT_ID` | `od-amrita-api` | Shows up in broker logs and metrics |
| `KAFKA_CONSUMER_GROUP` | `od-amrita-worker` | The worker's group; the cache bus uses none |
| `KAFKA_LINGER_MS` | `200` | How long a partial batch waits for company |
| `KAFKA_PRODUCE_TIMEOUT_SECONDS` | `10` | Bounds one produce round trip |
| `KAFKA_BUFFER_SIZE` | `4096` | An order of magnitude above the busiest measured day |
| `KAFKA_BATCH_SIZE` | `256` | Records per produce request |

Three refusals are deliberate and are each a test:

- **An unrecognised mechanism fails the boot.** Falling back to "no authentication" on a typo is the
  worst of both: it dials in the clear, and it presents as a broker-side auth error far from the
  variable that caused it.
- **Credentials with no mechanism are refused.** The more dangerous half of the same typo — it would
  dial unauthenticated while the operator believed otherwise.
- **SASL `PLAIN` without TLS is refused.** PLAIN sends the password unencrypted at the protocol level.
  Every hosted broker that offers it also requires TLS, but a self-hosted cluster would happily accept
  it. There is deliberately **no `InsecureSkipVerify` knob** to reach for at 2am.

Units are in the variable names — `_MS` is milliseconds, `_SECONDS` is seconds — matching every other
timing setting in this codebase.

---

## 8. The bug the round trip caught, which nothing below it could

This is the most useful thing in this document, because it is a clean example of a class of bug that
unit tests structurally cannot find.

### 8.1 What happened

`TopicsFor(eventType)` routes by event type and **defaults to the audit mirror**:

```go
topics := []string{TopicAudit}
if strings.HasPrefix(eventType, "od.") { topics = append(topics, TopicMarks) }
return topics
```

The cache bus declared its own event type locally:

```go
const eventType = "cache.invalidated"   // in package cachebus
```

The router had never heard of it. So every cache invalidation was produced to **`od.audit.v1`** — a
topic the api's bus consumer does not subscribe to. The broadcast reached **no consumer at all**, while
the audit mirror filled with instructions that are not domain facts and would be consumed by the worker
as if they were.

### 8.2 Why every test passed anyway

- The **cachebus** unit tests passed: they call `Bus.Handle` directly with a hand-built envelope, so
  routing is never exercised.
- The **topics** unit tests passed: they iterate `domain.AllActions`, and `cache.invalidated` is
  deliberately not a registered action, so it was never in the test's input set.
- The **broker round trip's own echo test passed vacuously.** It asserted "cleared exactly once" — which
  is *also* true when nothing is delivered at all.

Only the peer-machine test failed, and only because it waited for a message that had to actually
arrive.

### 8.3 The fix, and the two tests added

The constant moved to `internal/events/topics.go`, **beside the routing table**, so the two cannot be
changed independently. The router returns early rather than adding to the default list, so the
exclusion from the audit mirror is a property of the code rather than of remembering to filter:

```go
if eventType == TypeCacheInvalidated {
    return []string{TopicCache}
}
```

Two regression tests, because each half is separately wrong: reaching the cache topic is what makes
the bus work, and staying **off** the audit mirror is what keeps the audit stream an audit stream. A
third asserts `cache.invalidated` is not a registered `domain.Action`, so it can never enter the
immutable ledger.

And the vacuous test was fixed: it now publishes a **second, peer-sourced** message and waits for that
one to land, so the pipe is proven working before "exactly once" means anything.

### 8.4 The lesson

A test that asserts *"X happened exactly N times"* passes both when the system is correct and when the
system is dead. Any such assertion needs a companion that proves the machinery is alive — otherwise it
is a test that reports green precisely when things are most broken.

---

## 9. Testing

| Suite | Build tag | Count | Needs |
|---|---|---:|---|
| `internal/events` | — | 13 | nothing |
| `internal/cachebus` | — | 9 | nothing |
| `internal/kafka` | — | 10 | nothing |
| `internal/store` (publish) | — | 8 | nothing |
| `cmd/api` (composition) | — | 3 | nothing |
| `test/store` (ordering) | `integration` | 6 | Postgres |
| `test/stream` (round trip) | `stream` | 4 | Kafka |

### 9.1 The tests that matter most

**`TestAsyncPublisher_PublishNeverBlocksWhenTheSinkIsStuck`** — the sink is held open and the buffer
is one deep, so every call after the first has nowhere to go and *must* return anyway. If this ever
fails, a stalled broker stalls the marking path.

**`TestAsyncPublisher_BatchesAreNotAliased`** — a correctness trap, not a style point. `run()` reuses
one slice per batch; a sink that keeps the slice it was handed (franz-go's async produce does exactly
that) would see its records overwritten by the next batch — **publishing one student's mark under
another student's envelope.**

**`TestStream_NothingIsPublishedWhenTheTransactionRollsBack`** — the business write inserts a row and
*then* fails, so the transaction really rolls back real work. If the stream could publish without a
commit, a consumer would act on a mark the database undid: a live board showing a student present who
is not, with no row anywhere to explain it.

**`TestStream_ExtraAuditRowsAreAlsoPublished` / `...DiscardedOnRollback`** — the pending-events
mechanism from §6, in both directions.

**`TestRun_WriteBoundaryCarriesTheEventPublisher`** — a composition guard, checked against `main.go`'s
source. `store.New(pool)` without `WithPublisher` compiles, passes every test in every package, and
produces a server publishing **nothing at all** — no error, no log line, just an empty topic. Nothing
below `cmd/api` can catch it, because every other test builds its own object graph. This is the same
technique `routes_test.go` already uses to guard the admin group.

**`TestRun_CacheInvalidationGoesThroughTheBus`** — passing `revCache` directly (as this file did
before the bus) also compiles and also passes everything, and quietly reverts fleet-wide invalidation
to single-machine. It also counts registrations, because `Clearing()` on an unregistered scope
broadcasts correctly and clears **nothing locally** — the worse half to get wrong.

### 9.2 Why `test/stream` has its own build tag

`integration` means "a Postgres is running". These need a Kafka. Folding them together would make the
whole integration suite fail on a machine that has a database but no broker — which is the normal
state of every machine in this project, and of every CI job.

### 9.3 Results

- `go build ./...`, `gofmt -l`, and `go vet` under the default, `integration` and `stream` tags — all
  clean.
- Full unit suite — green.
- Full `make test-integration` — green, all 8 packages, against local Postgres 17.
- `make test-stream` — green, all 4, against a real single-node Redpanda.

### 9.4 End-to-end

Beyond the suites: a write through the **real** `store.WithAudit`, with the **real** `eventstream`
runtime, against the **real** local Postgres and Redpanda, with the **real** `cmd/worker` consuming.

```
eventstream: enabled, source=shruhath-omen-cachyos topics=[od.audit.v1 od.marks.v1 od.cache.v1]
committed target_id=e2e-1787068381143267916
eventstream: closed (sent=1 dropped=0 failed=0)

worker: od.created target=od_records/e2e-1787068381143267916 actor=coord@amrita.edu
        source=shruhath-omen-cachyos {"slot":3,"capture_method":"nfc","context":"e2e"}
worker: od.created target=od_records/e2e-1787068381143267916 …   ← twice: audit + marks topics
```

And in Postgres:

```
 od.created | e2e-1787068381143267916 | {"slot": 3, "context": "e2e", "capture_method": "nfc"}
```

The payload is byte-identical on both sides, which is the concrete form of "a consumer and an auditor
read the same account of an event".

### 9.5 The disabled path, booted for real

`cmd/api` started with `KAFKA_BROKERS` unset: all routes registered, `/health` returned **200**, and
`grep -ci "kafka\|eventstream"` over the boot log returned **0**.

---

## 10. Operations

### 10.1 Local development

None of this is needed to run the API.

```
make kafka-up       # single-node Redpanda on 127.0.0.1:19092
make kafka-topics   # provision with the intended retention/partitions
make kafka-tail     # follow the stream
make test-stream    # the broker-backed round trip
make kafka-down     # remove it
```

Redpanda rather than Kafka with ZooKeeper or KRaft: one container, no coordinator to keep alive, the
same protocol franz-go speaks to a real cluster, and it starts in a couple of seconds. A local broker
exists so a developer can watch events flow, and anything heavier would simply not get run. Pinned by
tag, like every other tool here — a broker that changes under you is a debugging trap.

### 10.2 Turning it on in production

```bash
flyctl secrets set --app od-amrita-api \
  KAFKA_BROKERS="seed-1.example.com:9092,seed-2.example.com:9092" \
  KAFKA_TLS=true KAFKA_SASL_MECHANISM=scram-sha-256 \
  KAFKA_SASL_USER=od-amrita KAFKA_SASL_PASSWORD='...'
```

Then provision the topics **once**, so they get the retention and partition counts above rather than
the broker's auto-creation defaults — the one-hour cache retention especially:

```bash
cd api && KAFKA_BROKERS=... go run ./cmd/streamctl -provision
```

Topic creation lives in `streamctl` and `cmd/worker`, **not** in the api. Creating a topic is a
cluster-wide administrative act, and an autoscaling web process should not perform one; a managed
cluster will usually refuse it anyway, turning a normal boot into a scary log line about a permission
the process should never have wanted. `EnsureTopics` treats `TOPIC_ALREADY_EXISTS` as success, so it
is safe to re-run.

To turn it off: unset `KAFKA_BROKERS` and restart. Nothing to drain — the events mirror rows that are
already committed.

### 10.3 `cmd/streamctl`

| Flag | Does |
|---|---|
| `-provision` | Create the topics with the intended config, then exit. Idempotent |
| `-tail` | Print events as they arrive. **Joins no consumer group** |
| `-topics` | Which topics to tail (default: all three) |
| `-from-start` | Tail from the beginning rather than from now |

It reads the same `KAFKA_*` environment the api and worker do, so there is no second place a broker
address or credential is configured.

---

## 11. What was deliberately not done

**No outbox table.** An outbox drained by the worker would make delivery exactly-once-ish and survive
a broker outage. It also means a write to a second table inside every business transaction, plus a
poller — on a database whose cost this project has spent a release reducing. If lost events ever
matter, an outbox is the right answer; making `Publish` return an error is not.

**No typed per-event structs.** See §4.2.

**No consumer that does real work.** `cmd/worker` logs what it sees, and that is the honest state of
it rather than a placeholder somebody forgot to fill in. What exists is the plumbing plus a consumer
proving end to end that events produced by the api are readable by another process — the thing that
had to be true before an export or a push-fed live board could be written.

**The worker is not deployed**, and there is no Fly app for it. A process whose only job is to log
costs money and produces a log nobody reads.

**No CI job for `test/stream`.** No CI runner has a broker, and adding a Redpanda service container
would cost ~1–2 min per run against a private-repo Actions budget of 2,000 min/month — for a feature
that is switched off. Worth adding when the stream is turned on for real.

**Nothing was weakened to make anything pass.** The audit row still co-commits inside the business
transaction. `WithAudit`'s registry gate is untouched. No test was relaxed.

---

## 12. Open items

| # | Item | Tag |
|---|---|---|
| 1 | Keep the stream **off** in production through the 28 Jul event | 🧭 DECISION |
| 2 | `go mod tidy` once with GitHub access — franz-go pulled the `golang.org/x/*` indirects forward, and `tidy` cannot run here (it tries to fetch the workspace-local contracts module from GitHub and fails on credentials), so `go.mod` was hand-adjusted to mark franz-go and kadm direct | ⚠️ ACTION |
| 3 | Do not deploy `cmd/worker` until a consumer does real work | 🔬 UNVERIFIED |
| 4 | Add a Redpanda service container + `-tags stream` step to `api-ci` when the stream goes live | 🕓 DEFERRED |

All four are recorded with their revert instructions in the local (unpushed) delivery log.

### The dependency churn, in detail

Adding franz-go moved these forward:

| Module | Before | After |
|---|---|---|
| `golang.org/x/sync` | 0.17.0 | 0.20.0 *(now a direct require)* |
| `golang.org/x/crypto` | 0.41.0 | 0.50.0 |
| `golang.org/x/net` | 0.43.0 | 0.52.0 |
| `golang.org/x/sys` | 0.35.0 | 0.43.0 |
| `golang.org/x/text` | 0.29.0 | 0.36.0 |
| `golang.org/x/tools` | 0.36.0 | 0.43.0 |
| `golang.org/x/mod` | 0.27.0 | 0.34.0 |

Plus new indirects: `franz-go/pkg/kmsg`, `klauspost/compress`, `pierrec/lz4/v4`. Everything builds and
both suites pass, but it is a wider `go.mod` diff than the feature itself, and it deserves one clean
`tidy` run somewhere with network access to GitHub.

---

## 13. Files

**New**

```
api/internal/events/events.go              api/internal/events/async.go
api/internal/events/topics.go              api/internal/events/async_test.go
api/internal/events/topics_test.go
api/internal/kafka/config.go               api/internal/kafka/sink.go
api/internal/kafka/consumer.go             api/internal/kafka/admin.go
api/internal/kafka/sasl.go                 api/internal/kafka/config_test.go
api/internal/cachebus/cachebus.go          api/internal/cachebus/cachebus_test.go
api/internal/eventstream/eventstream.go
api/internal/store/publish.go              api/internal/store/publish_test.go
api/cmd/streamctl/main.go
api/cmd/api/event_stream_test.go
api/test/store/event_stream_test.go
api/test/stream/round_trip_test.go
```

**Modified**

```
api/internal/store/tx.go     publisher field, pending registry, post-commit publish
api/cmd/api/main.go          stream construction, WithPublisher, bus registration
api/cmd/worker/main.go       a real consumer group, replacing the no-op skeleton
api/internal/config          (untouched — Kafka config lives in internal/kafka)
api/Makefile                 kafka-up / -down / -topics / -tail, test-stream
api/.env.example             the KAFKA_* block
docs/deployment-runbook.md   §7.4, and a correction to §7.2's staleness claim
api/go.mod, api/go.sum, go.work.sum
```

---

## 14. Summary

| | Before | After (stream **on**) |
|---|---|---|
| Publish sites in the codebase | — | **1** (`store.WithAudit`, post-commit) |
| Domain events reaching a consumer | 0 (poll `audit_log`) | every committed event, pushed |
| Cache invalidation reach | the one machine that took the write | **every machine**, in milliseconds |
| Peer-machine staleness after an admin edit | up to 60 s, intermittent by machine | milliseconds |
| Risk to the marking path | — | **none by construction**: post-commit, non-blocking, error-free interface |
| Behaviour with the broker down | — | events dropped and counted; caches fall back to TTL |
| Behaviour with `KAFKA_BROKERS` unset | — | **identical to before this change** |

The stream is built, tested against a real broker end to end, committed, and switched off. Turning it
on is one `flyctl secrets set` and one `streamctl -provision`. My recommendation is to leave it off
until a consumer exists that pays for the broker — a live board fed by push rather than by polling
Postgres is the obvious first one, and it is the same argument the caching work already made.
