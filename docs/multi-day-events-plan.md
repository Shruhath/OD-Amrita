# Multi-day events — plan

**Status:** approved approach, not yet built.
**Written:** 2026-08-03. **Revised:** 2026-08-06.
**Decision: build a real date range on one event (Option A).**

**Trigger:** the upcoming event runs for **30 days**. The current model assumes an
event is a single calendar day, and the workaround (one event per day) means
re-creating categories and coordinator assignments thirty times.

> **Revision note — this document previously recommended Option B (an event
> *series*: keep one event per day, add a bulk-create endpoint) and was reversed on
> 2026-08-06.** The reversal is kept visible rather than edited away, because the
> reasoning is the useful part. Two things moved it:
>
> 1. **Option B only solves creation.** Adding a coordinator on day 12 still means
>    19 assignments; adding a category mid-festival, likewise; raising the credit
>    cap, likewise. Each needs its own fan-out endpoint, and after three of those
>    you have rebuilt "this is one event" on top of thirty rows — with nothing
>    enforcing that the thirty stay in sync.
> 2. **The safety argument for B was priced against data that does not exist.** B's
>    case rested on never touching `od_records`. The live database holds **1
>    `od_record`, 0 credit-usage rows and 2 events**, so the backfill risk that
>    justified the detour is theoretical.
>
> A 30-day festival *is* one event. The `series_id` that Option B introduced was a
> shadow entity standing in for exactly that, and a worse version of it.

---

## 1. The problem in one paragraph

`events.event_date` is a single `date NOT NULL` (`api/db/migrations/0005_events.sql`)
and `CreateEventRequest.event_date` is a single `format: date`
(`contracts/openapi.yaml`). There is no start/end anywhere. A 30-day festival
therefore has to be modelled as 30 events, each needing its own categories and its
own coordinator assignments — which is not a realistic amount of manual admin.

## 2. What the one-day assumption is wired into

Four mechanisms are keyed to it. **None of them fail loudly** if an event simply
spans more days — they produce wrong answers quietly, which is worse. This section
is therefore both the risk register and the change list.

### 2.1 The duplicate rule has no date

```sql
-- api/db/migrations/0011_od_records.sql
CREATE UNIQUE INDEX uq_od_records_present_student_event_slot
    ON od_records (student_id, event_id, slot)
    WHERE status = 'present';
```

This index is what enforces FR10's "one OD per student per slot". `od_records` has
**no date column at all** — only `server_received_at` and `created_at`.

Left alone on a 30-day event: a student marked in slot 8 on day 1 can never be
marked in slot 8 again for the rest of the month. The second mark collides with the
index and is rejected as a duplicate.

### 2.2 The counters have no date

```sql
-- api/db/queries/od_records.sql — CountMarksForCategory
WHERE event_id = @event_id
  AND category_id = @category_id
  AND status = 'present';
```

Both figures on the marking screen ("Marked by you", "Marked in total") would show
a running 30-day cumulative total on a screen coordinators read as *today*.

### 2.3 The fallback-credit cap is per event, not per day

`event_capture_credit_config` holds one `credit_limit` per event and
`event_capture_credit_usage` is keyed `(event_id, user_id)`
(`api/db/migrations/0013_capture_credits.sql`).

Left alone: a coordinator gets **5 camera/search credits for the entire month**
rather than 5 per day. The cap silently becomes ~30× stricter than intended.

### 2.4 The slot gate

```go
// api/internal/service/od_record/schedule.go
if ny != ey || nm != em || nd != ed {
    return 0, SlotWindow{}, false
}
```

`ActiveSlot` returns "no slot open" unless today *equals* the event date, before it
ever consults the slot table.

### 2.5 The public dashboard groups by `events.event_date`

`ListPublicStudentODRecords` carries an explicit comment saying so, and saying why
it does not use `server_received_at`:

> *Deriving the day from the receipt time would file a teacher's next-morning
> correction under the wrong day, which on this screen reads to the student as "I
> was not marked".*

That comment is the single most important input to this design. It means the day an
OD **belongs to** is not the same fact as the time the row was **written**, and once
an event spans days, that difference needs somewhere to live. See §5.2.

---

## 3. The two options

### Option A — a real date range on one event *(chosen)*

Add `end_date` to `events` and thread an explicit OD date through the records.

| Area | Change |
|---|---|
| `events` | Add `end_date date NOT NULL`, `CHECK (end_date >= event_date)`, backfill `= event_date` |
| `od_records` | Add `od_date date NOT NULL`, backfill in campus time, constrain inside the event range |
| `od_records` indexes | Unique index becomes `(student_id, event_id, od_date, slot)`; supporting indexes gain `od_date` |
| `event_capture_credit_usage` | Add `usage_date` to the primary key; consume/read/reset become per-day |
| `ActiveSlot` | Date equality → range containment |
| Mark write | Populate `od_date` from server campus time |
| Counters | Add the date predicate |
| Public dashboard | Group by `od_records.od_date` instead of `events.event_date` |
| Contract | `end_date` on `Event` and `CreateEventRequest`; regenerate; drift gate |
| Web | Range input on the create form, range display in list and detail |

### Option B — an event series *(rejected)*

Keep one event per day; add a bulk-create endpoint that stamps out N days sharing a
`series_id`, and scope the marking selector to today.

Genuinely cheaper on day one — nothing touches `od_records` — and it makes each
day's counters and credit pool correct for free. Rejected for the two reasons in the
revision note above: it solves creation only, and it models a festival as thirty
things that must be kept in step by convention rather than by constraint.

---

## 4. Why A wins

1. **One event is one row, so every later operation is one write.** Assigning a
   coordinator, adding a category, archiving, and raising the credit cap each stay
   single-row operations for the whole festival. Under B each becomes a fan-out.

2. **The credit cap raise keeps working when it is needed.** The live cap raise is
   the documented fallback if NFC fails at the venue. Under A it is one `PUT` that
   applies to every remaining day; under B it lifts one day out of thirty unless a
   series-wide endpoint is built first. That is the wrong moment to discover a
   missing endpoint.

3. **The model matches reality**, so reports, exports and the dashboard all key on
   an explicit OD date rather than on a convention about how many event rows exist.

**What A costs that B does not:** it modifies the FR10 uniqueness rule and re-keys
the credit usage table. Both are mechanical, and both are being done against 1 OD
record and 0 usage rows.

---

## 5. Implementation

> **Migration numbering corrected 2026-08-06.** Steps 1-3 originally claimed
> `0014`/`0015`/`0016`, but the disputes work's `0014_disputes.sql` also claims
> `0014`. The disputes work is ahead of this plan, so **disputes keeps `0014`** and
> the steps below shift to `0015`/`0016`/`0017`. Re-check against the highest
> version actually present in `api/db/migrations/` before writing them — goose
> refuses a duplicate version, and two changes in flight can shift it again.

Ordered so each step is independently revertible. Next migration number is `0014`.

### Step 1 — `end_date` on `events` (migration `0015`)

```sql
ALTER TABLE events ADD COLUMN end_date date;
UPDATE events SET end_date = event_date WHERE end_date IS NULL;
ALTER TABLE events ALTER COLUMN end_date SET NOT NULL;
ALTER TABLE events ADD CONSTRAINT events_date_range_ck
    CHECK (end_date >= event_date);
```

`event_date` is **kept as the range start** rather than renamed to `start_date`. A
rename would churn queries, generated code, the contract and the web surface for a
cosmetic gain; a column comment recording that `event_date` is the first day is
enough. Every existing event becomes a one-day range, which is what it already was.

Bound the range length in the service (suggest ≤ 90 days) so a mistyped year cannot
create a decade-long event.

### Step 2 — `od_date` on `od_records` (migration `0016`)

**This is the load-bearing step.** `od_date` is the day the OD *belongs to*, which
is deliberately not the same fact as `server_received_at`:

- For a **live coordinator mark**, the server derives it as the current campus date.
  It is never accepted from the client — a client-settable date is a way to file a
  mark into yesterday.
- For a **teacher/admin manual write**, it is **chosen by the
  teacher**, because a correction typed on Tuesday morning may well be fixing
  Monday. This is precisely the case `ListPublicStudentODRecords` already warns
  about, and the reason a derived-from-receipt-time column would be wrong.

```sql
ALTER TABLE od_records ADD COLUMN od_date date;
UPDATE od_records
   SET od_date = (server_received_at AT TIME ZONE 'Asia/Kolkata')::date
 WHERE od_date IS NULL;
ALTER TABLE od_records ALTER COLUMN od_date SET NOT NULL;

DROP INDEX uq_od_records_present_student_event_slot;
CREATE UNIQUE INDEX uq_od_records_present_student_event_date_slot
    ON od_records (student_id, event_id, od_date, slot)
    WHERE status = 'present';
```

`NOT NULL` is not optional. In Postgres, NULLs are distinct in a unique index, so a
single NULL-dated row would never collide with anything and duplicates would come
back silently.

The backfill is safe for the one existing row because it is a live coordinator mark,
where derived and belonged-to date coincide.

Add `od_date` to the three supporting indexes
(`od_records_event_category_slot_idx`, `od_records_student_event_slot_idx`,
`od_records_actor_event_slot_idx`) so the counters and reads stay covered.

**Also add a range guard.** `od_records` already carries
`require_active_event_for_child_mutation('event_id')`; add a sibling trigger
asserting `od_date BETWEEN events.event_date AND events.end_date`. Without it, the
teacher-chosen date in 0.12b can file a mark outside the festival entirely, and
nothing downstream would notice.

### Step 3 — per-day credit usage (migration `0017`)

```sql
ALTER TABLE event_capture_credit_usage ADD COLUMN usage_date date;
UPDATE event_capture_credit_usage SET usage_date = ... ;  -- zero rows today
ALTER TABLE event_capture_credit_usage ALTER COLUMN usage_date SET NOT NULL;
ALTER TABLE event_capture_credit_usage DROP CONSTRAINT event_capture_credit_usage_pkey;
ALTER TABLE event_capture_credit_usage ADD PRIMARY KEY (event_id, user_id, usage_date);
```

`event_coordinator_assignments.fallback_credit_limit` stays on the coordinator
assignment — one cap for that coordinator across days — while only *usage*
becomes per-day.

The existing FK to `event_coordinator_assignments (user_id, event_id)` is unaffected
by the primary-key change.

### Step 4 — `ActiveSlot` becomes range containment

```go
// api/internal/service/od_record/schedule.go
func ActiveSlot(now time.Time, eventStart, eventEnd time.Time) (int, SlotWindow, bool)
```

Campus date must be `>= eventStart` and `<= eventEnd`, then the existing slot scan
runs against **today's** date rather than the event's. Expose the campus-local date
alongside, since the mark path needs it for `od_date`.

### Step 5 — the write and read paths

- Mark write: populate `od_date`; debit credit keyed on `(event_id, user_id, od_date)`.
- `CountMarksForCategory`: add `AND od_date = @od_date`.
- `ListPublicStudentODRecords`: group and order by `r.od_date`, not `e.event_date`.
- Attendance/admin reads: filter by day where they currently imply it.

### Step 6 — contract and web

- `end_date` on `Event` and `CreateEventRequest`; `make -C contracts generate`; the
  `contract-drift` gate must be green.
- Web: a start/end date range on the create form, and range display wherever a
  single date is shown today.

### Step 7 — the empty-day case

A coordinator opening `/mark` on a day inside the range but with no slot open, or on
a day outside it, should read as "nothing scheduled" — not as an error.

---

## 6. Decisions needed before building

1. **Skipped days.** A 30-day range currently means every day in it is markable. If
   the festival skips Sundays, nothing prevents a mark that day. Simplest v1: no
   exclusion list — a coordinator simply does not mark. Flag if that is wrong.
2. **Range length bound.** 90 days suggested. Confirm.
3. **Credit shape.** Cap per event (one number, applies each day), usage per day.
   Confirm this over one pool for the whole range.
4. **`event_date` kept as the range start** rather than renamed to `start_date`.
5. **Teacher-chosen `od_date` in 0.12b** — confirm a teacher picks the day when
   adding a record manually, constrained to the event range.

---

## 7. Knock-on: the disputes table must be decided first

**The disputes work creates the `disputes` table**, keyed on event +
slot. Once an event spans 30 days, "I was missed in slot 8" is ambiguous, so
`disputes` needs an OD date column from the start.

It is much cheaper to add that column when the table is created than to migrate it
afterwards. If disputes are built before this plan, add `od_date` to `disputes` anyway.

**Status 2026-08-06 — actioned.** The disputes design now carries it: **T21**
(`od_date date NOT NULL` from creation), **T11 amended** (the partial unique index
is keyed `(student_id, event_id, od_date, slot)`), **T22** (a database range guard
asserting the day falls inside the event — the anonymous-submitter analogue of Step
2's `od_records` guard, and explicitly *not* the active-event trigger T9 forbids),
and **T23** (the dispute form's day is a control bounded by the event's range, not
a value derived from `event_date`). All four are asserted by the dispute suites in
`api/test/odrecord/dispute_integration_test.go`, `test/contract/dispute_contract_test.go`
and `web/src/routes/public/PublicDisputeForm.test.tsx`.

When this plan lands, two follow-ups fall out of that work:

- `PublicEvent` gains `end_date`, and it must be added to the public schema
  **allowlist** in `test/contract/dispute_contract_test.go` in the same change —
  the exact-match assertion is what forces that to be a decision rather than a drift.
- `TestDispute_SameSlotOnADifferentDayIsADifferentDispute` in
  `api/test/odrecord/dispute_integration_test.go` currently `t.Skip`s because an
  event is a single day. Un-skip it as soon as Step 1 lands — it is the test that
  proves the multi-day duplicate rule actually works.

## 8. Rollback

- Step 1: `ALTER TABLE events DROP COLUMN end_date` — nothing else reads it until
  Step 4.
- Step 2: recreate the original unique index and drop `od_date`. Reverting **after**
  multi-day marks exist means real duplicates would surface — so this step is the
  point of no return in practice, and is the reason it is done while the table holds
  one row.
- Step 3: restore the two-column primary key; usage rows are disposable.
- Steps 4-6 are ordinary code reverts.
