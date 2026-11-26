//go:build integration

// Real-Postgres tests for the multi-day event date range.
//
// Deliberately in the EXISTING api/test/odrecord package. ./test/odrecord/... is
// already wired into both api/Makefile's INTEGRATION_PKGS and
// .github/workflows/api-ci.yml, so a new package here would silently never run in
// CI the way several earlier packages did.
//
// WHAT ONLY A REAL DATABASE CAN ANSWER, and therefore what lives here:
//
//   - that the re-keyed partial unique index actually permits the same student in
//     the same slot on two different days, and still refuses the second mark on
//     one day — as the ERROR CODE, not as "the insert failed";
//   - that the counters, the credit pool and the public grouping are keyed on the
//     day rather than on the event;
//   - that a record's day and its RECEIPT TIME are different facts.
//
// The range-guard trigger and the schema shapes live in api/test/db's
// multi_day_schema_test.go, against the migrator and runtime roles directly.
// Split that way so neither file duplicates the other's coverage.
//
// COMPILE-RED until `ActiveSlot` takes an end bound, `CampusDate` exists and
// `MarkResult`/`RecordInput` carry `ODDate`.
//
// Run: cd api && make db-up && make test-integration
package odrecordtest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// 0.12a-INT-001 — the same student, in the same slot, on two different days of
// one event: BOTH marks succeed.
//
// This is the feature. Under today's key, `(student_id, event_id, slot)`, the
// second day's mark is refused as a duplicate — a coordinator standing in front
// of a student on day two of a thirty-day festival is told they have already been
// marked, and there is no way to correct it.
//
// Both marks go through the real service and the real writer, not raw SQL,
// because the point is that the WRITE PATH carries the day — a test that inserted
// its own od_date would pass even if the service never derived one.
func TestMultiDay_SameStudentAndSlotOnTwoDaysBothSucceed(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	widenFixtureEvent(t, ctx, migrator, fixture, "2026-08-06")

	studentID := seedMarkStudent(t, ctx, migrator, fixture)
	dayOne := fixture.slotOneTime                         // 2026-08-03 08:25 IST
	dayTwo := fixture.slotOneTime.AddDate(0, 0, 1)        // 2026-08-04 08:25 IST
	dayFinal := campusDay(t, 2026, time.August, 6, 8, 25) // the last day of the range

	first, err := markServiceAt(runtime, fixture, dayOne).Mark(ctx, fixtureCoordinator(fixture), searchMarkInput(fixture, studentID))
	if err != nil {
		t.Fatalf("[P0] day one mark: %v", err)
	}
	if got := first.ODDate.Format("2006-01-02"); got != "2026-08-03" {
		t.Errorf("[P0] day one od_date = %q, want 2026-08-03 (CampusDate of the mark, not the event's start)", got)
	}

	second, err := markServiceAt(runtime, fixture, dayTwo).Mark(ctx, fixtureCoordinator(fixture), searchMarkInput(fixture, studentID))
	if err != nil {
		t.Fatalf("[P0] the SECOND day's mark for the same student and slot was refused: %v.\n"+
			"The partial unique index must be keyed (student_id, event_id, od_date, slot). Keyed without od_date "+
			"it refuses a genuine second-day mark, and the coordinator standing in front of the student is told "+
			"they are already marked with no way to correct it", err)
	}
	if got := second.ODDate.Format("2006-01-02"); got != "2026-08-04" {
		t.Errorf("[P0] day two od_date = %q, want 2026-08-04", got)
	}
	if first.RecordID == second.RecordID {
		t.Errorf("[P0] both days returned the same record id %q", first.RecordID)
	}

	// The final day of the range must work too. Range containment is inclusive,
	// and a coordinator on the closing day of a festival is doing exactly the
	// thing the date range exists for.
	if _, err := markServiceAt(runtime, fixture, dayFinal).Mark(ctx, fixtureCoordinator(fixture), searchMarkInput(fixture, studentID)); err != nil {
		t.Errorf("[P0] the LAST day of the range was refused: %v. Containment is inclusive at both ends", err)
	}
}

// 0.12a-INT-002 — a second mark on the SAME day still returns SCAN_DUPLICATE, and
// this asserts the ERROR CODE rather than that the insert failed.
//
// T8 renames the unique index to `uq_od_records_present_student_event_date_slot`,
// and `mapPgError` in api/internal/service/od_record/writer.go matches the OLD
// name as a STRING LITERAL. Miss the rename there and the duplicate path stops
// returning SCAN_DUPLICATE (409) and starts returning an unmapped 500 — while a
// test that only checks `err != nil` stays green, because the row still fails to
// insert.
//
// That is the exact defect shape this codebase has been bitten by three times.
// The assertion is the code.
func TestMultiDay_SecondMarkOnTheSameDayIsStillScanDuplicate(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	widenFixtureEvent(t, ctx, migrator, fixture, "2026-08-06")

	studentID := seedMarkStudent(t, ctx, migrator, fixture)
	svc := markServiceAt(runtime, fixture, fixture.slotOneTime)

	if _, err := svc.Mark(ctx, fixtureCoordinator(fixture), searchMarkInput(fixture, studentID)); err != nil {
		t.Fatalf("[P0] first mark: %v", err)
	}

	_, err := svc.Mark(ctx, fixtureCoordinator(fixture), searchMarkInput(fixture, studentID))
	if err == nil {
		t.Fatalf("[P0] a duplicate mark on the SAME (student, event, od_date, slot) was ACCEPTED. Adding od_date " +
			"to the key must NARROW the guard, not remove it")
	}
	if !isDomainCode(err, contracts.ErrorCodeSCANDUPLICATE) {
		t.Fatalf("[P0] duplicate error = %v, want SCAN_DUPLICATE.\n"+
			"The index was renamed to uq_od_records_present_student_event_date_slot; mapPgError in "+
			"api/internal/service/od_record/writer.go matches the OLD name as a string literal. Left unchanged, "+
			"a duplicate becomes an unmapped 500 and the coordinator sees a server error instead of the red "+
			"'already marked' state they know — with every existing duplicate test still green, because the row "+
			"still fails to insert", err)
	}
}

// 0.12a-INT-003 — the two FR13 counters count TODAY only.
//
// Without `AND od_date = @od_date`, both figures a coordinator reads as "today's
// progress" are running thirty-day cumulative totals. That is not a cosmetic
// defect: the counters are how a coordinator knows whether the queue in front of
// them has been processed.
func TestMultiDay_CountersCountTodayOnly(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	widenFixtureEvent(t, ctx, migrator, fixture, "2026-08-06")

	// Three marks yesterday, two today, all by the fixture coordinator.
	seedPresentRowsOn(t, ctx, migrator, fixture, fixture.coordinatorID, "2026-08-03", 3)
	seedPresentRowsOn(t, ctx, migrator, fixture, fixture.coordinatorID, "2026-08-04", 2)

	svc := odsvc.NewStatusService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewSQLCCounter(runtime),
		odsvc.NewSQLCCreditLedger(runtime),
		fixedClock{now: campusDay(t, 2026, time.August, 4, 8, 25)},
	)

	status, err := svc.MarkStatus(ctx,
		odsvc.Actor{UserID: fixture.coordinatorID, Role: "coordinator"},
		fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus: %v", err)
	}

	if status.Counters.Mine != 2 {
		t.Errorf("[P0] Mine = %d, want 2 — TODAY's marks only. Got 5? The counter is summing the whole "+
			"thirty-day range, so both numbers on a screen the coordinator reads as 'today' are cumulative "+
			"totals (T15)", status.Counters.Mine)
	}
	if status.Counters.CategoryTotal != 2 {
		t.Errorf("[P0] CategoryTotal = %d, want 2 — today's marks across all coordinators", status.Counters.CategoryTotal)
	}
	if status.Slot == nil {
		t.Errorf("[P0] no slot open on day two of the range at 08:25. ActiveSlot must test range CONTAINMENT, " +
			"not equality with the event's start date")
	}
}

// 0.12a-INT-004 — the fallback-credit pool is per DAY while the cap stays per
// EVENT.
//
// T12's asymmetry, proven from both sides in one test:
//
//   - exhausting the pool on day one must NOT block day two, or a thirty-day
//     festival gets one day's worth of camera fallback in total;
//   - the cap is read from the UNCHANGED per-event config, so the single admin
//     PUT that raises it applies to every remaining day. That live cap raise is
//     the documented fallback when NFC fails at the venue, and making the cap
//     per-day would turn it into thirty PUTs during the incident.
func TestMultiDay_CreditPoolIsPerDayWhileTheCapStaysPerEvent(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	widenFixtureEvent(t, ctx, migrator, fixture, "2026-08-06")
	setCaptureCreditLimit(t, ctx, migrator, fixture, 1)

	dayOne := fixture.slotOneTime
	dayTwo := fixture.slotOneTime.AddDate(0, 0, 1)

	// Day one: the single credit is spent, then the pool is exhausted.
	if _, err := markServiceAt(runtime, fixture, dayOne).Mark(ctx, fixtureCoordinator(fixture),
		searchMarkInput(fixture, seedMarkStudent(t, ctx, migrator, fixture))); err != nil {
		t.Fatalf("[P0] day one's only credit: %v", err)
	}
	_, err := markServiceAt(runtime, fixture, dayOne).Mark(ctx, fixtureCoordinator(fixture),
		searchMarkInput(fixture, seedMarkStudent(t, ctx, migrator, fixture)))
	if !isDomainCode(err, contracts.ErrorCodeCAPTURECREDITLIMITREACHED) {
		t.Fatalf("[P0] day one's second fallback mark = %v, want CAPTURE_CREDIT_LIMIT_REACHED. The per-day pool "+
			"must still exhaust WITHIN a day, or the cap enforces nothing", err)
	}

	// Day two: a fresh pool, from the same unchanged per-event cap of 1.
	if _, err := markServiceAt(runtime, fixture, dayTwo).Mark(ctx, fixtureCoordinator(fixture),
		searchMarkInput(fixture, seedMarkStudent(t, ctx, migrator, fixture))); err != nil {
		t.Fatalf("[P0] day two's first fallback mark was refused: %v.\n"+
			"Usage is keyed (event_id, user_id, usage_date) — day one exhausting the pool must not consume day "+
			"two's. Keyed per event, a thirty-day festival gets one day's worth of camera fallback in total", err)
	}

	// The two days are separate ROWS, not one running counter.
	if got := creditConsumedOn(t, ctx, runtime, fixture, "2026-08-03"); got != 1 {
		t.Errorf("[P0] day one consumed = %d, want 1", got)
	}
	if got := creditConsumedOn(t, ctx, runtime, fixture, "2026-08-04"); got != 1 {
		t.Errorf("[P0] day two consumed = %d, want 1 — a fresh pool, not a continuation of day one's", got)
	}

	// And the CAP is still one row for the coordinator assignment. A per-day cap
	// would mean the live raise has to be repeated for every remaining day.
	var assignmentRows int
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM event_coordinator_assignments WHERE event_id = $1 AND user_id = $2`,
		fixture.eventID, fixture.coordinatorID).Scan(&assignmentRows); err != nil {
		t.Fatalf("count coordinator assignment rows: %v", err)
	}
	if assignmentRows != 1 {
		t.Errorf("[P0] coordinator assignment rows = %d, want 1. The CAP is per coordinator assignment, not per day", assignmentRows)
	}
}

// 0.12a-INT-005 — the public dashboard groups by the day the OD BELONGS TO, not
// by the day the row was WRITTEN.
//
// The sharpest assertion of the date range, and the one that fails if anyone derives
// the date at read time from `server_received_at`. Both rows below are WRITTEN on
// the 4th; one of them BELONGS to the 3rd. `ListPublicStudentODRecords` already
// carries a comment explaining exactly this hazard: a teacher's next-morning
// correction filed under the wrong day reads to the student as "I was not
// marked", which is the one answer this page must never give by accident.
func TestMultiDay_PublicDashboardGroupsByTheDayTheODBelongsTo(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	widenFixtureEvent(t, ctx, migrator, fixture, "2026-08-06")

	writtenOnDayTwo := campusDay(t, 2026, time.August, 4, 9, 30)

	// The correction: it BELONGS to the 3rd, and it was WRITTEN on the 4th.
	insertRecordOn(t, ctx, migrator, fixture, fixture.studentID, fixture.coordinatorID, 3, "2026-08-03", writtenOnDayTwo)
	// A normal day-two mark, written the same morning.
	insertRecordOn(t, ctx, migrator, fixture, fixture.studentID, fixture.coordinatorID, 3, "2026-08-04", writtenOnDayTwo)

	collection, err := publicattendance.NewSQLCService(runtime).Records(ctx, fixture.studentID)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	if len(collection.Days) != 2 {
		t.Fatalf("[P0] day groups = %d, want 2. Both rows were WRITTEN on 2026-08-04 but one BELONGS to "+
			"2026-08-03. Grouping on server_received_at collapses them onto one day and the student is told "+
			"they were not marked on a day they were; grouping on events.event_date collapses a thirty-day "+
			"festival onto its first day (T18). Days: %+v", len(collection.Days), collection.Days)
	}
	// Newest first, as 0.10 established.
	if got := collection.Days[0].ODDate; got != "2026-08-04" {
		t.Errorf("[P0] first day group = %q, want 2026-08-04 (newest first)", got)
	}
	if got := collection.Days[1].ODDate; got != "2026-08-03" {
		t.Errorf("[P0] second day group = %q, want 2026-08-03 — the day the correction BELONGS to, which is "+
			"NOT the day it was written", got)
	}
	// The record's own event_date must still be the event's FIRST day:
	// eventsFromRecords() in PublicAttendanceScreen.tsx builds the dispute form's
	// event options from it, and a record reporting its own day there would offer
	// the student an event dated to one arbitrary record.
	for _, day := range collection.Days {
		for _, record := range day.Records {
			if record.EventDate != "2026-08-03" {
				t.Errorf("[P0] record.EventDate = %q, want the EVENT's first day 2026-08-03. `event_date` and "+
					"`od_date` are different facts on the same row and both are needed", record.EventDate)
			}
			if record.ODDate != day.ODDate {
				t.Errorf("[P0] record.ODDate = %q inside day group %q", record.ODDate, day.ODDate)
			}
		}
	}
}

// 0.12a-INT-006 — the admin attendance read can be narrowed to ONE day.
//
// T22. A thirty-day event's attendance list otherwise interleaves thirty days of
// the same slot with nothing on screen distinguishing them, which defeats the
// purpose of a verification surface. The unfiltered read must still return
// everything — a filter that always applies is not optional.
func TestMultiDay_AdminAttendanceFiltersToOneDay(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	widenFixtureEvent(t, ctx, migrator, fixture, "2026-08-06")
	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)

	seedPresentRowsOn(t, ctx, migrator, fixture, teacherID, "2026-08-03", 2)
	seedPresentRowsOn(t, ctx, migrator, fixture, teacherID, "2026-08-04", 3)

	svc := marking.NewPortalMarkReadService(runtime)
	dayTwo := "2026-08-04"

	filtered, err := svc.Attendance(ctx, marking.Actor{Role: "admin"}, fixture.eventID, nil, nil, &dayTwo, 1, 20)
	if err != nil {
		t.Fatalf("[P0] Attendance(event, od_date) = %v, want nil", err)
	}
	if filtered.Total != 3 {
		t.Errorf("[P0] od_date-filtered Total = %d, want 3 — day one's two rows must be excluded", filtered.Total)
	}
	for _, record := range filtered.Data {
		if record.ODDate != dayTwo {
			t.Errorf("[P0] a record dated %q survived the %q filter", record.ODDate, dayTwo)
		}
	}

	all, err := svc.Attendance(ctx, marking.Actor{Role: "admin"}, fixture.eventID, nil, nil, nil, 1, 20)
	if err != nil {
		t.Fatalf("[P0] Attendance(event, no filters) = %v, want nil", err)
	}
	if all.Total != 5 {
		t.Errorf("[P0] unfiltered Total = %d, want 5. The day filter is OPTIONAL — a nil od_date must return "+
			"every day, or every existing caller silently starts seeing one day's rows", all.Total)
	}
}

// 0.12a-INT-007 — a teacher/admin correction the morning AFTER the event still
// lands inside the range.
//
// T14. Teacher/admin search-marks are the correction path: exempt from slot
// windows, and legitimately performed the next morning. An UNCLAMPED campus date
// on the day after a one-day event is outside the range, and T10's database
// trigger would then refuse a correction the teacher is entitled to make — a
// feature that worked before the date range, broken by it.
//
// For a single-day event the clamp reproduces today's observable behaviour
// exactly: the record files under the event's own date.
func TestMultiDay_TeacherCorrectionAfterTheEventClampsIntoTheRange(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator) // a SINGLE-day event on 2026-08-03
	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)
	studentID := seedMarkStudent(t, ctx, migrator, fixture)

	// The morning after — three days after, in fact, which is well outside a
	// one-day event and is a perfectly ordinary time to notice a missed mark.
	theMorningAfter := campusDay(t, 2026, time.August, 6, 9, 0)
	slot := 3
	reason := "manual attendance correction after event"

	svc := roleAwareMarkServiceAt(runtime, fixture, theMorningAfter)

	result, err := svc.Mark(ctx, odsvc.Actor{UserID: teacherID, Email: "atdd-teacher@amrita.edu", Role: "teacher"},
		odsvc.MarkInput{
			EventID:       fixture.eventID,
			CategoryID:    fixture.categoryID,
			CaptureMethod: odsvc.CaptureMethodSearch,
			StudentID:     &studentID,
			Slot:          &slot,
			ReasonNote:    &reason,
		})
	if err != nil {
		t.Fatalf("[P0] a teacher correction three days after a one-day event was refused: %v.\n"+
			"An UNCLAMPED CampusDate lands outside the event range and T10's trigger refuses it — breaking a "+
			"correction path that works today. Clamp: before start -> event_date, after end -> end_date, else "+
			"today (T14)", err)
	}
	if got := result.ODDate.Format("2006-01-02"); got != "2026-08-03" {
		t.Errorf("[P0] the correction filed under %q, want the event's own day 2026-08-03. For a single-day "+
			"event the clamp must reproduce today's observable behaviour exactly — which is what the public "+
			"dashboard already shows", got)
	}
}

// ---------------------------------------------------------------------------
// Fixtures and helpers — real bodies, no logic of their own.
// ---------------------------------------------------------------------------

// widenFixtureEvent turns the single-day fixture event into a real range.
//
// Widening the EXISTING event rather than seeding a second one is deliberate: two
// events would let every assertion in this file pass even with `od_date` absent
// from the unique key, which would make the whole suite prove nothing.
func widenFixtureEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture markFixture, endDate string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE events SET end_date = $2::date WHERE id = $1`, fixture.eventID, endDate); err != nil {
		t.Fatalf("widen fixture event to %s: %v", endDate, err)
	}
}

func campusDay(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load campus location: %v", err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

func fixtureCoordinator(fixture markFixture) odsvc.Actor {
	return odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"}
}

// searchMarkInput is a coordinator SEARCH mark — the capture method that consumes
// a fallback credit, so one input shape serves both the uniqueness tests and the
// per-day credit test.
func searchMarkInput(fixture markFixture, studentID string) odsvc.MarkInput {
	return odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodSearch,
		StudentID:     &studentID,
	}
}

// markServiceAt is markService (capture_credit_integration_test.go) with the
// clock as a parameter. Every multi-day assertion turns on WHICH day the service
// thinks it is, so the clock cannot stay pinned to the fixture's slotOneTime.
func markServiceAt(runtime *pgxpool.Pool, fixture markFixture, now time.Time) *odsvc.Service {
	return odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: now},
		odsvc.AllowAnyScope{},
		odsvc.NewSQLCCreditLedger(runtime),
	)
}

// roleAwareMarkServiceAt is roleAwareMarkService
// (teacher_admin_attendance_integration_test.go) with the clock as a parameter,
// so the teacher/admin clamp can be exercised on a day of the test's choosing.
func roleAwareMarkServiceAt(runtime *pgxpool.Pool, fixture markFixture, now time.Time) *odsvc.Service {
	policy := marking.NewPolicy(marking.NewEventReader(svcevent.NewSQLCReader(runtime)))
	return odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: now},
		marking.NewRoleAwareMarkGuard(policy, runtime),
		odsvc.NewSQLCCreditLedger(runtime),
	)
}

// seedPresentRowsOn is seedPresentRows with an explicit day. The day is EXPLICIT
// rather than derived from `server_received_at` on purpose: a fixture that
// derived it would make every day-grouping assertion in this file vacuous (T24).
func seedPresentRowsOn(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	fixture markFixture, actorUserID, odDate string, count int,
) {
	t.Helper()
	for range count {
		insertRecordOn(t, ctx, pool, fixture, seedMarkStudent(t, ctx, pool, fixture),
			actorUserID, 1, odDate, fixture.slotOneTime)
	}
}

func insertRecordOn(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	fixture markFixture, studentID, actorUserID string, slot int, odDate string, receivedAt time.Time,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method,
			 actor_user_id, actor_role, server_received_at)
		VALUES ($1, $2, $3, $4, $5, $6::date, 'present', 'nfc', $7, 'coordinator', $8)`,
		uuid.NewString(), studentID, fixture.eventID, fixture.categoryID, slot, odDate,
		actorUserID, receivedAt); err != nil {
		t.Fatalf("seed od_record on %s: %v", odDate, err)
	}
}

func creditConsumedOn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture markFixture, usageDate string) int {
	t.Helper()
	var consumed int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(consumed, 0) FROM event_capture_credit_usage
		WHERE event_id = $1 AND user_id = $2 AND usage_date = $3::date`,
		fixture.eventID, fixture.coordinatorID, usageDate).Scan(&consumed); err != nil {
		return 0
	}
	return consumed
}
