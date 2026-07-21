//go:build integration

package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestEventSchema_NormalizedDateOnlyAndConstrained(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	for _, table := range []string{"branches", "events", "event_branches"} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Errorf("[P0] expected %s table", table)
		}
	}

	var eventDateType string
	if err := pool.QueryRow(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'events' AND column_name = 'event_date'`).Scan(&eventDateType); err != nil {
		t.Fatalf("read events.event_date type: %v", err)
	}
	if eventDateType != "date" {
		t.Errorf("[P0] events.event_date type = %q, want date", eventDateType)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO events (name, event_date, status)
		VALUES ('invalid lifecycle', DATE '2026-08-15', 'deleted')`)
	if !hasSQLState(err, "23514") {
		t.Errorf("[P0] invalid event lifecycle should fail CHECK 23514, got %v", err)
	}
}

func TestEventSchema_RuntimeCannotHardDelete(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	var eventID, branchID string
	if err := migrator.QueryRow(ctx, `
		INSERT INTO branches (code, name)
		VALUES ('DELETE-GATE-' || substr(gen_random_uuid()::text, 1, 8), 'Delete gate fixture')
		RETURNING id::text`).Scan(&branchID); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	if err := migrator.QueryRow(ctx, `
		INSERT INTO events (name, event_date)
		VALUES ('Delete gate fixture', DATE '2026-08-15')
		RETURNING id::text`).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := migrator.Exec(ctx,
		`INSERT INTO event_branches (event_id, branch_id) VALUES ($1::uuid, $2::uuid)`, eventID, branchID); err != nil {
		t.Fatalf("seed association: %v", err)
	}
	t.Cleanup(func() {
		_, _ = migrator.Exec(context.Background(), `DELETE FROM event_branches WHERE event_id = $1::uuid`, eventID)
		_, _ = migrator.Exec(context.Background(), `DELETE FROM events WHERE id = $1::uuid`, eventID)
		_, _ = migrator.Exec(context.Background(), `DELETE FROM branches WHERE id = $1::uuid`, branchID)
	})

	if _, err := runtime.Exec(ctx, `DELETE FROM event_branches WHERE event_id = $1::uuid`, eventID); !isPermissionDenied(err) {
		t.Errorf("[P0] runtime event-branch delete should be denied with 42501, got %v", err)
	}
	if _, err := runtime.Exec(ctx, `DELETE FROM events WHERE id = $1::uuid`, eventID); !isPermissionDenied(err) {
		t.Errorf("[P0] runtime event delete should be denied with 42501, got %v", err)
	}
}

func TestEventSchema_ArchivedAggregateIsImmutable(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	var actorID, eventID, firstBranchID, secondBranchID string
	if err := migrator.QueryRow(ctx, `
		INSERT INTO users (supabase_subject, email, role)
		VALUES ('event-schema-' || gen_random_uuid()::text, 'event-schema@amrita.edu', 'admin')
		RETURNING id::text`).Scan(&actorID); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	for target, code := range map[*string]string{&firstBranchID: "ARCHIVE-A", &secondBranchID: "ARCHIVE-B"} {
		if err := migrator.QueryRow(ctx, `
			INSERT INTO branches (code, name)
			VALUES ($1 || '-' || substr(gen_random_uuid()::text, 1, 8), $1 || ' fixture')
			RETURNING id::text`, code).Scan(target); err != nil {
			t.Fatalf("seed branch: %v", err)
		}
	}
	if err := migrator.QueryRow(ctx, `
		INSERT INTO events (name, event_date)
		VALUES ('Archive immutability fixture', DATE '2026-08-16')
		RETURNING id::text`).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := migrator.Exec(ctx,
		`INSERT INTO event_branches (event_id, branch_id) VALUES ($1::uuid, $2::uuid)`, eventID, firstBranchID); err != nil {
		t.Fatalf("seed association: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = migrator.Exec(cleanupCtx, `DELETE FROM event_branches WHERE event_id = $1::uuid`, eventID)
		_, _ = migrator.Exec(cleanupCtx, `DELETE FROM events WHERE id = $1::uuid`, eventID)
		_, _ = migrator.Exec(cleanupCtx, `DELETE FROM branches WHERE id IN ($1::uuid, $2::uuid)`, firstBranchID, secondBranchID)
		_, _ = migrator.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, actorID)
	})

	if _, err := runtime.Exec(ctx, `
		UPDATE events
		SET status = 'archived', archived_at = now(), archived_by_user_id = $2::uuid
		WHERE id = $1::uuid`, eventID, actorID); err != nil {
		t.Fatalf("archive transition: %v", err)
	}

	if _, err := runtime.Exec(ctx, `UPDATE events SET name = 'mutated' WHERE id = $1::uuid`, eventID); !hasSQLState(err, "55000") {
		t.Errorf("[P0] archived event update should fail 55000, got %v", err)
	}
	if _, err := runtime.Exec(ctx, `
		UPDATE event_branches SET branch_id = $2::uuid
		WHERE event_id = $1::uuid`, eventID, secondBranchID); !hasSQLState(err, "55000") {
		t.Errorf("[P0] archived branch mutation should fail 55000, got %v", err)
	}
}

func hasSQLState(err error, state string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == state
}
