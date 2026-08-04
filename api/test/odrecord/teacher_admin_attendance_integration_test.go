//go:build integration

// Real-Postgres tests for Story 0.9's teacher/admin search-mark write path and
// the admin attendance read.
//
// Deliberately in the EXISTING api/test/odrecord package, matching the reason
// marking_integration_test.go gives: ./test/odrecord/... is already wired into
// both api/Makefile's INTEGRATION_PKGS and .github/workflows/api-ci.yml, so a
// new package here would silently never run in CI the way three earlier
// stories' packages did.
//
// Helpers (testCtx, dsnOrSkip, connect, seedMarkFixture, markFixture,
// fixedClock, countRows) come from odrecord_integration_atdd_test.go and
// marking_integration_test.go, tagged `integration` so all three files compile
// together.
package odrecordtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// 0.9-INT-001a — a branch-associated teacher's search-mark writes through the
// REAL RoleAwareMarkGuard (not a stub), commits exactly one od_records row and
// one od.created audit row, and never touches the coordinator fallback-credit
// ledger.
func TestTeacherAdminSearchMarkIntegration_AuditConservationAndNoCreditDebit(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)

	beforeRecords := countRows(t, ctx, migrator, "od_records")
	beforeAudit := countRows(t, ctx, migrator, "audit_log")

	svc := roleAwareMarkService(runtime, fixture)
	slot := 4
	studentID := fixture.studentID
	result, err := svc.Mark(ctx,
		odsvc.Actor{UserID: teacherID, Email: "teacher@amrita.edu", Role: "teacher"},
		odsvc.MarkInput{
			EventID:       fixture.eventID,
			CategoryID:    fixture.categoryID,
			CaptureMethod: odsvc.CaptureMethodSearch,
			StudentID:     &studentID,
			Slot:          &slot,
		})
	if err != nil {
		t.Fatalf("[P0] teacher search mark in-branch = %v, want nil", err)
	}
	if result.CaptureMethod != odsvc.CaptureMethodSearch {
		t.Errorf("[P0] result capture_method = %q, want search", result.CaptureMethod)
	}

	if got := countRows(t, ctx, migrator, "od_records") - beforeRecords; got != 1 {
		t.Errorf("[P0] od_records grew by %d, want exactly 1", got)
	}
	if got := countRows(t, ctx, migrator, "audit_log") - beforeAudit; got != 1 {
		t.Errorf("[P0] audit_log grew by %d, want exactly 1 (od.created, same transaction)", got)
	}

	var actorRole, capture string
	if err := migrator.QueryRow(ctx,
		`SELECT actor_role, capture_method FROM od_records WHERE id = $1`, result.RecordID,
	).Scan(&actorRole, &capture); err != nil {
		t.Fatalf("read back the record: %v", err)
	}
	if actorRole != "teacher" || capture != "search" {
		t.Errorf("[P0] persisted actor_role/capture_method = %q/%q, want teacher/search", actorRole, capture)
	}

	if got := creditConsumedFor(t, ctx, migrator, fixture.eventID, teacherID); got != 0 {
		t.Errorf("[P0] teacher search-mark consumed %d fallback credits, want 0 — that ledger is coordinator-only", got)
	}
}

// 0.9-INT-001b — two concurrent teacher/admin search marks for the same
// student/event/slot must conserve to exactly one row: one success, one
// SCAN_DUPLICATE, one od_records row, one od.created audit row.
func TestTeacherAdminSearchMarkIntegration_DuplicateConcurrentMarksConserveRows(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)

	svc := roleAwareMarkService(runtime, fixture)
	actor := odsvc.Actor{UserID: teacherID, Email: "teacher@amrita.edu", Role: "teacher"}
	slot := 5
	studentID := fixture.studentID

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Mark(ctx, actor, odsvc.MarkInput{
				EventID:       fixture.eventID,
				CategoryID:    fixture.categoryID,
				CaptureMethod: odsvc.CaptureMethodSearch,
				StudentID:     &studentID,
				Slot:          &slot,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var successes, duplicates int
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		var de domain.DomainError
		if errors.As(err, &de) && de.Code == contracts.ErrorCodeSCANDUPLICATE {
			duplicates++
			continue
		}
		t.Fatalf("unexpected race error: %v", err)
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("[P0] race result successes=%d duplicates=%d, want 1/1", successes, duplicates)
	}

	var odRows, auditRows int
	if err := migrator.QueryRow(ctx,
		`SELECT count(*) FROM od_records WHERE student_id = $1 AND event_id = $2 AND slot = $3`,
		fixture.studentID, fixture.eventID, slot,
	).Scan(&odRows); err != nil {
		t.Fatalf("count od rows: %v", err)
	}
	if err := migrator.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE action = $1 AND target_table = 'od_records' AND payload->>'event_id' = $2 AND (payload->>'slot')::int = $3
	`, string(domain.ActionODCreated), fixture.eventID, slot).Scan(&auditRows); err != nil {
		t.Fatalf("count od.created audit rows: %v", err)
	}
	if odRows != 1 || auditRows != 1 {
		t.Fatalf("[P0] od/audit conservation = %d/%d, want exactly one each", odRows, auditRows)
	}
}

// 0.9-INT-001c — the admin attendance read applies category/slot filters
// BEFORE pagination and never leaks a row from another event.
func TestAdminAttendanceIntegration_FiltersBeforePagination(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)

	// The row that must survive every filter below.
	wantStudent := seedAttendanceRecord(t, ctx, migrator, fixture.eventID, fixture.categoryID, 1, teacherID, "teacher")

	// A sibling category on the SAME event — must be excluded by the category filter.
	siblingCategory := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO categories (id, event_id, name) VALUES ($1, $2, 'ATDD 0.9 Sibling Category')`,
		siblingCategory, fixture.eventID); err != nil {
		t.Fatalf("seed sibling category: %v", err)
	}
	seedAttendanceRecord(t, ctx, migrator, fixture.eventID, siblingCategory, 1, teacherID, "teacher")

	// The same category, a different slot — must be excluded by the slot filter.
	seedAttendanceRecord(t, ctx, migrator, fixture.eventID, fixture.categoryID, 2, teacherID, "teacher")

	// An entirely different event — must never appear regardless of filters.
	otherEventID := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO events (id, name, event_date, status) VALUES ($1, 'ATDD 0.9 Other Event', $2, 'active')`,
		otherEventID, fixture.slotOneTime); err != nil {
		t.Fatalf("seed other event: %v", err)
	}
	otherCategoryID := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO categories (id, event_id, name) VALUES ($1, $2, 'ATDD 0.9 Other Category')`,
		otherCategoryID, otherEventID); err != nil {
		t.Fatalf("seed other category: %v", err)
	}
	seedAttendanceRecord(t, ctx, migrator, otherEventID, otherCategoryID, 1, teacherID, "teacher")

	svc := marking.NewPortalMarkReadService(runtime)
	slot := 1
	page, err := svc.Attendance(ctx, marking.Actor{Role: "admin"}, fixture.eventID, &fixture.categoryID, &slot, 1, 20)
	if err != nil {
		t.Fatalf("[P0] Attendance(event, category, slot) = %v, want nil", err)
	}
	if page.Total != 1 {
		t.Fatalf("[P0] Total = %d, want 1 — the sibling category, sibling slot, and other event must all be excluded", page.Total)
	}
	if len(page.Data) != 1 || page.Data[0].Student.StudentID != wantStudent {
		t.Fatalf("[P0] Data = %+v, want exactly the seeded student %s", page.Data, wantStudent)
	}

	// The same event with no filters must see all three of ITS rows (not the other event's).
	all, err := svc.Attendance(ctx, marking.Actor{Role: "admin"}, fixture.eventID, nil, nil, 1, 20)
	if err != nil {
		t.Fatalf("[P0] Attendance(event, no filters) = %v, want nil", err)
	}
	if all.Total != 3 {
		t.Fatalf("[P0] unfiltered Total = %d, want 3 (this event's rows only, not the other event's)", all.Total)
	}
}

// 0.9-INT-001d — deleting the marking actor's user row (ON DELETE SET NULL)
// must leave the attendance record readable with nullable marked_by fields,
// not an error.
func TestAdminAttendanceIntegration_NullActorStillRendersRecord(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	teacherID := seedTeacherAndBranch(t, ctx, migrator, fixture)

	svc := roleAwareMarkService(runtime, fixture)
	slot := 6
	studentID := fixture.studentID
	if _, err := svc.Mark(ctx,
		odsvc.Actor{UserID: teacherID, Email: "teacher@amrita.edu", Role: "teacher"},
		odsvc.MarkInput{
			EventID:       fixture.eventID,
			CategoryID:    fixture.categoryID,
			CaptureMethod: odsvc.CaptureMethodSearch,
			StudentID:     &studentID,
			Slot:          &slot,
		}); err != nil {
		t.Fatalf("seed the mark to later orphan: %v", err)
	}

	// user_branches.user_id is ON DELETE RESTRICT (0008_teacher_branches.sql), so
	// the branch association must go first or the user delete itself fails.
	if _, err := migrator.Exec(ctx, `DELETE FROM user_branches WHERE user_id = $1`, teacherID); err != nil {
		t.Fatalf("clear teacher branch association: %v", err)
	}
	if _, err := migrator.Exec(ctx, `DELETE FROM users WHERE id = $1`, teacherID); err != nil {
		t.Fatalf("delete teacher user: %v", err)
	}

	readSvc := marking.NewPortalMarkReadService(runtime)
	page, err := readSvc.Attendance(ctx, marking.Actor{Role: "admin"}, fixture.eventID, nil, &slot, 1, 20)
	if err != nil {
		t.Fatalf("[P0] Attendance after actor deletion = %v, want nil — a deleted actor must not break the read", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("[P0] Data = %+v, want exactly one row surviving the actor's deletion", page.Data)
	}
	row := page.Data[0]
	if row.MarkedBy.UserID != nil || row.MarkedBy.Email != nil || row.MarkedBy.Name != nil {
		t.Errorf("[P1] MarkedBy = %+v, want all nil after ON DELETE SET NULL", row.MarkedBy)
	}
	if row.ActorRole != "teacher" {
		t.Errorf("[P1] ActorRole = %q, want teacher — the row's own actor_role column is untouched by the user delete", row.ActorRole)
	}
}

// ---------------------------------------------------------------------------
// Scaffold helpers — real bodies, no logic of their own.
// ---------------------------------------------------------------------------

// roleAwareMarkService wires the REAL RoleAwareMarkGuard used in production
// (api/cmd/api/main.go), not a test stub — this is the seam the code review's
// flagship finding proved was never reached, so these tests must exercise it.
func roleAwareMarkService(runtime *pgxpool.Pool, fixture markFixture) *odsvc.Service {
	eventReader := svcevent.NewSQLCReader(runtime)
	policy := marking.NewPolicy(marking.NewEventReader(eventReader))
	return odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: fixture.slotOneTime},
		marking.NewRoleAwareMarkGuard(policy, runtime),
		odsvc.NewSQLCCreditLedger(runtime),
	)
}

// seedTeacherAndBranch creates a teacher-role user associated with the
// fixture's branch via user_branches, and associates that same branch with the
// fixture's event via event_branches — both required by TeacherCanSearchMark.
func seedTeacherAndBranch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture markFixture) string {
	t.Helper()
	teacherID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'atdd-teacher@amrita.edu', 'ATDD Teacher', 'teacher')
		ON CONFLICT (supabase_subject) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role`,
		teacherID, "atdd-teacher-"+teacherID); err != nil {
		t.Fatalf("seed teacher: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_branches (user_id, branch_id)
		VALUES ($1, '91000000-0000-4000-8000-000000000001')
		ON CONFLICT DO NOTHING`, teacherID); err != nil {
		t.Fatalf("associate teacher branch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO event_branches (event_id, branch_id)
		VALUES ($1, '91000000-0000-4000-8000-000000000001')
		ON CONFLICT DO NOTHING`, fixture.eventID); err != nil {
		t.Fatalf("associate event branch: %v", err)
	}
	return teacherID
}

// seedAttendanceRecord inserts one new student in the fixture branch and one
// present od_records row for it, returning the student id so callers can
// assert on which row survived a filter.
func seedAttendanceRecord(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	eventID, categoryID string, slot int, actorUserID, actorRole string,
) string {
	t.Helper()
	studentID := uuid.NewString()
	rollSuffix := strings.ToUpper(strings.ReplaceAll(studentID, "-", ""))[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1, $2, 'ATDD Attendance Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')`,
		studentID, "CB.SC.U4ODR"+rollSuffix); err != nil {
		t.Fatalf("seed attendance student: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, status, capture_method, actor_user_id, actor_role, server_received_at)
		VALUES ($1, $2, $3, $4, $5, 'present', 'search', $6, $7, now())`,
		uuid.NewString(), studentID, eventID, categoryID, slot, actorUserID, actorRole); err != nil {
		t.Fatalf("seed attendance record: %v", err)
	}
	return studentID
}

// creditConsumedFor mirrors capture_credit_integration_test.go's creditConsumed,
// keyed by an arbitrary (event, user) pair rather than the fixture's own
// coordinator — a teacher/admin search-mark must leave no row at all here.
func creditConsumedFor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID, userID string) int {
	t.Helper()
	var consumed int
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(consumed, 0)
		FROM event_capture_credit_usage
		WHERE event_id = $1 AND user_id = $2`, eventID, userID).Scan(&consumed)
	if err == nil {
		return consumed
	}
	if strings.Contains(err.Error(), "no rows") {
		return 0
	}
	t.Fatalf("read credit usage: %v", err)
	return 0
}
