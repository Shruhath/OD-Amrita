//go:build integration

// Real-Postgres tests for Story 0.10's public OD-records read (AC 4, 6, 14).
//
// Deliberately in the EXISTING api/test/odrecord package. ./test/odrecord/... is
// already wired into both api/Makefile's INTEGRATION_PKGS and
// .github/workflows/api-ci.yml, so a new package here would silently never run
// in CI the way three earlier stories' packages did — adding one means editing
// the Makefile AND the workflow in the same change.
//
// Helpers (testCtx, dsnOrSkip, connect, seedMarkFixture, markFixture, countRows)
// come from odrecord_integration_atdd_test.go and marking_integration_test.go,
// tagged `integration` so all files compile together.
//
// COMPILE-RED until api/internal/service/publicattendance exists. The surface
// these tests prescribe:
//
//	func NewSQLCService(*pgxpool.Pool) *Service
//	func (*Service) Records(ctx, studentID string) (RecordCollection, error)
//
//	type RecordCollection struct { Student Student; Days []Day; GeneratedAt time.Time }
//	type Day              struct { EventDate string; Records []Record }   // EventDate is YYYY-MM-DD
//	type Record           struct { RecordID, EventID, EventName, EventDate string
//	                               CategoryID, CategoryName string
//	                               Slot int
//	                               MarkedByName *string   // nullable: actor_user_id is ON DELETE SET NULL
//	                               MarkedByRole string
//	                               MarkedAt time.Time }
//	type Student          struct { StudentID, RollNo, DisplayName, Branch, Section, Batch string }
//
// These run under `make -C api test-integration` locally (make db-up first).
// Story 0.9's retrospective identified "no local Postgres, so CI is the first
// real execution" as the single root cause of six defects across five CI rounds.
// Run them locally before pushing.
package odrecordtest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
)

// 0.10-INT-001 — records are grouped by od_records.od_date, most recent day
// first, and ordered by slot ascending inside each day (AC 4).
//
// The grouping key MOVED under Story 0.12a: it was events.event_date, which was
// correct only while one event meant one day, and would now collapse a thirty-day
// festival onto its first heading. It is still not server_received_at — that
// would file a teacher's next-morning correction under the wrong day, which on
// this screen reads to the student as "I was not marked".
func TestPublicAttendance_GroupsByODDateNewestFirstAndSlotAscending(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	// The fixture event is 2026-08-03. A second, EARLIER event gives grouping
	// something to order; a single day cannot prove "most recent first".
	earlier := seedPublicEvent(t, ctx, migrator, "ATDD Public Earlier Event", "2026-08-01")

	// Deliberately inserted out of order — slot 7 before slot 2 on the later day,
	// and the earlier day's row first of all. An already-sorted fixture cannot
	// prove that anything sorted it.
	seedPublicRecord(t, ctx, migrator, fixture.studentID, earlier.eventID, earlier.categoryID, 5, fixture.coordinatorID, "coordinator", "2026-08-01")
	seedPublicRecord(t, ctx, migrator, fixture.studentID, fixture.eventID, fixture.categoryID, 7, fixture.coordinatorID, "coordinator", "2026-08-03")
	seedPublicRecord(t, ctx, migrator, fixture.studentID, fixture.eventID, fixture.categoryID, 2, fixture.coordinatorID, "coordinator", "2026-08-03")

	collection, err := publicattendance.NewSQLCService(runtime).Records(ctx, fixture.studentID)
	if err != nil {
		t.Fatalf("[P0] Records: %v", err)
	}

	if len(collection.Days) != 2 {
		t.Fatalf("[P0] got %d days, want 2 (2026-08-03 and 2026-08-01)", len(collection.Days))
	}
	if got := collection.Days[0].ODDate; got != "2026-08-03" {
		t.Errorf("[P0] first day = %q, want 2026-08-03 — most recent day first, or a student opening the "+
			"page at 10pm scrolls past last month to find today", got)
	}
	if got := collection.Days[1].ODDate; got != "2026-08-01" {
		t.Errorf("[P0] second day = %q, want 2026-08-01", got)
	}

	slots := []int{}
	for _, record := range collection.Days[0].Records {
		slots = append(slots, record.Slot)
	}
	if len(slots) != 2 || slots[0] != 2 || slots[1] != 7 {
		t.Errorf("[P0] day 2026-08-03 slots = %v, want [2 7] ascending. Order comes from SQL and must be "+
			"preserved through the Go grouping — do not re-sort in the handler and never in the browser", slots)
	}

	record := collection.Days[0].Records[0]
	if record.EventName == "" || record.CategoryName == "" || record.EventDate != "2026-08-03" {
		t.Errorf("[P0] record is missing display context: event=%q category=%q date=%q — a slot with no "+
			"event name tells the student nothing about what they attended",
			record.EventName, record.CategoryName, record.EventDate)
	}
	if record.MarkedAt.IsZero() {
		t.Errorf("[P0] record.MarkedAt is zero; server_received_at is what makes the row auditable to the student")
	}
	if collection.GeneratedAt.IsZero() {
		t.Errorf("[P1] GeneratedAt is zero; the UI stamps it as \"as of HH:MM\" so a stale tab is not mistaken " +
			"for live truth")
	}
}

// 0.10-INT-002 — a record on an ARCHIVED event is still readable (T6, AC 6).
//
// Archiving is an administrative act on the event, not a retraction of the
// student's attendance. Filtering on events.status = 'active' would erase a
// student's whole record the moment the coordinator tidied up after the event —
// exactly when they are most likely to check it.
func TestPublicAttendance_ArchivedEventRecordsAreStillReturned(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	archived := seedPublicEvent(t, ctx, migrator, "ATDD Public Archived Event", "2026-07-28")
	// Insert the record FIRST, then archive. `events` carries triggers that block
	// child mutations on an archived event, so seeding in the other order fails —
	// a trap worth stating out loud because the error surfaces as a confusing
	// permission-shaped failure on the INSERT rather than on the archive.
	seedPublicRecord(t, ctx, migrator, fixture.studentID, archived.eventID, archived.categoryID, 3, fixture.coordinatorID, "coordinator", "2026-07-28")
	// archived_by_user_id is NOT optional here: events_archive_state_ck requires
	// status/archived_at/archived_by_user_id to move together, so an UPDATE that
	// sets only the first two is rejected by the constraint rather than by
	// anything to do with this story.
	if _, err := migrator.Exec(ctx, `
		UPDATE events SET status = 'archived', archived_at = now(), archived_by_user_id = $2
		WHERE id = $1`, archived.eventID, fixture.adminID); err != nil {
		t.Fatalf("archive event: %v", err)
	}

	collection, err := publicattendance.NewSQLCService(runtime).Records(ctx, fixture.studentID)
	if err != nil {
		t.Fatalf("[P0] Records: %v", err)
	}

	found := false
	for _, day := range collection.Days {
		if day.ODDate == "2026-07-28" {
			found = true
		}
	}
	if !found {
		t.Errorf("[P0] the archived event's record is missing. The records query filters on status = 'present' "+
			"and the student id ONLY — do not filter events.status (T6). Days returned: %d", len(collection.Days))
	}
}

// 0.10-INT-003 — the marker's NAME is shown, and a deleted staff row leaves the
// record readable with a null name (AC 4).
//
// actor_user_id is ON DELETE SET NULL. A join that drops the row when the actor
// is gone would silently delete a student's attendance record as a side effect of
// off-boarding a teacher.
func TestPublicAttendance_MarkerNameIsShownAndSurvivesActorDeletion(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	// A throwaway actor with no other references, so the DELETE below is clean.
	doomedID := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'atdd-doomed@amrita.edu', 'Doomed Marker', 'teacher')`,
		doomedID, "atdd-doomed-"+doomedID); err != nil {
		t.Fatalf("seed doomed actor: %v", err)
	}
	seedPublicRecord(t, ctx, migrator, fixture.studentID, fixture.eventID, fixture.categoryID, 4, doomedID, "teacher", "2026-08-03")

	svc := publicattendance.NewSQLCService(runtime)

	before, err := svc.Records(ctx, fixture.studentID)
	if err != nil {
		t.Fatalf("[P0] Records: %v", err)
	}
	record := findRecordBySlot(t, before, 4)
	if record.MarkedByName == nil || *record.MarkedByName != "Doomed Marker" {
		t.Errorf("[P0] marked_by_name = %v, want \"Doomed Marker\"", record.MarkedByName)
	}
	if record.MarkedByRole != "teacher" {
		t.Errorf("[P0] marked_by_role = %q, want teacher", record.MarkedByRole)
	}

	if _, err := migrator.Exec(ctx, `DELETE FROM users WHERE id = $1`, doomedID); err != nil {
		t.Fatalf("delete actor: %v", err)
	}

	after, err := svc.Records(ctx, fixture.studentID)
	if err != nil {
		t.Fatalf("[P0] Records after actor deletion: %v", err)
	}
	orphan := findRecordBySlot(t, after, 4)
	if orphan.MarkedByName != nil {
		t.Errorf("[P0] marked_by_name = %v after the actor row was deleted, want nil", orphan.MarkedByName)
	}
	if orphan.MarkedByRole != "teacher" {
		t.Errorf("[P0] marked_by_role = %q after actor deletion, want teacher — actor_role is stored ON THE "+
			"RECORD precisely so it survives the user row", orphan.MarkedByRole)
	}
}

// 0.10-INT-004 — a real student with no marks is a 200 with an empty day list,
// and an unknown student is a typed STUDENT_NOT_FOUND (AC 6).
//
// These must be DIFFERENT answers. Collapsing "no such student" into an empty
// list tells a student who mistyped their roll number that they were not marked,
// which is the single most damaging wrong answer this page can give.
func TestPublicAttendance_EmptyRecordsAndUnknownStudentAreDifferentAnswers(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	svc := publicattendance.NewSQLCService(runtime)

	// A real student, never marked.
	collection, err := svc.Records(ctx, fixture.studentID)
	if err != nil {
		t.Fatalf("[P0] a real student with no marks must be a successful empty read, got: %v", err)
	}
	if len(collection.Days) != 0 {
		t.Errorf("[P0] got %d days for an unmarked student, want 0", len(collection.Days))
	}
	if collection.Student.RollNo == "" {
		t.Errorf("[P0] the student's display fields are empty on a no-records read; the page still has to " +
			"name whose record it is showing")
	}

	// A well-formed uuid that matches nobody.
	_, err = svc.Records(ctx, uuid.NewString())
	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("[P0] unknown student returned %v, want a typed domain error", err)
	}
	if domainErr.Code != contracts.ErrorCodeSTUDENTNOTFOUND {
		t.Errorf("[P0] unknown student code = %q, want STUDENT_NOT_FOUND (already mapped to 404 in "+
			"domain/errors.go — reuse it, do not add a code)", domainErr.Code)
	}
}

// 0.10-INT-005 — a public read writes NOTHING (AC 14, T10).
//
// This is the invariant that makes an unauthenticated endpoint safe to expose.
// Both tables are checked: an audit row would mean the read went through
// store.WithAudit (it must not), and an od_records row would mean a read path
// acquired a write.
func TestPublicAttendance_ReadsWriteNoAuditAndNoRecords(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	seedPublicRecord(t, ctx, migrator, fixture.studentID, fixture.eventID, fixture.categoryID, 6, fixture.coordinatorID, "coordinator", "2026-08-03")

	beforeAudit := countRows(t, ctx, migrator, "audit_log")
	beforeRecords := countRows(t, ctx, migrator, "od_records")

	svc := publicattendance.NewSQLCService(runtime)
	for i := 0; i < 3; i++ {
		if _, err := svc.Records(ctx, fixture.studentID); err != nil {
			t.Fatalf("[P0] Records: %v", err)
		}
	}
	// The not-found path too: an error branch is where a stray "log this" write
	// is most likely to be added later.
	_, _ = svc.Records(ctx, uuid.NewString())

	if got := countRows(t, ctx, migrator, "audit_log"); got != beforeAudit {
		t.Errorf("[P0] audit_log grew from %d to %d across public reads. A public read has no actor to "+
			"attribute and no state change to record; an audit row here is an unauthenticated write path",
			beforeAudit, got)
	}
	if got := countRows(t, ctx, migrator, "od_records"); got != beforeRecords {
		t.Errorf("[P0] od_records grew from %d to %d across public reads", beforeRecords, got)
	}
}

// ---------------------------------------------------------------------------
// Fixtures local to the public-attendance tests.
// ---------------------------------------------------------------------------

type publicEventFixture struct {
	eventID    string
	categoryID string
}

// seedPublicEvent creates an active event on a specific date plus one category.
// eventDate is a YYYY-MM-DD string so the expected grouping key is written the
// same way it is asserted.
func seedPublicEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, eventDate string) publicEventFixture {
	t.Helper()
	fixture := publicEventFixture{eventID: uuid.NewString(), categoryID: uuid.NewString()}
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (id, name, event_date, end_date, status)
		VALUES ($1, $2, $3::date, $3::date, 'active')`, fixture.eventID, name, eventDate); err != nil {
		t.Fatalf("seed public event %q: %v", name, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO categories (id, event_id, name)
		VALUES ($1, $2, 'ATDD Public Category')`, fixture.categoryID, fixture.eventID); err != nil {
		t.Fatalf("seed public category: %v", err)
	}
	return fixture
}

// seedPublicRecord inserts one present od_records row for an EXISTING student.
//
// Distinct from seedAttendanceRecord (teacher_admin_attendance_integration_test.go),
// which creates a new student per call: these tests need MANY records for ONE
// student, which is the whole shape of a per-student public read.
// odDate is passed EXPLICITLY rather than derived from the event or from now():
// the grouping key under test is od_records.od_date, and a fixture that let the
// database or the clock choose it would make every day assertion below agree
// with whatever the code did.
func seedPublicRecord(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	studentID, eventID, categoryID string, slot int, actorUserID, actorRole, odDate string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, reason_note, server_received_at)
		VALUES ($1, $2, $3, $4, $5, $8::date, 'present', 'search', $6, $7, 'public attendance integration seed', now())`,
		uuid.NewString(), studentID, eventID, categoryID, slot, actorUserID, actorRole, odDate); err != nil {
		t.Fatalf("seed public od record (slot %d): %v", slot, err)
	}
}

// findRecordBySlot locates one record across every day, so a test can name the
// row it seeded without depending on how many days came back.
func findRecordBySlot(t *testing.T, collection publicattendance.RecordCollection, slot int) publicattendance.Record {
	t.Helper()
	for _, day := range collection.Days {
		for _, record := range day.Records {
			if record.Slot == slot {
				return record
			}
		}
	}
	t.Fatalf("[P0] no record in slot %d across %d days", slot, len(collection.Days))
	return publicattendance.Record{}
}
