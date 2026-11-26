//go:build integration

package eventtest

// Taking staff access away, against real Postgres with the real grants and
// triggers: removing one coordinator from one event, and demoting a staff user
// to student.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	teachersvc "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
	"github.com/Shruhath/OD-Amrita/api/internal/service/userrole"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func seedCategoryAccess(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, categoryID, adminID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_category_assignments (user_id, category_id, created_by_user_id)
		VALUES ($1, $2, $3)`, userID, categoryID, adminID); err != nil {
		t.Fatalf("seed category access: %v", err)
	}
}

func seedCreditUsage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID, userID uuid.UUID, day string, consumed int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed)
		VALUES ($1, $2, $3::date, $4)`, eventID, userID, day, consumed); err != nil {
		t.Fatalf("seed credit usage: %v", err)
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

func wantDomainCode(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
}

func TestCoordinatorRemoval_DeletesTheAssignmentItsCategoriesAndCreditsWithOneAuditRow(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "removal-admin@amrita.edu")
	coordID := seedUser(t, ctx, migrator, "removal-coord@amrita.edu", "coordinator")
	removedEvent := seedActiveEvent(t, ctx, migrator, "Removed From")
	keptEvent := seedActiveEvent(t, ctx, migrator, "Kept On")
	removedCategory := seedCategory(t, ctx, migrator, removedEvent, "Removed Category")
	keptCategory := seedCategory(t, ctx, migrator, keptEvent, "Kept Category")
	insertCoordinatorAssignment(t, ctx, migrator, removedEvent, coordID, adminID)
	insertCoordinatorAssignment(t, ctx, migrator, keptEvent, coordID, adminID)
	seedCategoryAccess(t, ctx, migrator, coordID, removedCategory, adminID)
	seedCategoryAccess(t, ctx, migrator, coordID, keptCategory, adminID)
	seedCreditUsage(t, ctx, migrator, removedEvent, coordID, "2026-08-18", 2)
	seedCreditUsage(t, ctx, migrator, removedEvent, coordID, "2026-08-19", 1)
	seedCreditUsage(t, ctx, migrator, keptEvent, coordID, "2026-08-18", 4)

	svc := eventAdminService(runtime)
	actor := eventsvc.Actor{UserID: adminID.String(), Email: "removal-admin@amrita.edu", Role: "admin"}
	if err := svc.RemoveCoordinatorAssignment(ctx, actor, eventsvc.RemoveCoordinatorAssignmentInput{
		EventID: removedEvent.String(), UserID: coordID.String(),
	}); err != nil {
		t.Fatalf("[P0] RemoveCoordinatorAssignment: %v", err)
	}

	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_coordinator_assignments WHERE user_id = $1 AND event_id = $2`, coordID, removedEvent); n != 0 {
		t.Errorf("[P0] the assignment must be gone, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_coordinator_assignments WHERE user_id = $1 AND event_id = $2`, coordID, keptEvent); n != 1 {
		t.Errorf("[P0] the other event's assignment must stay, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM user_category_assignments WHERE user_id = $1 AND category_id = $2`, coordID, removedCategory); n != 0 {
		t.Errorf("[P0] the removed event's category access must be gone, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM user_category_assignments WHERE user_id = $1 AND category_id = $2`, coordID, keptCategory); n != 1 {
		t.Errorf("[P0] the other event's category access must stay, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_capture_credit_usage WHERE user_id = $1 AND event_id = $2`, coordID, removedEvent); n != 0 {
		t.Errorf("[P0] the removed assignment's credit counters must be gone, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_capture_credit_usage WHERE user_id = $1 AND event_id = $2`, coordID, keptEvent); n != 1 {
		t.Errorf("[P0] the other event's credit counters must stay, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM users WHERE id = $1 AND role = 'coordinator'`, coordID); n != 1 {
		t.Errorf("[P1] removal from one event must not change the global role")
	}

	var payloadText string
	if err := migrator.QueryRow(ctx, `SELECT payload::text FROM audit_log WHERE action = $1`, string(domain.ActionCoordinatorRemoved)).Scan(&payloadText); err != nil {
		t.Fatalf("[P0] exactly one coordinator.removed audit row expected: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["removed_credit_usage_consumed"] != float64(3) || payload["removed_credit_usage_rows"] != float64(2) {
		t.Errorf("[P1] audit payload must snapshot the removed credits, got %v", payload)
	}

	// Marking access ends with the row: the exact (uncached) policy refuses.
	policy := marking.NewPolicy(marking.NewEventReader(eventsvc.NewSQLCReader(runtime)))
	if err := policy.AssertCanMark(ctx, marking.Actor{UserID: coordID.String(), Role: "coordinator"},
		removedEvent.String(), removedCategory.String()); !errors.Is(err, marking.ErrEventNotAssigned) {
		t.Errorf("[P0] a removed coordinator must not be able to mark, got %v", err)
	}

	// A repeat is a 404, and writes no second audit row.
	err := svc.RemoveCoordinatorAssignment(ctx, actor, eventsvc.RemoveCoordinatorAssignmentInput{EventID: removedEvent.String(), UserID: coordID.String()})
	wantDomainCode(t, err, contracts.ErrorCodeUSERNOTFOUND)
	assertEventActionTotal(t, ctx, migrator, domain.ActionCoordinatorRemoved, 1)

	// And the coordinator can be assigned again, starting clean.
	if _, err := svc.CreateCoordinatorAssignment(ctx, actor, eventsvc.CreateCoordinatorAssignmentInput{
		EventID: removedEvent.String(), Email: "removal-coord@amrita.edu", CategoryIDs: []string{removedCategory.String()},
	}); err != nil {
		t.Errorf("[P1] re-assignment after removal must work: %v", err)
	}
}

func TestCoordinatorRemovalRefusedOnArchivedEvent(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "archived-removal-admin@amrita.edu")
	coordID := seedUser(t, ctx, migrator, "archived-removal@amrita.edu", "coordinator")
	archived := seedArchivedEvent(t, ctx, migrator, "Archived Removal")
	insertCoordinatorAssignment(t, ctx, migrator, archived, coordID, adminID)

	err := eventAdminService(runtime).RemoveCoordinatorAssignment(ctx,
		eventsvc.Actor{UserID: adminID.String(), Email: "archived-removal-admin@amrita.edu", Role: "admin"},
		eventsvc.RemoveCoordinatorAssignmentInput{EventID: archived.String(), UserID: coordID.String()})
	wantDomainCode(t, err, contracts.ErrorCodeEVENTARCHIVED)
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_coordinator_assignments WHERE event_id = $1`, archived); n != 1 {
		t.Errorf("[P0] an archived event's assignment must stay, found %d", n)
	}
	assertEventActionTotal(t, ctx, migrator, domain.ActionCoordinatorRemoved, 0)

	// The trigger is the backstop below the service: the grant alone does not
	// let the runtime role touch an archived event.
	_, err = runtime.Exec(ctx, `DELETE FROM event_coordinator_assignments WHERE event_id = $1`, archived)
	var pgErr *pgconn.PgError
	if !asPgError(err, &pgErr) || pgErr.Code != "55000" {
		t.Errorf("[P0] runtime delete on an archived event = %v, want SQLSTATE 55000 from the trigger", err)
	}
}

func demotionService(runtime *pgxpool.Pool) *userrole.Service {
	return userrole.NewService(store.New(runtime), userrole.NewSQLCWriter())
}

func TestDemotion_CoordinatorLosesActiveAccessKeepsArchivedHistoryAndIsLoggedOut(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "demote-admin@amrita.edu")
	coordID := seedUser(t, ctx, migrator, "demote-coord@amrita.edu", "coordinator")
	active := seedActiveEvent(t, ctx, migrator, "Demotion Active")
	archived := seedArchivedEvent(t, ctx, migrator, "Demotion Archived")
	activeCategory := seedCategory(t, ctx, migrator, active, "Active Category")
	archivedCategory := seedCategory(t, ctx, migrator, archived, "Archived Category")
	insertCoordinatorAssignment(t, ctx, migrator, active, coordID, adminID)
	insertCoordinatorAssignment(t, ctx, migrator, archived, coordID, adminID)
	seedCategoryAccess(t, ctx, migrator, coordID, activeCategory, adminID)
	seedCategoryAccess(t, ctx, migrator, coordID, archivedCategory, adminID)
	seedCreditUsage(t, ctx, migrator, active, coordID, "2026-08-18", 2)
	seedCreditUsage(t, ctx, migrator, archived, coordID, "2025-08-18", 1)

	actor := userrole.Actor{UserID: adminID.String(), Email: "demote-admin@amrita.edu", Role: "admin"}
	result, err := demotionService(runtime).Demote(ctx, actor, coordID.String())
	if err != nil {
		t.Fatalf("[P0] Demote: %v", err)
	}
	if !result.Changed || result.PreviousRole != "coordinator" || result.Role != "student" || !result.SessionsRevoked {
		t.Fatalf("[P0] result = %#v", result)
	}
	if result.RemovedCoordinatorAssignments() != 1 || result.RemovedCategoryAssignments != 2 {
		t.Errorf("[P1] removed counts = %d assignments / %d categories, want 1 / 2",
			result.RemovedCoordinatorAssignments(), result.RemovedCategoryAssignments)
	}

	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM users WHERE id = $1 AND role = 'student'`, coordID); n != 1 {
		t.Errorf("[P0] role must be student")
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_coordinator_assignments WHERE user_id = $1 AND event_id = $2`, coordID, active); n != 0 {
		t.Errorf("[P0] the active-event assignment must be gone")
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_coordinator_assignments WHERE user_id = $1 AND event_id = $2`, coordID, archived); n != 1 {
		t.Errorf("[P1] the archived-event assignment stays as history")
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM user_category_assignments WHERE user_id = $1`, coordID); n != 0 {
		t.Errorf("[P0] every category assignment must be gone, found %d", n)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM event_capture_credit_usage WHERE user_id = $1 AND event_id = $2`, coordID, active); n != 0 {
		t.Errorf("[P0] active-event credit counters must be gone")
	}
	if n := countRows(t, ctx, migrator, `
		SELECT count(*) FROM auth_session_revocations r JOIN users u ON u.supabase_subject = r.supabase_subject
		WHERE u.id = $1 AND r.scope = 'all' AND r.revoked_by_user_id = $2`, coordID, adminID); n != 1 {
		t.Errorf("[P0] the demotion must record exactly one scope=all revocation, found %d", n)
	}
	assertEventAuditCount(t, ctx, migrator, domain.ActionUserDemoted, coordID.String(), 1)
	// The revocation is part of user.demoted, not a second audit row.
	assertEventActionTotal(t, ctx, migrator, domain.ActionAuthSessionRevoked, 0)

	// A repeat is an idempotent no-op: no change, no new audit row, no new revocation.
	again, err := demotionService(runtime).Demote(ctx, actor, coordID.String())
	if err != nil || again.Changed {
		t.Fatalf("[P1] repeat demotion = %#v, %v; want a no-op", again, err)
	}
	assertEventAuditCount(t, ctx, migrator, domain.ActionUserDemoted, coordID.String(), 1)
}

func TestDemotion_TeacherLosesBranchesAndCanBeMadeATeacherAgain(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "teacher-demote-admin@amrita.edu")
	branchID := seedBranch(t, ctx, migrator, "CSE", "Computer Science")
	eventID := seedActiveEvent(t, ctx, migrator, "Teacher Demotion Event")
	categoryID := seedCategory(t, ctx, migrator, eventID, "Teacher Category")
	teacherID := seedUser(t, ctx, migrator, "demoted.teacher@amrita.edu", "teacher")
	if _, err := migrator.Exec(ctx, `INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, teacherID, branchID); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	seedCategoryAccess(t, ctx, migrator, teacherID, categoryID, adminID)

	result, err := demotionService(runtime).Demote(ctx,
		userrole.Actor{UserID: adminID.String(), Email: "teacher-demote-admin@amrita.edu", Role: "admin"}, teacherID.String())
	if err != nil {
		t.Fatalf("[P0] Demote: %v", err)
	}
	if result.RemovedTeacherBranches != 1 || result.RemovedCategoryAssignments != 1 {
		t.Errorf("[P1] removed %d branches / %d categories, want 1 / 1", result.RemovedTeacherBranches, result.RemovedCategoryAssignments)
	}
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM user_branches WHERE user_id = $1`, teacherID); n != 0 {
		t.Errorf("[P0] branch associations must be gone, found %d", n)
	}

	// Re-promotion inserts the branches again; a left-over row would collide on
	// the primary key. This is why demotion deletes rather than leaving them.
	teachers := teachersvc.NewService(teachersvc.NewSQLCReader(runtime), store.New(runtime), teachersvc.NewSQLCWriter())
	if _, err := teachers.Create(ctx,
		teachersvc.Actor{UserID: adminID.String(), Email: "teacher-demote-admin@amrita.edu", Role: "admin"},
		teachersvc.CreateInput{Email: "demoted.teacher@amrita.edu", BranchIDs: []string{branchID.String()}, CategoryIDs: []string{categoryID.String()}},
	); err != nil {
		t.Errorf("[P0] a demoted teacher must be promotable again: %v", err)
	}
}

func TestDemotion_RefusesSelfAndUnknownUsers(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "self-demote@amrita.edu")
	actor := userrole.Actor{UserID: adminID.String(), Email: "self-demote@amrita.edu", Role: "admin"}

	_, err := demotionService(runtime).Demote(ctx, actor, adminID.String())
	wantDomainCode(t, err, contracts.ErrorCodeSELFDEMOTIONREFUSED)
	_, err = demotionService(runtime).Demote(ctx, actor, uuid.NewString())
	wantDomainCode(t, err, contracts.ErrorCodeUSERNOTFOUND)
	if n := countRows(t, ctx, migrator, `SELECT count(*) FROM users WHERE id = $1 AND role = 'admin'`, adminID); n != 1 {
		t.Errorf("[P0] a refused self-demotion must leave the admin in place")
	}
	assertEventActionTotal(t, ctx, migrator, domain.ActionUserDemoted, 0)
}

// Two administrators demoting each other at the same moment must not leave the
// system with no admin: the admin rows are locked, so the second transaction
// sees that its actor is no longer an admin and is refused.
func TestDemotion_ConcurrentMutualDemotionLeavesAnAdmin(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminA := seedAdmin(t, ctx, migrator, "admin-a@amrita.edu")
	adminB := seedAdmin(t, ctx, migrator, "admin-b@amrita.edu")

	for round := 0; round < 5; round++ {
		if _, err := migrator.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = ANY($1)`, []uuid.UUID{adminA, adminB}); err != nil {
			t.Fatalf("reset admins: %v", err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, pair := range [][2]uuid.UUID{{adminA, adminB}, {adminB, adminA}} {
			wg.Add(1)
			go func(i int, actor, target uuid.UUID) {
				defer wg.Done()
				<-start
				_, errs[i] = demotionService(runtime).Demote(ctx, userrole.Actor{UserID: actor.String(), Email: "a@amrita.edu", Role: "admin"}, target.String())
			}(i, pair[0], pair[1])
		}
		close(start)
		wg.Wait()

		admins := countRows(t, ctx, migrator, `SELECT count(*) FROM users WHERE role = 'admin' AND id = ANY($1)`, []uuid.UUID{adminA, adminB})
		if admins != 1 {
			t.Fatalf("[P0] round %d: %d of the two admins remain (errors %v, %v), want exactly 1", round, admins, errs[0], errs[1])
		}
		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
				continue
			}
			wantDomainCode(t, err, contracts.ErrorCodeRBACFORBIDDEN)
		}
		if succeeded != 1 {
			t.Fatalf("[P0] round %d: %d demotions succeeded, want exactly 1", round, succeeded)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
