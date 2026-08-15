//go:build integration

// Schema gates for the multi-day event date range.
//
// RED PHASE. Everything here asserts a NAMED database object — a named column, a
// named CHECK constraint, a named unique index, a named trigger constraint. That
// is not stylistic pedantry: this story's two headline traps both fail SILENTLY,
// and a test that only asks "did the write fail?" cannot see either of them.
//
//   - A nullable `od_date` disables the duplicate guard with no error at all,
//     because Postgres treats NULLs as DISTINCT in a unique index. A single
//     NULL-dated row never collides with anything.
//   - A row rejected by the WRONG constraint looks identical to a row rejected by
//     the right one. Story 0.10's review found three tests that could not fail;
//     asserting the constraint NAME is what makes these able to.
//
// Placed in the EXISTING api/test/db package, which api/Makefile's
// INTEGRATION_PKGS and .github/workflows/api-ci.yml both already run. Stories
// 0.2-0.5 each shipped a new integration package that CI silently never ran.
//
// Run: cd api && make db-up && make test-integration
package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 0.12a-DB-001 — `events.end_date` exists, is NOT NULL, and the ordering is
// guaranteed by a NAMED database CHECK rather than by the service.
//
// T4/T5 split this deliberately. The 90-day LENGTH bound lives in the service
// because it is policy that will be tuned and a CHECK would cost a migration each
// time. The ORDERING is the part that must never be tunable — an event ending
// before it starts makes `ActiveSlot`'s range containment vacuous and every
// per-day counter meaningless — so it is in the database.
func TestMultiDaySchema_EventCarriesANotNullEndDateOrderedByACheck(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	dataType, nullable := columnShape(t, ctx, migrator, "events", "end_date")
	if dataType != "date" {
		t.Errorf("[P0] events.end_date type = %q, want date", dataType)
	}
	if nullable != "NO" {
		t.Errorf("[P0] events.end_date is nullable. Every event has a last day — the migration backfills "+
			"pre-existing rows to end_date = event_date — and a NULL end date makes range containment in "+
			"ActiveSlot silently false for the whole event (is_nullable = %q)", nullable)
	}

	// The CHECK must be real AND named. An unnamed CHECK gets a generated name
	// that changes between environments, which makes the assertion below
	// unwritable and makes a future mapPgError entry impossible to key on.
	if !constraintExists(t, ctx, migrator, "events", "events_date_range_ck") {
		t.Errorf("[P0] constraint events_date_range_ck is missing from events (T4)")
	}

	err := insertEventRange(ctx, migrator, "Backwards range fixture", "2026-08-20", "2026-08-10")
	if !violatesConstraint(err, "23514", "events_date_range_ck") {
		t.Errorf("[P0] an event with end_date BEFORE event_date was accepted, or was refused by the wrong "+
			"constraint. Want SQLSTATE 23514 on events_date_range_ck, got %v.\n"+
			"A test that merely asserts 'the insert failed' passes when a DIFFERENT constraint rejects the row, "+
			"which proves nothing about the range", err)
	}

	// The positive control. A guard that refuses everything would satisfy the
	// assertion above while making every event uncreatable.
	if err := insertEventRange(ctx, migrator, "Valid range fixture", "2026-08-10", "2026-09-08"); err != nil {
		t.Errorf("[P0] a valid 30-day range was refused: %v. events_date_range_ck must assert "+
			"end_date >= event_date, not equality", err)
	}
	// And a single-day event — the shape every pre-existing row is backfilled to —
	// must remain legal, or the migration's own backfill would be rejected.
	if err := insertEventRange(ctx, migrator, "Single day fixture", "2026-08-10", "2026-08-10"); err != nil {
		t.Errorf("[P0] a single-day event (end_date == event_date) was refused: %v. This is the shape every "+
			"pre-existing row is backfilled to, so the migration itself would fail", err)
	}
}

// 0.12a-DB-002 — `events.event_date` records what it MEANS.
//
// T2 keeps the name `event_date` rather than renaming it to `start_date`,
// because a rename churns six SQL queries, the generated sqlc + Go + TS types,
// the contract, five web surfaces and every fixture for a cosmetic gain. The
// price of keeping the name is that the name no longer says what the column is,
// so T2 pays it with a COMMENT — and an undocumented decision to keep a
// misleading name is just a misleading name.
func TestMultiDaySchema_EventDateDocumentsThatItIsTheRangeStart(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	var comment *string
	if err := migrator.QueryRow(ctx, `
		SELECT col_description(c.oid, a.attnum)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid
		WHERE n.nspname = 'public' AND c.relname = 'events' AND a.attname = 'event_date'`).Scan(&comment); err != nil {
		t.Fatalf("read events.event_date comment: %v", err)
	}
	if comment == nil || *comment == "" {
		t.Errorf("[P0] events.event_date has no COMMENT (T2). The column is the range START and is deliberately " +
			"NOT renamed to start_date; the comment is the entire mechanism recording that, and without it the " +
			"next reader sees a column called event_date on a table where an event spans thirty days")
	}
}

// 0.12a-DB-003 — `od_records.od_date` is NOT NULL, and the duplicate guard is
// re-keyed on it under its NEW name.
//
// This is the single most consequential assertion in the story. Postgres treats
// NULLs as DISTINCT in a unique index by default, so ONE nullable-dated row never
// collides with anything and duplicate marks quietly come back — with no error,
// no log line, and the existing duplicate test still green because that test uses
// non-NULL rows.
//
// The index is also RENAMED, and the rename has a live consequence:
// `mapPgError` in api/internal/service/od_record/writer.go matches the OLD name
// as a string literal. Miss it and a duplicate mark stops returning
// SCAN_DUPLICATE (409) and starts returning an unmapped 500 — the FR11 duplicate
// path broken, while the existing duplicate test stays green because the row
// still fails to insert. That is why 0.12a-INT-002 asserts the error CODE.
func TestMultiDaySchema_ODRecordsCarryANotNullODDateInTheDuplicateKey(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	dataType, nullable := columnShape(t, ctx, migrator, "od_records", "od_date")
	if dataType != "date" {
		t.Errorf("[P0] od_records.od_date type = %q, want date", dataType)
	}
	if nullable != "NO" {
		t.Errorf("[P0] od_records.od_date is NULLABLE (is_nullable = %q). This is the story's headline silent "+
			"failure: Postgres treats NULLs as DISTINCT in a unique index, so one NULL-dated row never collides "+
			"and the FR10 duplicate guard stops guarding — with no error anywhere. `NULLS NOT DISTINCT` is NOT "+
			"the fix; the column has no legitimate NULL state", nullable)
	}

	newIndex := "uq_od_records_present_student_event_date_slot"
	oldIndex := "uq_od_records_present_student_event_slot"

	def := indexDef(t, ctx, migrator, newIndex)
	if def == "" {
		t.Fatalf("[P0] unique index %s does not exist (T8)", newIndex)
	}
	for _, column := range []string{"student_id", "event_id", "od_date", "slot"} {
		if !containsIdentifier(def, column) {
			t.Errorf("[P0] %s does not key on %s. Definition: %s", newIndex, column, def)
		}
	}
	// The guard must NARROW, not disappear. A unique index without the partial
	// predicate would also refuse a future absent/removed row, and one without
	// UNIQUE is not a guard at all.
	if !containsIdentifier(def, "UNIQUE") {
		t.Errorf("[P0] %s is not UNIQUE. Definition: %s", newIndex, def)
	}
	if !containsIdentifier(def, "present") {
		t.Errorf("[P0] %s lost its `WHERE status = 'present'` predicate. Definition: %s", newIndex, def)
	}

	if indexDef(t, ctx, migrator, oldIndex) != "" {
		t.Errorf("[P0] the OLD index %s still exists alongside the new one. Two overlapping unique guards mean "+
			"the old one still refuses a legitimate second-day mark, so the feature does not work — and "+
			"mapPgError would keep matching the old name and appear correct", oldIndex)
	}

	// T9: the three supporting indexes carry od_date, or every per-day counter
	// and the public read fall back to a sequential scan on the busiest table.
	for _, name := range []string{
		"od_records_event_category_slot_idx",
		"od_records_student_event_slot_idx",
		"od_records_actor_event_slot_idx",
	} {
		supporting := indexDef(t, ctx, migrator, name)
		if supporting == "" {
			t.Errorf("[P1] supporting index %s is missing (T9)", name)
			continue
		}
		if !containsIdentifier(supporting, "od_date") {
			t.Errorf("[P1] supporting index %s does not include od_date (T9). It covers the per-day counters "+
				"and the public read; without the column those queries scan. Definition: %s", name, supporting)
		}
	}
}

// 0.12a-DB-004 — the DATABASE refuses an `od_date` outside the event's range, and
// says which constraint refused it.
//
// T10. Written DIRECTLY as the runtime role, bypassing the service, so it proves
// the DATABASE refuses the row rather than that the Go validation ran. Service
// validation alone is not enough: 0.12b lets a teacher PICK the day, and at that
// point a mark filed outside the festival entirely would be accepted with nothing
// downstream noticing.
//
// The in-range positive control is in the same test on purpose. A trigger that
// raises unconditionally would satisfy the refusal assertion while making every
// mark impossible — and Story 0.10's review found exactly this class of test.
func TestMultiDaySchema_DatabaseRefusesAnODDateOutsideTheEventRange(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	fixture := seedRangeFixture(t, ctx, migrator, "2026-08-10", "2026-09-08")

	for _, tc := range []struct {
		name   string
		odDate string
	}{
		{"the day before the range starts", "2026-08-09"},
		{"the day after the range ends", "2026-09-09"},
		{"a year outside the range entirely", "2027-08-15"},
	} {
		err := insertODRecord(ctx, runtime, fixture, tc.odDate, 8)
		if !violatesConstraint(err, "23514", "od_records_od_date_event_range_ck") {
			t.Errorf("[P0] %s (%s) was accepted, or refused by the wrong constraint. Want SQLSTATE 23514 on "+
				"od_records_od_date_event_range_ck, got %v.\n"+
				"Model the trigger on require_dispute_od_date_in_event_range in 0014_disputes.sql — same error "+
				"code, same constraint-name convention, same not-found branch. It is a SECOND trigger alongside "+
				"od_records_active_event_guard, not a replacement for it", tc.name, tc.odDate, err)
		}
	}

	// Positive controls at BOTH bounds. Range containment is inclusive, and an
	// exclusive comparison would make day one and the final day unmarkable — the
	// first and last day of a festival being precisely when marking happens.
	for _, tc := range []struct {
		name   string
		odDate string
		slot   int
	}{
		{"the first day of the range", "2026-08-10", 1},
		{"a day in the middle", "2026-08-19", 2},
		{"the last day of the range", "2026-09-08", 3},
	} {
		if err := insertODRecord(ctx, runtime, fixture, tc.odDate, tc.slot); err != nil {
			t.Errorf("[P0] %s (%s) was REFUSED: %v. Containment is INCLUSIVE at both ends — an exclusive "+
				"comparison makes the first and last day of a festival unmarkable, which is exactly when "+
				"marking happens", tc.name, tc.odDate, err)
		}
	}
}

// 0.12a-DB-005 — credit USAGE is per day; the coordinator CAP is not.
func TestMultiDaySchema_CreditUsageIsPerDayWhileTheCapStaysOnAssignment(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	dataType, nullable := columnShape(t, ctx, migrator, "event_capture_credit_usage", "usage_date")
	if dataType != "date" {
		t.Errorf("[P0] event_capture_credit_usage.usage_date type = %q, want date (T12)", dataType)
	}
	if nullable != "NO" {
		t.Errorf("[P0] event_capture_credit_usage.usage_date is nullable (is_nullable = %q); it is part of the "+
			"primary key", nullable)
	}

	pk := primaryKeyColumns(t, ctx, migrator, "event_capture_credit_usage")
	want := []string{"event_id", "user_id", "usage_date"}
	if len(pk) != len(want) {
		t.Errorf("[P0] event_capture_credit_usage primary key = %v, want %v", pk, want)
	} else {
		for i, column := range want {
			if pk[i] != column {
				t.Errorf("[P0] event_capture_credit_usage primary key = %v, want %v", pk, want)
				break
			}
		}
	}

	// The cap lives on the coordinator assignment and must not gain a usage date;
	// usage rows reset daily, limits do not.
	dataType, nullable = columnShape(t, ctx, migrator, "event_coordinator_assignments", "fallback_credit_limit")
	if dataType != "integer" || nullable != "NO" {
		t.Errorf("[P0] event_coordinator_assignments.fallback_credit_limit shape = %s/%s, want integer/NO", dataType, nullable)
	}
	if _, _, exists := optionalColumnShape(t, ctx, migrator, "event_coordinator_assignments", "usage_date"); exists {
		t.Errorf("[P0] event_coordinator_assignments gained a usage_date column. Usage resets daily; the coordinator cap itself must not become per-day")
	}
}

// 0.12a-DB-006 — the dispute range guard was WIDENED, not left single-day.
//
// T6. `0014_disputes.sql` created require_dispute_od_date_in_event_range() with
// `IF NEW.od_date <> event_start`, and left a comment reading "Story 0.12a widens
// this to BETWEEN event_date AND end_date". Missing it means every dispute filed
// for day 2+ of a festival is rejected BY THE DATABASE — a public write path,
// failing for a student, with no test currently covering it.
//
// The single-day case must still be refused when it is genuinely out of range, or
// the widening turned the guard off instead of widening it.
func TestMultiDaySchema_DisputeGuardAcceptsAnyDayInsideTheRange(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	fixture := seedRangeFixture(t, ctx, migrator, "2026-08-10", "2026-09-08")

	for _, odDate := range []string{"2026-08-10", "2026-08-19", "2026-09-08"} {
		if err := insertDispute(ctx, runtime, fixture, odDate, 4); err != nil {
			t.Errorf("[P0] a dispute for %s — inside the event's range — was refused by the database: %v.\n"+
				"require_dispute_od_date_in_event_range() must be CREATE OR REPLACE'd to BETWEEN event_date AND "+
				"end_date (T6). Left single-day, every dispute for day 2+ of a festival is impossible to file, "+
				"on a PUBLIC write path, for a student who was already missed once", odDate, err)
		}
	}

	if err := insertDispute(ctx, runtime, fixture, "2026-09-09", 4); !violatesConstraint(err, "23514", "disputes_od_date_event_range_ck") {
		t.Errorf("[P0] a dispute dated outside the range was accepted, or refused by the wrong constraint: %v.\n"+
			"Widening the guard must not disable it — the submitter here is ANONYMOUS over the open internet, "+
			"and without the guard anyone can file a dispute dated 1970", err)
	}
}

// ---------------------------------------------------------------------------
// Fixtures and probes — real bodies, no logic of their own.
// ---------------------------------------------------------------------------

type rangeFixture struct {
	branchID   string
	eventID    string
	categoryID string
	studentID  string
	actorID    string
}

// seedRangeFixture builds one event with a real date range plus everything an
// od_record and a dispute need to reference.
//
// Ids are freshly generated per call rather than fixed: 0.9 and 0.10 both lost CI
// rounds to roll-number and id collisions across specs sharing a database.
func seedRangeFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, startDate, endDate string) rangeFixture {
	t.Helper()
	fixture := rangeFixture{
		branchID:   uuid.NewString(),
		eventID:    uuid.NewString(),
		categoryID: uuid.NewString(),
		studentID:  uuid.NewString(),
		actorID:    uuid.NewString(),
	}
	// UPPERCASE: `branches_code_upper_chk` and `students_roll_no_canonical_chk`
	// both require it, and a lowercase suffix fails the fixture rather than the
	// assertion — which is how a red-phase test comes to report the wrong reason.
	suffix := strings.ToUpper(fixture.eventID[:8])

	mustExec(t, ctx, pool, "seed branch", `
		INSERT INTO branches (id, code, name) VALUES ($1::uuid, $2, 'Multi-day range fixture')`,
		fixture.branchID, "RANGE-"+suffix)
	mustExec(t, ctx, pool, "seed actor", `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1::uuid, $2, $3, 'Range Fixture Coordinator', 'coordinator')`,
		fixture.actorID, "range-"+suffix, "range-"+strings.ToLower(suffix)+"@amrita.edu")
	mustExec(t, ctx, pool, "seed event", `
		INSERT INTO events (id, name, event_date, end_date, status)
		VALUES ($1::uuid, 'Multi-day range fixture', $2::date, $3::date, 'active')`,
		fixture.eventID, startDate, endDate)
	mustExec(t, ctx, pool, "seed assignment", `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ($1::uuid, $2::uuid, $1::uuid)`,
		fixture.actorID, fixture.eventID)
	mustExec(t, ctx, pool, "seed category", `
		INSERT INTO categories (id, event_id, name) VALUES ($1::uuid, $2::uuid, 'Range Fixture Category')`,
		fixture.categoryID, fixture.eventID)
	mustExec(t, ctx, pool, "seed student", `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1::uuid, $2, 'Range Fixture Student', $3::uuid, 'A', '2024')`,
		fixture.studentID, "CB.SC.U4RNG"+suffix, fixture.branchID)

	t.Cleanup(func() {
		clean := context.Background()
		for _, statement := range []string{
			`DELETE FROM disputes WHERE event_id = $1::uuid`,
			`DELETE FROM od_records WHERE event_id = $1::uuid`,
			`DELETE FROM event_capture_credit_usage WHERE event_id = $1::uuid`,
			`DELETE FROM event_coordinator_assignments WHERE event_id = $1::uuid`,
			`DELETE FROM categories WHERE event_id = $1::uuid`,
			`DELETE FROM events WHERE id = $1::uuid`,
		} {
			_, _ = pool.Exec(clean, statement, fixture.eventID)
		}
		_, _ = pool.Exec(clean, `DELETE FROM students WHERE id = $1::uuid`, fixture.studentID)
		_, _ = pool.Exec(clean, `DELETE FROM users WHERE id = $1::uuid`, fixture.actorID)
		_, _ = pool.Exec(clean, `DELETE FROM branches WHERE id = $1::uuid`, fixture.branchID)
	})
	return fixture
}

func insertEventRange(ctx context.Context, pool *pgxpool.Pool, name, startDate, endDate string) error {
	id := uuid.NewString()
	_, err := pool.Exec(ctx, `
		INSERT INTO events (id, name, event_date, end_date, status)
		VALUES ($1::uuid, $2, $3::date, $4::date, 'active')`, id, name+" "+id[:8], startDate, endDate)
	if err == nil {
		_, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id = $1::uuid`, id)
	}
	return err
}

func insertODRecord(ctx context.Context, pool *pgxpool.Pool, fixture rangeFixture, odDate string, slot int) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method,
			 actor_user_id, actor_role, server_received_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::date, 'present', 'nfc', $7::uuid, 'coordinator', now())`,
		uuid.NewString(), fixture.studentID, fixture.eventID, fixture.categoryID, slot, odDate, fixture.actorID)
	return err
}

func insertDispute(ctx context.Context, pool *pgxpool.Pool, fixture rangeFixture, odDate string, slot int) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO disputes (id, student_id, event_id, category_id, od_date, slot, submitted_email, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::date, $6, $7, 'open')`,
		uuid.NewString(), fixture.studentID, fixture.eventID, fixture.categoryID, odDate, slot,
		"range-dispute-"+uuid.NewString()[:8]+"@amrita.edu")
	return err
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, what, statement string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func columnShape(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) (string, string) {
	t.Helper()
	dataType, nullable, exists := optionalColumnShape(t, ctx, pool, table, column)
	if !exists {
		t.Fatalf("[P0] %s.%s does not exist", table, column)
	}
	return dataType, nullable
}

func optionalColumnShape(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) (string, string, bool) {
	t.Helper()
	var dataType, nullable string
	err := pool.QueryRow(ctx, `
		SELECT data_type, is_nullable FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, table, column).Scan(&dataType, &nullable)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false
		}
		t.Fatalf("read %s.%s shape: %v", table, column, err)
	}
	return dataType, nullable, true
}

func constraintExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, constraint string) bool {
	t.Helper()
	var found bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_constraint c
			JOIN pg_class rel ON rel.oid = c.conrelid
			JOIN pg_namespace n ON n.oid = rel.relnamespace
			WHERE n.nspname = 'public' AND rel.relname = $1 AND c.conname = $2
		)`, table, constraint).Scan(&found); err != nil {
		t.Fatalf("check constraint %s on %s: %v", constraint, table, err)
	}
	return found
}

func indexDef(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var def *string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, name).Scan(&def); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ""
		}
		t.Fatalf("read index %s: %v", name, err)
	}
	if def == nil {
		return ""
	}
	return *def
}

func primaryKeyColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN pg_class rel ON rel.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = rel.relnamespace
		JOIN unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = rel.oid AND a.attnum = k.attnum
		WHERE n.nspname = 'public' AND rel.relname = $1 AND c.contype = 'p'
		ORDER BY k.ord`, table)
	if err != nil {
		t.Fatalf("read primary key of %s: %v", table, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan primary key column of %s: %v", table, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate primary key of %s: %v", table, err)
	}
	return columns
}

// violatesConstraint asserts on the TYPED pgconn error — the SQLSTATE and the
// CONSTRAINT NAME — never on message text. A row refused by a different
// constraint is a different fact, and Postgres message wording is not a contract.
func violatesConstraint(err error, sqlstate, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == sqlstate && pgErr.ConstraintName == constraint
}

// containsIdentifier is a case-insensitive substring test over an index
// definition, so `od_date` matches whether pg_indexes renders it bare or quoted.
func containsIdentifier(definition, identifier string) bool {
	return strings.Contains(strings.ToLower(definition), strings.ToLower(identifier))
}

// The 0016 backfill CLAMPS a legacy row into its event's range.
//
// This is the one migration step whose correctness depends on real rows, and
// nothing else in the suite exercises it: TestMigrationsRoundTrip_Idempotent
// resets and re-applies against an EMPTY database, so the UPDATE runs over zero
// rows and proves nothing.
//
// The hazard is specific. Story 0.9's teacher/admin search-mark is exempt from
// slot windows, is the documented correction path, and before this story had NO
// date check at all — so a correction typed the morning after a one-day event is
// a row that already exists and whose `server_received_at` falls OUTSIDE the
// event. Backfilled unclamped, it becomes permanently illegal data: the range
// trigger validates new writes only and never revalidates existing rows, so the
// row would sit there unreachable from the admin day filter (which enumerates
// only event_date..end_date), rendered on the public dashboard under a day the
// event never had, and raising 23514 on any later UPDATE of event_id/od_date.
//
// Drives goose down to 0015 (dropping od_date), plants exactly that legacy row,
// then migrates up and asserts the clamp — the same rule ClampToEventRange
// applies in Go.
func TestMultiDaySchema_BackfillClampsALegacyRowIntoTheEventRange(t *testing.T) {
	requireGoose(t)
	ctx := testCtx(t)
	migratorDSN := dsnOrSkip(t, envMigratorDSN)
	pool := connect(t, ctx, migratorDSN)

	// Whatever happens, leave the schema fully migrated for every later test.
	t.Cleanup(func() { runGoose(t, migratorDSN, "up") })

	// RESET, not a bare `down-to`. Rolling back over live data is exactly what
	// 0016's and 0017's Down halves cannot do once the feature has been used:
	// the narrower unique index and the two-column primary key both raise 23505
	// on the very rows the Up halves exist to permit. A `down-to 15` therefore
	// fails against any database where a coordinator has spent credits on two
	// days — which is a correct, documented one-way door, but makes it useless
	// as a test primitive. `reset` drops the tables outright, so this test
	// depends on no prior state and leaves none behind.
	//
	// TestMigrationsRoundTrip_Idempotent in this same package already resets the
	// database, so this is the established posture here rather than a new one.
	clearRollbackBlockers(t, ctx, pool)
	runGoose(t, migratorDSN, "reset")
	runGoose(t, migratorDSN, "up-to", "15")

	// Seeded AT version 15 — the shape the legacy row was written in, with
	// end_date present (0015) and od_date not yet added (0016).
	fixture := seedRangeFixture(t, ctx, pool, "2026-08-10", "2026-08-10")

	var hasODDate bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'od_records' AND column_name = 'od_date'
		)`).Scan(&hasODDate); err != nil {
		t.Fatalf("check od_date after down-to 15: %v", err)
	}
	if hasODDate {
		t.Fatal("[P0] od_records.od_date still exists after down-to 15; 0016's Down did not drop it")
	}

	// The correction: received three days AFTER a one-day event, exactly as a
	// teacher fixing Monday on Thursday morning produces.
	recordID := uuid.NewString()
	mustExec(t, ctx, pool, "seed legacy correction", `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, status, capture_method,
			 actor_user_id, actor_role, server_received_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 5, 'present', 'search',
		        $5::uuid, 'teacher', '2026-08-13 04:30:00+00'::timestamptz)`,
		recordID, fixture.studentID, fixture.eventID, fixture.categoryID, fixture.actorID)

	runGoose(t, migratorDSN, "up")

	var odDate string
	if err := pool.QueryRow(ctx, `
		SELECT od_date::text FROM od_records WHERE id = $1::uuid`, recordID).Scan(&odDate); err != nil {
		t.Fatalf("read backfilled od_date: %v", err)
	}

	// 2026-08-13 IST is the receipt day and is OUTSIDE the event. Clamped to the
	// event's only day.
	if odDate != "2026-08-10" {
		t.Fatalf("[P0] backfilled od_date = %s, want 2026-08-10 (clamped into the event range); "+
			"an unclamped backfill writes the receipt day and creates a row the range trigger would refuse", odDate)
	}

	// And the clamped row must satisfy the guard the same migration installed —
	// the property that makes it legal data rather than merely differently wrong.
	if _, err := pool.Exec(ctx, `
		UPDATE od_records SET od_date = od_date, reason_note = 'legacy correction verified after migration' WHERE id = $1::uuid`, recordID); err != nil {
		t.Fatalf("[P0] the backfilled row does not satisfy its own range guard: %v", err)
	}
}
