//go:build integration

package odrecordtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestCaptureCreditsMigrationAndPrivileges(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))

	for _, table := range []string{"event_capture_credit_config", "event_capture_credit_usage"} {
		for privilege, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": false} {
			var got bool
			if err := migrator.QueryRow(ctx, `SELECT has_table_privilege('od_runtime', $1, $2)`, table, privilege).Scan(&got); err != nil {
				t.Fatalf("check %s privilege on %s: %v", privilege, table, err)
			}
			if got != want {
				t.Errorf("[P0] od_runtime %s on %s = %v, want %v", privilege, table, got, want)
			}
		}
	}
}

func TestCaptureCreditAtomicDebitConcurrentRemainingOne(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	secondStudent := seedMarkStudent(t, ctx, migrator, fixture)
	setCaptureCreditLimit(t, ctx, migrator, fixture, 1)

	svc := markService(runtime, fixture)
	actor := odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"}
	inputs := []odsvc.MarkInput{
		{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: odsvc.CaptureMethodCamera, StudentID: &fixture.studentID},
		{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: odsvc.CaptureMethodSearch, StudentID: &secondStudent},
	}

	errs := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func(input odsvc.MarkInput) {
			defer wg.Done()
			_, err := svc.Mark(ctx, actor, input)
			errs <- err
		}(input)
	}
	wg.Wait()
	close(errs)

	var successes, exhausted int
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if isDomainCode(err, contracts.ErrorCodeCAPTURECREDITLIMITREACHED) {
			exhausted++
			continue
		}
		t.Fatalf("unexpected concurrent mark error: %v", err)
	}
	if successes != 1 || exhausted != 1 {
		t.Fatalf("[P0] concurrent last-credit result successes=%d exhausted=%d, want 1/1", successes, exhausted)
	}
	if got := creditConsumed(t, ctx, migrator, fixture); got != 1 {
		t.Fatalf("[P0] consumed = %d, want 1 after two concurrent attempts at remaining 1", got)
	}
	assertAuditCount(t, ctx, migrator, fixture.eventID, domain.ActionODCreated, 1)
	assertAuditCount(t, ctx, migrator, fixture.eventID, domain.ActionScanBlocked, 1)
}

func TestCaptureCreditDebitUsesIndependentCoordinatorLimits(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	coordinatorB := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'coordinator-b@amrita.edu', 'ATDD Coordinator B', 'coordinator')`,
		coordinatorB, "atdd-coordinator-b-"+coordinatorB); err != nil {
		t.Fatalf("seed second coordinator: %v", err)
	}
	if _, err := migrator.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id, fallback_credit_limit)
		VALUES ($1, $2, $3, 20)`, coordinatorB, fixture.eventID, fixture.adminID); err != nil {
		t.Fatalf("seed second coordinator assignment: %v", err)
	}
	setCaptureCreditLimit(t, ctx, migrator, fixture, 5)

	svc := markService(runtime, fixture)
	actorA := odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"}
	for range 5 {
		studentID := seedMarkStudent(t, ctx, migrator, fixture)
		if _, err := svc.Mark(ctx, actorA, odsvc.MarkInput{
			EventID:       fixture.eventID,
			CategoryID:    fixture.categoryID,
			CaptureMethod: odsvc.CaptureMethodSearch,
			StudentID:     &studentID,
		}); err != nil {
			t.Fatalf("coordinator A mark within limit: %v", err)
		}
	}
	overflowStudent := seedMarkStudent(t, ctx, migrator, fixture)
	if _, err := svc.Mark(ctx, actorA, odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodSearch,
		StudentID:     &overflowStudent,
	}); !isDomainCode(err, contracts.ErrorCodeCAPTURECREDITLIMITREACHED) {
		t.Fatalf("coordinator A overflow = %v, want CAPTURE_CREDIT_LIMIT_REACHED", err)
	}

	studentB := seedMarkStudent(t, ctx, migrator, fixture)
	if _, err := svc.Mark(ctx, odsvc.Actor{UserID: coordinatorB, Email: "coordinator-b@amrita.edu", Role: "coordinator"}, odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodSearch,
		StudentID:     &studentB,
	}); err != nil {
		t.Fatalf("[P0] coordinator B mark after A exhausted = %v, want nil because B has independent limit 20", err)
	}
	if got := creditConsumed(t, ctx, migrator, fixture); got != 5 {
		t.Fatalf("[P0] coordinator A consumed = %d, want 5", got)
	}
	if got := creditConsumedForUser(t, ctx, migrator, fixture.eventID, coordinatorB); got != 1 {
		t.Fatalf("[P0] coordinator B consumed = %d, want 1", got)
	}
}

func TestCaptureCreditRollbackDuplicateDoesNotBurnCredit(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	setCaptureCreditLimit(t, ctx, migrator, fixture, 5)

	svc := markService(runtime, fixture)
	actor := odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"}
	input := odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodSearch,
		StudentID:     &fixture.studentID,
	}

	if _, err := svc.Mark(ctx, actor, input); err != nil {
		t.Fatalf("first search mark = %v, want nil", err)
	}
	if _, err := svc.Mark(ctx, actor, input); !isDomainCode(err, contracts.ErrorCodeSCANDUPLICATE) {
		t.Fatalf("duplicate search mark = %v, want SCAN_DUPLICATE", err)
	}
	if got := creditConsumed(t, ctx, migrator, fixture); got != 1 {
		t.Fatalf("[P0] consumed = %d, want 1; duplicate rollback must not burn a second credit", got)
	}
}

func TestCaptureCreditNFCIgnoresExhaustedPool(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)
	setCaptureCreditLimit(t, ctx, migrator, fixture, 0)

	result, err := markService(runtime, fixture).Mark(ctx, odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"}, odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodNFC,
		Card:          &odsvc.CardInput{Version: "v1", Token: fixture.cardToken},
	})
	if err != nil {
		t.Fatalf("[P0] nfc mark with exhausted fallback pool = %v, want nil", err)
	}
	if result.CaptureMethod != odsvc.CaptureMethodNFC {
		t.Errorf("[P0] result capture_method = %q, want nfc", result.CaptureMethod)
	}
	if got := creditConsumed(t, ctx, migrator, fixture); got != 0 {
		t.Fatalf("[P0] consumed = %d, want 0; NFC must not use fallback credit", got)
	}
}

func TestCaptureCreditStatusCapLoweredBelowConsumedExhaustsWithoutWriting(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	setCaptureCreditLimit(t, ctx, migrator, fixture, 5)
	for range 4 {
		studentID := seedMarkStudent(t, ctx, migrator, fixture)
		if _, err := markService(runtime, fixture).Mark(ctx,
			odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"},
			odsvc.MarkInput{
				EventID:       fixture.eventID,
				CategoryID:    fixture.categoryID,
				CaptureMethod: odsvc.CaptureMethodSearch,
				StudentID:     &studentID,
			}); err != nil {
			t.Fatalf("seed consumed credit with mark: %v", err)
		}
	}
	setCaptureCreditLimit(t, ctx, migrator, fixture, 3)

	status, err := odsvc.NewStatusService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewSQLCCounter(runtime),
		odsvc.NewSQLCCreditLedger(runtime),
		fixedClock{now: fixture.slotOneTime},
	).MarkStatus(ctx, odsvc.Actor{UserID: fixture.coordinatorID, Role: "coordinator"}, fixture.eventID, fixture.categoryID)
	if err != nil {
		t.Fatalf("MarkStatus = %v, want nil", err)
	}
	if status.FallbackCredit.Limit != 3 ||
		status.FallbackCredit.Consumed != 4 ||
		status.FallbackCredit.Remaining != 0 ||
		!status.FallbackCredit.Exhausted {
		t.Fatalf("[P0] fallback credit = %+v, want limit 3 consumed 4 remaining 0 exhausted", status.FallbackCredit)
	}
}

func markService(runtime *pgxpool.Pool, fixture markFixture) *odsvc.Service {
	return odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: fixture.slotOneTime},
		odsvc.AllowAnyScope{},
		odsvc.NewSQLCCreditLedger(runtime),
	)
}

func setCaptureCreditLimit(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, fixture markFixture, limit int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		UPDATE event_coordinator_assignments
		SET fallback_credit_limit = $3
		WHERE event_id = $1 AND user_id = $2`,
		fixture.eventID, fixture.coordinatorID, limit); err != nil {
		t.Fatalf("set credit limit: %v", err)
	}
}

func seedMarkStudent(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, fixture markFixture) string {
	t.Helper()
	studentID := uuid.NewString()
	rollSuffix := strings.ToUpper(strings.ReplaceAll(studentID, "-", ""))[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1, $2, 'ATDD Credit Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')`,
		studentID, "CB.SC.U4ODR"+rollSuffix); err != nil {
		t.Fatalf("seed credit student: %v", err)
	}
	return studentID
}

func creditConsumed(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, fixture markFixture) int {
	t.Helper()
	return creditConsumedForUser(t, ctx, pool, fixture.eventID, fixture.coordinatorID)
}

func creditConsumedForUser(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, eventID, userID string) int {
	t.Helper()
	var consumed int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(consumed, 0)
		FROM event_capture_credit_usage
		WHERE event_id = $1 AND user_id = $2`, eventID, userID).Scan(&consumed); errors.Is(err, pgx.ErrNoRows) {
		return 0
	} else if err != nil {
		t.Fatalf("read credit usage: %v", err)
	}
	return consumed
}

func assertAuditCount(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, eventID string, action domain.Action, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE action = $1 AND payload->>'event_id' = $2`, string(action), eventID).Scan(&got); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	if got != want {
		t.Fatalf("[P0] audit rows for %s = %d, want %d", action, got, want)
	}
}

func isDomainCode(err error, code contracts.ErrorCode) bool {
	var de domain.DomainError
	return errors.As(err, &de) && de.Code == code
}
