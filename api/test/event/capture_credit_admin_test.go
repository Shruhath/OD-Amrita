//go:build integration

package eventtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/campus"
	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// campusToday is the day the admin credit surface acts on.
//
// Computed the same way the service does — campus-local midnight in
// Asia/Kolkata — rather than as the test host's date, which differs from the
// campus date for five and a half hours of every day and would make these
// fixtures fail overnight in CI.
func campusToday() time.Time {
	return campus.Date(time.Now())
}

func TestCaptureCreditAdminSummary_ListsAssignedCoordinatorsOnly(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	fixture := seedCaptureCreditAdminFixture(t, ctx, migrator)
	otherEvent := seedActiveEvent(t, ctx, migrator, "Other Credit Event")
	otherCoord := seedUser(t, ctx, migrator, "other-credit-coord@amrita.edu", "coordinator")
	insertCoordinatorAssignment(t, ctx, migrator, otherEvent, otherCoord, fixture.adminID)
	// The pool is per DAY, and the admin surface reads TODAY's — a live cap
	// intervention can only ever be about the day in progress. Seeded at the
	// campus date rather than at whatever `now()` would default to, so the read
	// below asserts against a day this test named.
	today := campusToday()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed)
		VALUES ($1, $2, $3, 3)`, otherEvent, otherCoord, today); err != nil {
		t.Fatalf("seed other usage: %v", err)
	}
	if _, err := migrator.Exec(ctx, `
		INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed)
		VALUES ($1, $2, $3, 4)`, fixture.eventID, fixture.coordinatorA, today); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	summary, err := eventAdminService(runtime).ListCaptureCredits(ctx, adminActor(fixture), fixture.eventID.String())
	if err != nil {
		t.Fatalf("ListCaptureCredits: %v", err)
	}
	if summary.Config.Limit != 5 {
		t.Fatalf("default cap = %d, want 5", summary.Config.Limit)
	}
	if len(summary.Data) != 2 {
		t.Fatalf("[P0] summary rows = %d, want only the two assigned coordinators", len(summary.Data))
	}
	if summary.Data[0].UserID != fixture.coordinatorA.String() || summary.Data[0].Consumed != 4 || summary.Data[0].Remaining != 1 {
		t.Fatalf("first coordinator usage = %#v", summary.Data[0])
	}
	if summary.Data[1].UserID != fixture.coordinatorB.String() || summary.Data[1].Consumed != 0 || summary.Data[1].Remaining != 5 {
		t.Fatalf("second coordinator usage = %#v", summary.Data[1])
	}
}

func TestCaptureCreditAdminConfig_ChangeAuditsExactlyOnce(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	fixture := seedCaptureCreditAdminFixture(t, ctx, migrator)

	updated, err := eventAdminService(runtime).UpdateCaptureCreditConfig(ctx, adminActor(fixture), eventsvc.CaptureCreditConfigInput{
		EventID: fixture.eventID.String(),
		Limit:   0,
	})
	if err != nil {
		t.Fatalf("UpdateCaptureCreditConfig: %v", err)
	}
	if updated.Limit != 0 || updated.UpdatedByUserID == nil || *updated.UpdatedByUserID != fixture.adminID.String() {
		t.Fatalf("updated config = %#v", updated)
	}
	assertEventAuditCount(t, ctx, migrator, domain.ActionCaptureCreditCapChanged, fixture.eventID.String(), 1)

	var persisted int
	if err := migrator.QueryRow(ctx,
		`SELECT credit_limit FROM event_capture_credit_config WHERE event_id = $1`, fixture.eventID).Scan(&persisted); err != nil {
		t.Fatalf("read persisted cap: %v", err)
	}
	if persisted != 0 {
		t.Fatalf("[P0] persisted cap = %d, want 0 live-disable", persisted)
	}
}

func TestCaptureCreditAdminReset_ResetAuditsExactlyOnce(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	fixture := seedCaptureCreditAdminFixture(t, ctx, migrator)
	// Today's pool: a reset acts on the day in progress, so a row dated any
	// other day would leave nothing for it to zero and the assertion would pass
	// against an untouched database.
	if _, err := migrator.Exec(ctx, `
		INSERT INTO event_capture_credit_usage (event_id, user_id, usage_date, consumed)
		VALUES ($1, $2, $3, 3)`, fixture.eventID, fixture.coordinatorA, campusToday()); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	usage, err := eventAdminService(runtime).ResetCaptureCredits(ctx, adminActor(fixture), fixture.eventID.String(), fixture.coordinatorA.String())
	if err != nil {
		t.Fatalf("ResetCaptureCredits: %v", err)
	}
	if usage.Consumed != 0 || usage.Remaining != 5 || usage.UserID != fixture.coordinatorA.String() {
		t.Fatalf("reset usage = %#v", usage)
	}
	assertEventAuditCount(t, ctx, migrator, domain.ActionCaptureCreditReset, fixture.eventID.String()+":"+fixture.coordinatorA.String(), 1)

	nonAssigned := seedUser(t, ctx, migrator, "not-assigned-credit@amrita.edu", "coordinator")
	_, err = eventAdminService(runtime).ResetCaptureCredits(ctx, adminActor(fixture), fixture.eventID.String(), nonAssigned.String())
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeUSERNOTFOUND {
		t.Fatalf("non-assigned reset error = %v, want USER_NOT_FOUND", err)
	}
	assertEventAuditCount(t, ctx, migrator, domain.ActionCaptureCreditReset, fixture.eventID.String()+":"+nonAssigned.String(), 0)
}

func TestCaptureCreditAdminRejection_NoAuditOnRejectedInputs(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	fixture := seedCaptureCreditAdminFixture(t, ctx, migrator)
	archived := seedArchivedEvent(t, ctx, migrator, "Archived Credit Admin")
	svc := eventAdminService(runtime)

	rejections := []struct {
		name string
		run  func() error
		code contracts.ErrorCode
	}{
		{
			name: "non-admin",
			run: func() error {
				_, err := svc.UpdateCaptureCreditConfig(ctx, eventsvc.Actor{UserID: fixture.coordinatorA.String(), Email: "coord-a@amrita.edu", Role: "coordinator"}, eventsvc.CaptureCreditConfigInput{EventID: fixture.eventID.String(), Limit: 2})
				return err
			},
			code: contracts.ErrorCodeRBACFORBIDDEN,
		},
		{
			name: "negative cap",
			run: func() error {
				_, err := svc.UpdateCaptureCreditConfig(ctx, adminActor(fixture), eventsvc.CaptureCreditConfigInput{EventID: fixture.eventID.String(), Limit: -1})
				return err
			},
			code: contracts.ErrorCodeVALIDATIONFAILED,
		},
		{
			name: "archived event",
			run: func() error {
				_, err := svc.UpdateCaptureCreditConfig(ctx, adminActor(fixture), eventsvc.CaptureCreditConfigInput{EventID: archived.String(), Limit: 3})
				return err
			},
			code: contracts.ErrorCodeEVENTARCHIVED,
		},
		{
			name: "unknown coordinator",
			run: func() error {
				_, err := svc.ResetCaptureCredits(ctx, adminActor(fixture), fixture.eventID.String(), uuid.NewString())
				return err
			},
			code: contracts.ErrorCodeUSERNOTFOUND,
		},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			var de domain.DomainError
			if err := tc.run(); !errors.As(err, &de) || de.Code != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
	assertEventAuditCount(t, ctx, migrator, domain.ActionCaptureCreditCapChanged, fixture.eventID.String(), 0)
	assertEventActionTotal(t, ctx, migrator, domain.ActionCaptureCreditReset, 0)
}

type captureCreditAdminFixture struct {
	adminID      uuid.UUID
	eventID      uuid.UUID
	coordinatorA uuid.UUID
	coordinatorB uuid.UUID
}

func seedCaptureCreditAdminFixture(t *testing.T, ctx context.Context, migrator *pgxpool.Pool) captureCreditAdminFixture {
	t.Helper()
	fixture := captureCreditAdminFixture{
		adminID:      seedAdmin(t, ctx, migrator, "credit-admin@amrita.edu"),
		eventID:      seedActiveEvent(t, ctx, migrator, "Credit Admin Event"),
		coordinatorA: seedUser(t, ctx, migrator, "coord-a@amrita.edu", "coordinator"),
		coordinatorB: seedUser(t, ctx, migrator, "coord-b@amrita.edu", "coordinator"),
	}
	insertCoordinatorAssignment(t, ctx, migrator, fixture.eventID, fixture.coordinatorA, fixture.adminID)
	insertCoordinatorAssignment(t, ctx, migrator, fixture.eventID, fixture.coordinatorB, fixture.adminID)
	return fixture
}

func eventAdminService(runtime *pgxpool.Pool) *eventsvc.Service {
	return eventsvc.NewService(eventsvc.NewSQLCReader(runtime), store.New(runtime), eventsvc.NewSQLCWriter())
}

func adminActor(fixture captureCreditAdminFixture) eventsvc.Actor {
	return eventsvc.Actor{UserID: fixture.adminID.String(), Email: "credit-admin@amrita.edu", Role: "admin"}
}

func insertCoordinatorAssignment(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, eventID, userID, adminID uuid.UUID) {
	t.Helper()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ($1, $2, $3)`, userID, eventID, adminID); err != nil {
		t.Fatalf("seed coordinator assignment: %v", err)
	}
}

func assertEventAuditCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, action domain.Action, targetID string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE action = $1 AND target_id = $2`, string(action), targetID).Scan(&got); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	if got != want {
		t.Fatalf("[P0] audit rows for %s target %s = %d, want %d", action, targetID, got, want)
	}
}

func assertEventActionTotal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, action domain.Action, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = $1`, string(action)).Scan(&got); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	if got != want {
		t.Fatalf("[P0] audit rows for %s = %d, want %d", action, got, want)
	}
}
