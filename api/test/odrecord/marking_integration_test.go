//go:build integration

// Real-Postgres tests for the coordinator marking surface.
//
// Deliberately placed in the EXISTING api/test/odrecord package rather than a
// new one. Stories 0.2, 0.3, 0.4 and 0.5 each shipped a new integration package
// that CI silently never ran, because `INTEGRATION_PKGS` in api/Makefile and the
// integration command in .github/workflows/api-ci.yml were not updated with it.
// Reusing this package removes that failure mode for 0.6 entirely — ./test/
// odrecord/... is already in both lists.
//
// Helpers (testCtx, dsnOrSkip, connect, seedMarkFixture, fixedClock, markFixture)
// come from odrecord_integration_atdd_test.go, which is tagged `integration`, so
// both files compile together under `-tags atdd,integration`.
//
// SCOPE COVERAGE lives in the marking package's own unit tests and in the
// real-stack Playwright spec. What is proven HERE is what only a real database
// can answer: that the counting query separates two coordinators, that it stays
// inside one event+category, that the slot bounds are real, and that a camera
// mark persists a truthful capture_method.
//
// Run: cd api && go test -count=1 -tags integration -p 1 ./test/odrecord/...
package odrecordtest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// 0.6-INT-001a — the two counters are genuinely different questions.
//
// `mine` counts only the calling coordinator's marks; `category_total` counts
// everyone's for the same event+category. One COUNT(*) reused for both passes
// any single-actor test and is wrong the moment a second coordinator works the
// same category — which is the normal case at the event.
func TestMarkStatusCountersSeparateOwnMarksFromTheCategoryTotal(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	coordinatorA := fixture.coordinatorID
	coordinatorB := seedSecondCoordinator(t, ctx, migrator)

	// Real rows through raw SQL: this test is about the COUNTING query, not about
	// the write path (which TestCameraMarkWritesTruthfulRecordAndOneAuditRow owns).
	seedPresentRows(t, ctx, migrator, fixture, coordinatorA, 2)
	seedPresentRows(t, ctx, migrator, fixture, coordinatorB, 3)

	svc := odsvc.NewStatusService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewSQLCCounter(runtime),
		odsvc.NewSQLCCreditLedger(runtime),
		fixedClock{now: fixture.slotOneTime},
	)

	statusA, err := svc.MarkStatus(ctx,
		odsvc.Actor{UserID: coordinatorA, Role: "coordinator"},
		fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus(A) = %v, want nil", err)
	}
	if statusA.Counters.Mine != 2 {
		t.Errorf("[P0] A.Mine = %d, want 2", statusA.Counters.Mine)
	}
	if statusA.Counters.CategoryTotal != 5 {
		t.Errorf("[P0] A.CategoryTotal = %d, want 5 — the combined figure spans all coordinators (FR13)", statusA.Counters.CategoryTotal)
	}

	statusB, err := svc.MarkStatus(ctx,
		odsvc.Actor{UserID: coordinatorB, Role: "coordinator"},
		fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus(B) = %v, want nil", err)
	}
	if statusB.Counters.Mine != 3 {
		t.Errorf("[P0] B.Mine = %d, want 3 — `mine` must be per-coordinator, not a shared total", statusB.Counters.Mine)
	}
	if statusB.Counters.CategoryTotal != statusA.Counters.CategoryTotal {
		t.Errorf("[P0] CategoryTotal differs per caller (%d vs %d) — the combined figure must be identical for everyone",
			statusB.Counters.CategoryTotal, statusA.Counters.CategoryTotal)
	}
}

// 0.6-INT-001b — counters are scoped to ONE event+category. A mark in a sibling
// category on the same event must not inflate this category's total.
func TestMarkStatusCountersAreScopedToTheEventAndCategory(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	seedPresentRows(t, ctx, migrator, fixture, fixture.coordinatorID, 1)

	siblingCategory := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO categories (id, event_id, name)
		VALUES ($1, $2, 'ATDD Sibling Category')`, siblingCategory, fixture.eventID); err != nil {
		t.Fatalf("seed sibling category: %v", err)
	}
	seedPresentRowsInCategory(t, ctx, migrator, fixture, siblingCategory, fixture.coordinatorID, 4)

	svc := odsvc.NewStatusService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewSQLCCounter(runtime),
		odsvc.NewSQLCCreditLedger(runtime),
		fixedClock{now: fixture.slotOneTime},
	)
	status, err := svc.MarkStatus(ctx,
		odsvc.Actor{UserID: fixture.coordinatorID, Role: "coordinator"},
		fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus = %v, want nil", err)
	}
	if status.Counters.CategoryTotal != 1 {
		t.Errorf("[P0] CategoryTotal = %d, want 1 — 4 marks in a sibling category leaked into this one", status.Counters.CategoryTotal)
	}
	if status.Counters.Mine != 1 {
		t.Errorf("[P0] Mine = %d, want 1", status.Counters.Mine)
	}
}

// 0.6-INT-001c — the slot window comes back with real bounds on the event date,
// and is absent off it. This is the FR14 countdown's only source of truth.
func TestMarkStatusReturnsRealSlotBoundsOnTheEventDateAndNoneOffIt(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	open := odsvc.NewStatusService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewSQLCCounter(runtime),
		odsvc.NewSQLCCreditLedger(runtime),
		fixedClock{now: fixture.slotOneTime}, // 08:25 IST — inside slot 1 (08:00-08:50)
	)
	status, err := open.MarkStatus(ctx,
		odsvc.Actor{UserID: fixture.coordinatorID, Role: "coordinator"},
		fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus(open) = %v, want nil", err)
	}
	if status.Slot == nil {
		t.Fatal("[P0] Slot = nil at 08:25 on the event date — slot 1 runs 08:00-08:50")
	}
	if status.Slot.Slot != 1 {
		t.Errorf("[P0] Slot.Slot = %d, want 1", status.Slot.Slot)
	}
	if !status.ServerTime.Before(status.Slot.End) || status.ServerTime.Before(status.Slot.Start) {
		t.Errorf("[P0] ServerTime %v is outside the reported window [%v, %v) — the countdown would start wrong",
			status.ServerTime, status.Slot.Start, status.Slot.End)
	}

	// 10:35 IST is the break between slot 3 (ends 10:30) and slot 4 (starts 10:45).
	closed := odsvc.NewStatusService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewSQLCCounter(runtime),
		odsvc.NewSQLCCreditLedger(runtime),
		fixedClock{now: fixture.breakTime},
	)
	breakStatus, err := closed.MarkStatus(ctx,
		odsvc.Actor{UserID: fixture.coordinatorID, Role: "coordinator"},
		fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus(break) = %v, want nil — a closed slot is a state, not a failure", err)
	}
	if breakStatus.Slot != nil {
		t.Errorf("[P0] Slot = %+v during the 10:30-10:45 break, want nil (`slot: null`, never slot 0)", breakStatus.Slot)
	}
}

// 0.6-INT-001d — the camera path writes a TRUTHFUL row.
//
// Task 3 widens the pairing rule so a camera mark carries a student_id. What
// must not drift is honesty: `capture_method` is persisted as "camera" on the
// od_records row (FR44), because Story 0.8's shared camera+search credit cap and
// every later anomaly report read that column. A camera mark silently recorded
// as "search" would defeat both.
func TestCameraMarkWritesTruthfulRecordAndConservesAudit(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	beforeRecords := countRows(t, ctx, migrator, "od_records")
	beforeAudit := countRows(t, ctx, migrator, "audit_log")

	svc := odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: fixture.slotOneTime},
		odsvc.AllowAnyScope{},
		odsvc.NewSQLCCreditLedger(runtime),
	)

	studentID := fixture.studentID
	result, err := svc.Mark(ctx,
		odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"},
		odsvc.MarkInput{
			EventID:       fixture.eventID,
			CategoryID:    fixture.categoryID,
			CaptureMethod: odsvc.CaptureMethodCamera,
			StudentID:     &studentID,
		})
	if err != nil {
		t.Fatalf("[P0] camera mark with student_id = %v, want nil — Task 3 widens exactly this pairing", err)
	}
	if result.CaptureMethod != odsvc.CaptureMethodCamera {
		t.Errorf("[P0] result capture_method = %q, want camera", result.CaptureMethod)
	}

	if got := countRows(t, ctx, migrator, "od_records") - beforeRecords; got != 1 {
		t.Errorf("[P0] od_records grew by %d, want exactly 1", got)
	}
	if got := countRows(t, ctx, migrator, "audit_log") - beforeAudit; got != 1 {
		t.Errorf("[P0] audit_log grew by %d, want exactly 1 (od.created, same transaction)", got)
	}

	var persisted string
	if err := migrator.QueryRow(ctx,
		`SELECT capture_method FROM od_records WHERE id = $1`, result.RecordID).Scan(&persisted); err != nil {
		t.Fatalf("read back the record: %v", err)
	}
	if persisted != "camera" {
		t.Errorf("[P0] persisted capture_method = %q, want \"camera\" — the record must be truthful (FR44); "+
			"Story 0.8's shared camera+search credit cap reads this column", persisted)
	}
}

// 0.6-INT-001e — prefix search runs against the canonical uppercase roll column,
// returns an empty list rather than an error for no match, and stays bounded.
func TestStudentSearchUsesCanonicalRollAndStaysBounded(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	var rollNo string
	if err := migrator.QueryRow(ctx, `SELECT roll_no FROM students WHERE id = $1`, fixture.studentID).Scan(&rollNo); err != nil {
		t.Fatalf("read seeded roll_no: %v", err)
	}

	svc := studentsvc.NewService(nil, nil, nil)
	svc.WithSearchReader(studentsvc.NewSQLCSearchReader(runtime))
	actor := studentsvc.Actor{UserID: fixture.coordinatorID, Role: "coordinator"}

	// Story 0.2 canonicalised roll_no to uppercase; a coordinator types lowercase
	// on a phone keyboard, so a lowercase prefix must still match.
	lower, err := svc.Search(ctx, actor, studentsvc.SearchInput{Query: strings.ToLower(rollNo[:12])})
	if err != nil {
		t.Fatalf("Search(lowercase) = %v, want nil", err)
	}
	if len(lower) == 0 {
		t.Error("[P0] a lowercase prefix matched nothing — the query must be upper-cased against the canonical roll_no column")
	}

	none, err := svc.Search(ctx, actor, studentsvc.SearchInput{Query: "ZZZNOSUCHROLL"})
	if err != nil {
		t.Fatalf("Search(no match) = %v, want nil — no results is a valid answer, not a failure", err)
	}
	if len(none) != 0 {
		t.Errorf("[P0] got %d results for a non-matching prefix, want 0", len(none))
	}

	many, err := svc.Search(ctx, actor, studentsvc.SearchInput{Query: "CB.SC.U4ODR", Limit: 10_000})
	if err != nil {
		t.Fatalf("Search(huge limit) = %v, want nil", err)
	}
	if len(many) > 50 {
		t.Errorf("[P0] got %d results, want at most the server cap of 50", len(many))
	}
}

// ---------------------------------------------------------------------------
// Scaffold helpers — real bodies, no logic of their own.
// ---------------------------------------------------------------------------

func seedSecondCoordinator(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'coordinator2@amrita.edu', 'ATDD Coordinator Two', 'coordinator')
		ON CONFLICT (supabase_subject) DO UPDATE SET role = EXCLUDED.role`,
		id, "atdd-coordinator2-"+id); err != nil {
		t.Fatalf("seed second coordinator: %v", err)
	}
	return id
}

// seedPresentRows inserts `count` present od_records for one actor, each for a
// distinct student, so the partial-unique duplicate index is never tripped.
func seedPresentRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture markFixture, actorUserID string, count int) {
	t.Helper()
	seedPresentRowsInCategory(t, ctx, pool, fixture, fixture.categoryID, actorUserID, count)
}

func seedPresentRowsInCategory(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	fixture markFixture, categoryID, actorUserID string, count int,
) {
	t.Helper()
	for i := range count {
		studentID := uuid.NewString()
		rollSuffix := strings.ToUpper(strings.ReplaceAll(studentID, "-", ""))[:8]
		if _, err := pool.Exec(ctx, `
			INSERT INTO students (id, roll_no, name, branch_id, section, batch)
			VALUES ($1, $2, 'ATDD Counter Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')`,
			studentID, "CB.SC.U4ODR"+rollSuffix); err != nil {
			t.Fatalf("seed counter student %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO od_records
				(id, student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, server_received_at)
			VALUES ($1, $2, $3, $4, 1, $7::date, 'present', 'nfc', $5, 'coordinator', $6)`,
			uuid.NewString(), studentID, fixture.eventID, categoryID, actorUserID, fixture.slotOneTime,
			fixture.eventDate.Format("2006-01-02")); err != nil {
			t.Fatalf("seed od_record %d: %v", i, err)
		}
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	// `table` is a package-local constant in every call site, never user input.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
