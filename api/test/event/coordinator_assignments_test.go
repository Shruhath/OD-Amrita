//go:build integration

package eventtest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestCoordinatorAssignmentsMigrationRoundTripsCleanly(t *testing.T) {
	ctx := testContext(t)
	migrator, _, migratorDSN := isolatedCategoryDatabase(t, ctx)

	assertTableExists(t, ctx, migrator, "event_coordinator_assignments", true)
	if got := triggerFunction(t, ctx, migrator, "event_coordinator_assignments"); got != "require_active_event_for_child_mutation" {
		t.Fatalf("trigger function = %q, want shared active-event guard", got)
	}

	runGoose(t, ctx, migratorDSN, "down-to", "11")
	assertTableExists(t, ctx, migrator, "event_coordinator_assignments", false)

	runGoose(t, ctx, migratorDSN, "up")
	assertTableExists(t, ctx, migrator, "event_coordinator_assignments", true)
}

func TestCoordinatorAssignmentsUniqueConstraintRejectsConcurrentDuplicates(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "coord-admin@amrita.edu")
	userID := seedUser(t, ctx, migrator, "coord-unique@amrita.edu", "coordinator")
	eventID := seedActiveEvent(t, ctx, migrator, "Coordinator Unique")

	if _, err := runtime.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ($1, $2, $3)`, userID, eventID, adminID); err != nil {
		t.Fatalf("insert first assignment: %v", err)
	}
	_, err := runtime.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ($1, $2, $3)`, userID, eventID, adminID)
	if err == nil {
		t.Fatal("duplicate assignment was accepted")
	}
	var pgErr *pgconn.PgError
	if !asPgError(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "event_coordinator_assignments_user_event_unique" {
		t.Fatalf("duplicate error = %v, want unique constraint violation", err)
	}
}

func TestCoordinatorAssignmentsArchivedEventGuardRefusesRuntimeWrites(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "coord-archived-admin@amrita.edu")
	userID := seedUser(t, ctx, migrator, "coord-archived@amrita.edu", "coordinator")
	archivedEvent := seedArchivedEvent(t, ctx, migrator, "Archived Coordinator Event")

	_, err := runtime.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ($1, $2, $3)`, userID, archivedEvent, adminID)
	if err == nil {
		t.Fatal("runtime insert under archived event was accepted")
	}
	var pgErr *pgconn.PgError
	if !asPgError(err, &pgErr) || pgErr.Code != "55000" {
		t.Fatalf("error = %v, want SQLSTATE 55000", err)
	}
}

func TestCoordinatorAssignmentsRuntimePrivilegesAllowUpdateButNotDelete(t *testing.T) {
	ctx := testContext(t)
	migrator, _, _ := isolatedCategoryDatabase(t, ctx)
	for privilege, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": false} {
		var got bool
		if err := migrator.QueryRow(ctx,
			`SELECT has_table_privilege('od_runtime', 'event_coordinator_assignments', $1)`, privilege,
		).Scan(&got); err != nil {
			t.Fatalf("check %s: %v", privilege, err)
		}
		if got != want {
			t.Fatalf("%s privilege = %v, want %v", privilege, got, want)
		}
	}
}

func TestCoordinatorAssignmentCreateCommitsRoleInsertAndAuditAtomically(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "coord-create-admin@amrita.edu")
	studentID := seedUser(t, ctx, migrator, "coord-create@amrita.edu", "student")
	eventID := seedActiveEvent(t, ctx, migrator, "Coordinator Create")

	svc := eventsvc.NewService(eventsvc.NewSQLCReader(runtime), store.New(runtime), eventsvc.NewSQLCWriter())
	created, err := svc.CreateCoordinatorAssignment(ctx,
		eventsvc.Actor{UserID: adminID.String(), Email: "coord-create-admin@amrita.edu", Role: "admin"},
		eventsvc.CreateCoordinatorAssignmentInput{EventID: eventID.String(), Email: "coord-create@amrita.edu"},
	)
	if err != nil {
		t.Fatalf("CreateCoordinatorAssignment: %v", err)
	}
	if created.UserID != studentID.String() || created.EventID != eventID.String() {
		t.Fatalf("created = %#v", created)
	}

	var coordinatorRoles, assignments, auditRows int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1 AND role = 'coordinator'`, studentID).Scan(&coordinatorRoles); err != nil {
		t.Fatalf("count role: %v", err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM event_coordinator_assignments WHERE user_id = $1 AND event_id = $2`, studentID, eventID).Scan(&assignments); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'coordinator.assigned' AND target_table = 'event_coordinator_assignments'`).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if coordinatorRoles != 1 || assignments != 1 || auditRows != 1 {
		t.Fatalf("role/assignment/audit = %d/%d/%d, want 1/1/1", coordinatorRoles, assignments, auditRows)
	}
}

func TestCoordinatorAssignmentRejectionsLeaveNoAssignmentRoleChangeOrAudit(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "coord-reject-admin@amrita.edu")
	teacherID := seedUser(t, ctx, migrator, "coord-reject-teacher@amrita.edu", "teacher")
	eventID := seedActiveEvent(t, ctx, migrator, "Coordinator Reject")

	svc := eventsvc.NewService(eventsvc.NewSQLCReader(runtime), store.New(runtime), eventsvc.NewSQLCWriter())
	_, err := svc.CreateCoordinatorAssignment(ctx,
		eventsvc.Actor{UserID: adminID.String(), Email: "coord-reject-admin@amrita.edu", Role: "admin"},
		eventsvc.CreateCoordinatorAssignmentInput{EventID: eventID.String(), Email: "coord-reject-teacher@amrita.edu"},
	)
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeROLEALREADYASSIGNED {
		t.Fatalf("error = %#v, want ROLE_ALREADY_ASSIGNED", err)
	}

	var role string
	var assignments, auditRows int
	if err := migrator.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, teacherID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM event_coordinator_assignments`).Scan(&assignments); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'coordinator.assigned'`).Scan(&auditRows); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if role != "teacher" || assignments != 0 || auditRows != 0 {
		t.Fatalf("role/assignments/audit = %s/%d/%d, want teacher/0/0", role, assignments, auditRows)
	}
}

func TestCoordinatorAssignmentPolicyLimitsVisibleEventsAndMarkWrites(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	adminID := seedAdmin(t, ctx, migrator, "coord-policy-admin@amrita.edu")
	coordID := seedUser(t, ctx, migrator, "coord-policy@amrita.edu", "coordinator")
	assignedEvent := seedActiveEvent(t, ctx, migrator, "Assigned Event")
	unassignedEvent := seedActiveEvent(t, ctx, migrator, "Unassigned Event")
	assignedCategory := seedCategory(t, ctx, migrator, assignedEvent, "Assigned Category")
	unassignedCategory := seedCategory(t, ctx, migrator, unassignedEvent, "Unassigned Category")
	if _, err := migrator.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ($1, $2, $3)`, coordID, assignedEvent, adminID); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	policy := marking.NewPolicy(marking.NewEventReader(eventsvc.NewSQLCReader(runtime)))
	actor := marking.Actor{UserID: coordID.String(), Role: "coordinator"}
	visible, err := policy.VisibleEvents(ctx, actor)
	if err != nil {
		t.Fatalf("VisibleEvents: %v", err)
	}
	if len(visible) != 1 || visible[0].EventID != assignedEvent.String() {
		t.Fatalf("visible events = %#v, want only assigned active event", visible)
	}
	if err := policy.AssertCanMark(ctx, actor, assignedEvent.String(), assignedCategory.String()); err != nil {
		t.Fatalf("assigned event/category rejected: %v", err)
	}
	if err := policy.AssertCanMark(ctx, actor, unassignedEvent.String(), unassignedCategory.String()); !errors.Is(err, marking.ErrEventNotAssigned) {
		t.Fatalf("unassigned event error = %v, want ErrEventNotAssigned", err)
	}
	if err := policy.AssertCanMark(ctx, actor, assignedEvent.String(), unassignedCategory.String()); !errors.Is(err, marking.ErrCategoryNotFound) {
		t.Fatalf("foreign category error = %v, want ErrCategoryNotFound", err)
	}
}

func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, role)
		VALUES ($1, $2, $3, $4)`, id, "coord-user-"+id.String(), email, role); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedCategory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO categories (id, event_id, name) VALUES ($1, $2, $3)`, id, eventID, name); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	return id
}
