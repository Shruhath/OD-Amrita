//go:build integration

package db

import "testing"

// AC 1: goose up creates the audit_log table + base extensions cleanly.
//
// Assumes CI has already run `goose up` before invoking the integration suite
// (Task 8 wires: goose up → test → goose down). Asserts the resulting schema.
func TestMigrationsUp_CreatesAuditLog(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'audit_log'
		)`).Scan(&exists); err != nil {
		t.Fatalf("check audit_log existence: %v", err)
	}
	if !exists {
		t.Fatal("[P0] expected audit_log table to exist after goose up")
	}

	// The infallible-insert contract (Dev Notes) requires at minimum a server-set
	// id and a created_at default now(); assert the load-bearing columns exist.
	for _, col := range []string{"id", "action", "created_at"} {
		var colExists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'audit_log' AND column_name = $1
			)`, col).Scan(&colExists); err != nil {
			t.Fatalf("check column %q: %v", col, err)
		}
		if !colExists {
			t.Errorf("[P0] expected audit_log.%s to exist", col)
		}
	}

	// created_at must default to now() so a legitimate audit write can never trip
	// a NULL constraint.
	var def *string
	if err := pool.QueryRow(ctx, `
		SELECT column_default FROM information_schema.columns
		WHERE table_name = 'audit_log' AND column_name = 'created_at'`).Scan(&def); err != nil {
		t.Fatalf("read created_at default: %v", err)
	}
	if def == nil || *def == "" {
		t.Error("[P1] expected audit_log.created_at to have a default (now())")
	}
}

// AC 1: goose down → up leaves an identical schema (reversible + idempotent).
//
// Drives goose directly (down then up) and compares the audit_log column
// signature captured on either side of the cycle.
func TestMigrationsRoundTrip_Idempotent(t *testing.T) {
	requireGoose(t)
	ctx := testCtx(t)
	migratorDSN := dsnOrSkip(t, envMigratorDSN)
	pool := connect(t, ctx, migratorDSN)

	before := auditLogColumns(t, ctx, pool)
	if len(before) == 0 {
		t.Fatal("[P0] audit_log has no columns before round-trip (was goose up run?)")
	}

	// down must remove the table cleanly.
	runGoose(t, migratorDSN, "down")
	var stillThere bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'audit_log'
		)`).Scan(&stillThere); err != nil {
		t.Fatalf("check audit_log after down: %v", err)
	}
	if stillThere {
		t.Error("[P0] expected audit_log to be dropped after goose down")
	}

	// up must recreate an identical schema.
	runGoose(t, migratorDSN, "up")
	after := auditLogColumns(t, ctx, pool)

	if len(before) != len(after) {
		t.Fatalf("[P0] column count changed across round-trip: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("[P0] column signature drift at %d: before=%q after=%q", i, before[i], after[i])
		}
	}
}
