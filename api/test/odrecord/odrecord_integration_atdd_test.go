//go:build integration

package odrecordtest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestODRecordsSchemaRuntimePrivilegesAndInvariants(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))

	columns := map[string]string{}
	rows, err := migrator.Query(ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'od_records'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("read od_records columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = typ
	}
	for name, typ := range map[string]string{
		"id":                 "uuid",
		"student_id":         "uuid",
		"event_id":           "uuid",
		"category_id":        "uuid",
		"slot":               "integer",
		"status":             "text",
		"capture_method":     "text",
		"actor_user_id":      "uuid",
		"actor_role":         "text",
		"server_received_at": "timestamp with time zone",
		"created_at":         "timestamp with time zone",
	} {
		if columns[name] != typ {
			t.Errorf("[P0] od_records.%s type=%q, want %q", name, columns[name], typ)
		}
	}
	var actorRoleCheck bool
	if err := migrator.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_constraint
				WHERE conrelid = 'od_records'::regclass
				  AND contype = 'c'
				  AND pg_get_constraintdef(oid) LIKE '%actor_role%'
				  AND pg_get_constraintdef(oid) LIKE '%coordinator%'
				  AND pg_get_constraintdef(oid) LIKE '%teacher%'
				  AND pg_get_constraintdef(oid) LIKE '%admin%'
			)`).Scan(&actorRoleCheck); err != nil {
		t.Fatalf("check actor_role constraint: %v", err)
	}
	if !actorRoleCheck {
		t.Fatal("[P1] od_records.actor_role must be constrained to roles that can create marks")
	}
	for privilege, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": false} {
		var got bool
		if err := migrator.QueryRow(ctx, `SELECT has_table_privilege('od_runtime', 'od_records', $1)`, privilege).Scan(&got); err != nil {
			t.Fatalf("check %s privilege: %v", privilege, err)
		}
		if got != want {
			t.Errorf("[P0] od_runtime %s on od_records = %v, want %v", privilege, got, want)
		}
	}

	var hasDuplicateIndex bool
	if err := migrator.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_indexes
			WHERE schemaname = 'public'
			  AND tablename = 'od_records'
			  AND indexname = 'uq_od_records_present_student_event_slot'
			  AND indexdef LIKE '%UNIQUE%'
			  AND indexdef LIKE '%student_id, event_id, slot%'
			  AND indexdef LIKE '%status = ''present''%'
		)`).Scan(&hasDuplicateIndex); err != nil {
		t.Fatalf("check partial duplicate index: %v", err)
	}
	if !hasDuplicateIndex {
		t.Fatal("[P0] od_records needs a partial unique index on present student/event/slot")
	}
}

func TestODRecordServiceDuplicateRaceAuditAndBlockedSlot(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	svc := odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: fixture.slotOneTime},
		nil,
	)
	actor := odsvc.Actor{UserID: fixture.coordinatorID, Email: "coordinator@amrita.edu", Role: "coordinator"}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Mark(ctx, actor, odsvc.MarkInput{
				EventID:       fixture.eventID,
				CategoryID:    fixture.categoryID,
				CaptureMethod: odsvc.CaptureMethodNFC,
				Card:          &odsvc.CardInput{Version: "v1", Token: fixture.cardToken},
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
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM od_records WHERE student_id = $1 AND event_id = $2 AND slot = 1`, fixture.studentID, fixture.eventID).Scan(&odRows); err != nil {
		t.Fatalf("count od rows: %v", err)
	}
	// Scoped to THIS fixture's event. A table-wide count would be contaminated by
	// every other test in this package that writes an od.created row — which is a
	// property of the test, not of the code under test.
	if err := migrator.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE action = $1 AND target_table = 'od_records' AND payload->>'event_id' = $2
	`, string(domain.ActionODCreated), fixture.eventID).Scan(&auditRows); err != nil {
		t.Fatalf("count od.created audit rows: %v", err)
	}
	if odRows != 1 || auditRows != 1 {
		t.Fatalf("[P0] od/audit conservation = %d/%d, want exactly one each", odRows, auditRows)
	}

	closedSvc := odsvc.NewService(
		odsvc.NewSQLCReader(runtime),
		odsvc.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtime), fixture.cardKeyring),
		odsvc.NewSQLCWriter(),
		store.New(runtime),
		fixedClock{now: fixture.breakTime},
		nil,
	)
	_, err := closedSvc.Mark(ctx, actor, odsvc.MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: odsvc.CaptureMethodNFC,
		Card:          &odsvc.CardInput{Version: "v1", Token: fixture.cardToken},
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeSLOTCLOSED {
		t.Fatalf("closed slot error = %v, want SLOT_CLOSED", err)
	}
	if err := migrator.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE action = $1 AND payload->>'event_id' = $2
	`, string(domain.ActionScanBlocked), fixture.eventID).Scan(&auditRows); err != nil {
		t.Fatalf("count scan.blocked audit rows: %v", err)
	}
	if auditRows == 0 {
		t.Fatal("[P0] slot-closed rejection must write scan.blocked audit")
	}
}

type markFixture struct {
	eventID       string
	categoryID    string
	studentID     string
	coordinatorID string
	cardToken     string
	cardKeyring   svccardtoken.Keyring
	slotOneTime   time.Time
	breakTime     time.Time
}

func seedMarkFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) markFixture {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load campus location: %v", err)
	}
	eventDate := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	cardKey := bytes.Repeat([]byte{0x33}, 32)
	cardKeyring, err := svccardtoken.NewKeyring("v1", map[string][]byte{"v1": cardKey})
	if err != nil {
		t.Fatalf("create card keyring: %v", err)
	}
	fixture := markFixture{
		eventID:       uuid.NewString(),
		categoryID:    uuid.NewString(),
		studentID:     uuid.NewString(),
		coordinatorID: uuid.NewString(),
		cardKeyring:   cardKeyring,
		slotOneTime:   time.Date(2026, 8, 3, 8, 25, 0, 0, loc),
		breakTime:     time.Date(2026, 8, 3, 10, 35, 0, 0, loc),
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO branches (id, code, name)
		VALUES ('91000000-0000-4000-8000-000000000001', 'ODR', 'OD Record Fixture Branch')
		ON CONFLICT (id) DO UPDATE SET code = EXCLUDED.code, name = EXCLUDED.name`); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'coordinator@amrita.edu', 'ATDD Coordinator', 'coordinator')
		ON CONFLICT (supabase_subject) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role`,
		fixture.coordinatorID, "atdd-coordinator-"+fixture.coordinatorID); err != nil {
		t.Fatalf("seed coordinator: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (id, name, event_date, status)
		VALUES ($1, 'ATDD Mark Event', $2, 'active')`, fixture.eventID, eventDate); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO categories (id, event_id, name)
		VALUES ($1, $2, 'ATDD Category')`, fixture.categoryID, fixture.eventID); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	rollSuffix := strings.ToUpper(strings.ReplaceAll(fixture.studentID, "-", ""))[:8]
	rollNo := "CB.SC.U4ODR" + rollSuffix
	if _, err := pool.Exec(ctx, `
			INSERT INTO students (id, roll_no, name, branch_id, section, batch)
			VALUES ($1, $2, 'ATDD Mark Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')`,
		fixture.studentID, rollNo); err != nil {
		t.Fatalf("seed student: %v", err)
	}
	mac := hmac.New(sha256.New, cardKey)
	_, _ = mac.Write([]byte(rollNo))
	fixture.cardToken = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	hash := sha256.Sum256([]byte(fixture.cardToken))
	if _, err := pool.Exec(ctx, `
		INSERT INTO student_card_tokens (student_id, version, token_sha256)
		VALUES ($1, 'v1', $2)`,
		fixture.studentID, hash[:]); err != nil {
		t.Fatalf("seed card token using hash key marker %x: %v", hash, err)
	}
	return fixture
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dsnOrSkip(t *testing.T, env string) string {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s not set; skipping database integration test", env)
	}
	return dsn
}

func connect(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
