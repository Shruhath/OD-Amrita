//go:build integration

// Real-Postgres tests for teacher/admin record management:
// Correct, Remove, and Audit. Deliberately in the EXISTING api/test/odrecord
// package for the same reason teacher_admin_attendance_integration_test.go
// gives: ./test/odrecord/... is already wired into api/Makefile's
// INTEGRATION_PKGS and .github/workflows/api-ci.yml.
//
// WHAT ONLY A REAL DATABASE CAN ANSWER, and therefore what lives here:
//   - that a correction's previous/new snapshot is queryable back out of the
//     REAL audit_log table, not just asserted against an in-memory fake;
//   - that the re-keyed partial unique index actually refuses a correction
//     that collides with another present row, as SCAN_DUPLICATE;
//   - that a removed row's slot is genuinely free for a fresh mark, via the
//     same unique index the multi-day duplicate rule relies on;
//   - that RBAC now runs before the terminal-status check (the review's
//     T8 overturn): a teacher outside the record's branch gets a uniform
//     RBAC_FORBIDDEN whether the record is present, removed, or nonexistent.
//
// Helpers (testCtx, dsnOrSkip, connect, seedMarkFixture, markFixture,
// seedTeacherAndBranch, countRows) come from odrecord_integration_atdd_test.go
// and teacher_admin_attendance_integration_test.go, tagged `integration` so
// all files in this package compile together.
package odrecordtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// 0.12b-INT-001 — a correction changes the fields requested, leaves the
// record's id stable, and is queryable afterward as an od.overridden audit
// entry carrying both the previous and the new values (FR45).
func TestManagementCorrect_ChangesFieldsAndAuditsPreviousAndNew(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	adminID := fixture.adminID
	recordID := seedManagementRecord(t, ctx, migrator, fixture, 3, fixture.categoryID, fixture.eventDate, "search")

	svc := odsvc.NewManagementService(runtime, store.New(runtime))
	newCategoryID := uuid.NewString()
	if _, err := migrator.Exec(ctx, `INSERT INTO categories (id, event_id, name) VALUES ($1, $2, 'ATDD Correction Target Category')`, newCategoryID, fixture.eventID); err != nil {
		t.Fatalf("seed target category: %v", err)
	}

	result, err := svc.Correct(ctx, odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}, odsvc.CorrectionInput{
		RecordID:   recordID,
		ReasonNote: "Wrong category selected at capture time.",
		CategoryID: &newCategoryID,
	})
	if err != nil {
		t.Fatalf("[P0] Correct = %v, want nil", err)
	}
	if result.RecordID != recordID || result.Status != "present" {
		t.Fatalf("[P0] Correct result = %+v, want stable id %s and status present", result, recordID)
	}

	var storedCategory, storedReason string
	if err := migrator.QueryRow(ctx, `SELECT category_id::text, reason_note FROM od_records WHERE id = $1`, recordID).Scan(&storedCategory, &storedReason); err != nil {
		t.Fatalf("read corrected record: %v", err)
	}
	if storedCategory != newCategoryID || storedReason != "Wrong category selected at capture time." {
		t.Fatalf("[P0] persisted category/reason = %s/%q, want %s/%q", storedCategory, storedReason, newCategoryID, "Wrong category selected at capture time.")
	}

	entries, err := svc.Audit(ctx, odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}, recordID)
	if err != nil {
		t.Fatalf("[P0] Audit = %v, want nil", err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Action != string(domain.ActionODOverridden) {
			continue
		}
		found = true
		previous, ok := entry.Payload["previous"].(map[string]any)
		if !ok {
			t.Fatalf("[P0] audit payload.previous = %T, want an object", entry.Payload["previous"])
		}
		newValues, ok := entry.Payload["new"].(map[string]any)
		if !ok {
			t.Fatalf("[P0] audit payload.new = %T, want an object", entry.Payload["new"])
		}
		if previous["category_id"] == newValues["category_id"] {
			t.Fatalf("[P0] audit previous/new category_id both = %v, want them to differ", previous["category_id"])
		}
		if newValues["category_id"] != newCategoryID {
			t.Fatalf("[P0] audit new.category_id = %v, want %s", newValues["category_id"], newCategoryID)
		}
	}
	if !found {
		t.Fatal("[P0] no od.overridden audit entry found for the correction")
	}
}

// 0.12b-INT-002 — a correction that collides with another present record for
// the same student/event/od_date/slot is rejected as SCAN_DUPLICATE, by the
// same unique index the add path relies on.
func TestManagementCorrect_CollisionReturnsScanDuplicate(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	adminID := fixture.adminID
	targetSlot := 7
	// The unique index is keyed per (student_id, event_id, od_date, slot), so
	// the collision must be the SAME student occupying both slots — two
	// different students in the same slot is ordinary, not a duplicate.
	studentID := seedManagementStudent(t, ctx, migrator)
	seedManagementRecordForStudent(t, ctx, migrator, fixture, studentID, targetSlot, fixture.categoryID, fixture.eventDate, "search")
	movingRecordID := seedManagementRecordForStudent(t, ctx, migrator, fixture, studentID, 8, fixture.categoryID, fixture.eventDate, "search")

	svc := odsvc.NewManagementService(runtime, store.New(runtime))
	_, err := svc.Correct(ctx, odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}, odsvc.CorrectionInput{
		RecordID:   movingRecordID,
		ReasonNote: "Move onto the already-occupied slot on purpose.",
		Slot:       &targetSlot,
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeSCANDUPLICATE {
		t.Fatalf("[P0] colliding correction error = %v, want SCAN_DUPLICATE", err)
	}
}

// 0.12b-INT-003 — a correction whose new od_date falls outside the event's
// range is rejected as VALIDATION_FAILED by the existing 0.12a range trigger.
func TestManagementCorrect_OutOfRangeDateReturnsValidationFailed(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	adminID := fixture.adminID
	recordID := seedManagementRecord(t, ctx, migrator, fixture, 3, fixture.categoryID, fixture.eventDate, "search")

	svc := odsvc.NewManagementService(runtime, store.New(runtime))
	outOfRange := fixture.eventDate.AddDate(0, 0, 5).Format(time.DateOnly)
	_, err := svc.Correct(ctx, odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}, odsvc.CorrectionInput{
		RecordID:   recordID,
		ReasonNote: "Date is intentionally outside the event for validation.",
		ODDate:     &outOfRange,
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("[P0] out-of-range correction error = %v, want VALIDATION_FAILED", err)
	}
}

// 0.12b-INT-004 — removal soft-removes the row (never a hard delete: the
// runtime role has no DELETE grant), and the freed slot accepts a fresh mark
// for the same student/event/od_date/slot.
func TestManagementRemove_FreesSlotForFreshMark(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	adminID := fixture.adminID
	slot := 9
	recordID := seedManagementRecord(t, ctx, migrator, fixture, slot, fixture.categoryID, fixture.eventDate, "search")

	svc := odsvc.NewManagementService(runtime, store.New(runtime))
	result, err := svc.Remove(ctx, odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}, recordID, "Duplicate paper register entry.")
	if err != nil {
		t.Fatalf("[P0] Remove = %v, want nil", err)
	}
	if result.Status != "removed" {
		t.Fatalf("[P0] Remove result status = %q, want removed", result.Status)
	}
	var rowCount int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM od_records WHERE id = $1`, recordID).Scan(&rowCount); err != nil {
		t.Fatalf("count rows for removed id: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("[P0] removal deleted the row (count=%d), want a soft-removed row to survive", rowCount)
	}

	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)
	svcMark := roleAwareMarkService(runtime, fixture)
	studentID := fixture.studentID
	reason := "re-mark after the wrong entry was removed"
	if _, err := svcMark.Mark(ctx, odsvc.Actor{UserID: teacherID, Email: "teacher@amrita.edu", Role: "teacher"}, odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodSearch,
		StudentID:     &studentID,
		Slot:          &slot,
		ReasonNote:    &reason,
	}); err != nil {
		t.Fatalf("[P0] fresh mark into a freed slot = %v, want nil", err)
	}
}

// 0.12b-INT-005 — a second removal of an already-removed record is rejected
// as OD_RECORD_ALREADY_REMOVED, never a silent no-op or a second removal
// audit entry.
func TestManagementRemove_RemoveThenRemoveReturnsAlreadyRemoved(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	adminID := fixture.adminID
	recordID := seedManagementRecord(t, ctx, migrator, fixture, 10, fixture.categoryID, fixture.eventDate, "search")

	svc := odsvc.NewManagementService(runtime, store.New(runtime))
	actor := odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}
	if _, err := svc.Remove(ctx, actor, recordID, "First removal."); err != nil {
		t.Fatalf("[P0] first Remove = %v, want nil", err)
	}
	_, err := svc.Remove(ctx, actor, recordID, "A terminal row must not be removed twice.")
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeODRECORDALREADYREMOVED {
		t.Fatalf("[P0] second Remove error = %v, want OD_RECORD_ALREADY_REMOVED", err)
	}
}

// 0.12b-INT-006 — a teacher outside the record's branch gets a UNIFORM
// RBAC_FORBIDDEN for correct, remove, and audit, whether the record is
// present or already removed. This is the review's overturn of T8: RBAC now
// runs before the terminal-status check, so an unauthorized caller never
// learns whether an out-of-scope record exists or has already been removed.
func TestManagementCorrectRemoveAudit_TeacherCrossBranchAlwaysForbiddenNotLeakingState(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	adminID := fixture.adminID
	// A teacher associated with a DIFFERENT branch than the fixture's own, so
	// TeacherCanManageODRecord refuses every record seeded under the fixture
	// branch. seedTeacherAndBranch always associates the fixture's own branch,
	// so a second, disjoint branch/teacher pair is seeded directly here.
	otherBranchID := uuid.NewString()
	otherBranchCode := "OT" + strings.ToUpper(strings.ReplaceAll(otherBranchID, "-", ""))[:6]
	if _, err := migrator.Exec(ctx, `INSERT INTO branches (id, code, name) VALUES ($1, $2, 'Other Branch')`, otherBranchID, otherBranchCode); err != nil {
		t.Fatalf("seed other branch: %v", err)
	}
	outsiderTeacherID := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'outsider-teacher@amrita.edu', 'Outsider Teacher', 'teacher')`,
		outsiderTeacherID, "atdd-outsider-teacher-"+outsiderTeacherID); err != nil {
		t.Fatalf("seed outsider teacher: %v", err)
	}
	if _, err := migrator.Exec(ctx, `INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, outsiderTeacherID, otherBranchID); err != nil {
		t.Fatalf("associate outsider teacher branch: %v", err)
	}
	outsider := odsvc.Actor{UserID: outsiderTeacherID, Email: "outsider-teacher@amrita.edu", Role: "teacher"}

	presentRecordID := seedManagementRecord(t, ctx, migrator, fixture, 11, fixture.categoryID, fixture.eventDate, "search")
	removedRecordID := seedManagementRecord(t, ctx, migrator, fixture, 12, fixture.categoryID, fixture.eventDate, "search")
	if _, err := migrator.Exec(ctx, `UPDATE od_records SET status = 'removed' WHERE id = $1`, removedRecordID); err != nil {
		t.Fatalf("seed pre-removed record: %v", err)
	}
	unknownRecordID := uuid.NewString()

	svc := odsvc.NewManagementService(runtime, store.New(runtime))
	assertForbidden := func(t *testing.T, label string, err error) {
		t.Helper()
		var de domain.DomainError
		if !errors.As(err, &de) || de.Code != contracts.ErrorCodeRBACFORBIDDEN {
			t.Fatalf("[P0] %s error = %v, want RBAC_FORBIDDEN (not a state-specific code)", label, err)
		}
	}

	for _, recordID := range []string{presentRecordID, removedRecordID} {
		_, err := svc.Correct(ctx, outsider, odsvc.CorrectionInput{RecordID: recordID, ReasonNote: "Outsider must not correct another branch's record.", Slot: intPtr(6)})
		assertForbidden(t, "Correct("+recordID+")", err)

		_, err = svc.Remove(ctx, outsider, recordID, "Outsider must not remove another branch's record.")
		assertForbidden(t, "Remove("+recordID+")", err)

		_, err = svc.Audit(ctx, outsider, recordID)
		assertForbidden(t, "Audit("+recordID+")", err)
	}

	// The unknown record id must be rejected as NOT_FOUND regardless of actor —
	// this is the control case proving the forbidden result above is really
	// about branch scope, not merely "any id an outsider supplies is refused".
	_, err := svc.Correct(ctx, odsvc.Actor{UserID: adminID, Email: "admin@amrita.edu", Role: "admin"}, odsvc.CorrectionInput{RecordID: unknownRecordID, ReasonNote: "Admin against an absent id.", Slot: intPtr(6)})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeODRECORDNOTFOUND {
		t.Fatalf("[P0] admin correct of an unknown record id = %v, want OD_RECORD_NOT_FOUND", err)
	}
}

func intPtr(v int) *int { return &v }

// seedManagementStudent inserts one new student in the fixture branch,
// returning its id.
func seedManagementStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	studentID := uuid.NewString()
	rollSuffix := strings.ToUpper(strings.ReplaceAll(studentID, "-", ""))[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1, $2, 'ATDD Management Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')`,
		studentID, "CB.SC.U4ODM"+rollSuffix); err != nil {
		t.Fatalf("seed management student: %v", err)
	}
	return studentID
}

// seedManagementRecord inserts one new student in the fixture branch and one
// present od_records row for it with a reason note already set, returning the
// record id. Direct SQL rather than Mark() so the caller controls slot/day
// precisely without depending on ActiveSlot's live-clock derivation.
func seedManagementRecord(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	fixture markFixture, slot int, categoryID string, odDate time.Time, captureMethod string,
) string {
	t.Helper()
	return seedManagementRecordForStudent(t, ctx, pool, fixture, seedManagementStudent(t, ctx, pool), slot, categoryID, odDate, captureMethod)
}

// seedManagementRecordForStudent is seedManagementRecord for a caller-supplied
// (possibly already-used) student id, so a test can seed two records for the
// SAME student — the only way to exercise the per-student duplicate index.
func seedManagementRecordForStudent(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	fixture markFixture, studentID string, slot int, categoryID string, odDate time.Time, captureMethod string,
) string {
	t.Helper()
	recordID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, reason_note, server_received_at)
		VALUES ($1, $2, $3, $4, $5, $6::date, 'present', $7, $8, 'teacher', 'management fixture seed reason', now())`,
		recordID, studentID, fixture.eventID, categoryID, slot, odDate.Format(time.DateOnly), captureMethod, fixture.coordinatorID); err != nil {
		t.Fatalf("seed management record: %v", err)
	}
	return recordID
}
