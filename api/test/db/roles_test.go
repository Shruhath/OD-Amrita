//go:build integration

package db

import "testing"

// AC 2: both login roles exist — od_migrator (DDL/owner) and od_runtime (DML).
func TestDualRoles_Exist(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	for _, role := range []string{"od_migrator", "od_runtime"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = $1)`,
			role,
		).Scan(&exists); err != nil {
			t.Fatalf("check role %q: %v", role, err)
		}
		if !exists {
			t.Errorf("[P0] expected login role %q to exist", role)
		}
	}
}

// AC 2: od_migrator owns audit_log; od_runtime does NOT own it. Ownership is the
// escape hatch that would let a role DDL its own objects — the runtime role must
// own nothing so AC 3 holds and the 1.4 audit trigger cannot be dropped.
func TestOwnership_MigratorOwns_RuntimeOwnsNothing(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	var owner string
	if err := pool.QueryRow(ctx,
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE relname = 'audit_log'`,
	).Scan(&owner); err != nil {
		t.Fatalf("read audit_log owner: %v", err)
	}
	if owner != "od_migrator" {
		t.Errorf("[P0] expected audit_log owner=od_migrator, got %q", owner)
	}
	if owner == "od_runtime" {
		t.Error("[P0] od_runtime must not own audit_log")
	}
}

// AC 2 (Story 1.4 revoke): audit_log is APPEND-ONLY, so od_runtime holds only
// SELECT + INSERT — the app only ever appends and reads audit rows. UPDATE and
// DELETE are REVOKED by migration 0002 (least-privilege first line; the trigger
// is the below-role backstop). od_runtime still has NO CREATE on the schema, so
// it cannot introduce DDL.
//
// NOTE (Story 1.4): before 0002 this asserted all four DML privileges present.
// The revoke flips UPDATE/DELETE to "must be absent" — this test goes RED until
// 0002 lands, which is the intended red signal for the hardening.
func TestRuntimeGrants_DMLYesCreateNo(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	// Present: the append + read privileges the app legitimately needs.
	for _, priv := range []string{"SELECT", "INSERT"} {
		var granted bool
		if err := pool.QueryRow(ctx,
			`SELECT has_table_privilege('od_runtime', 'public.audit_log', $1)`,
			priv,
		).Scan(&granted); err != nil {
			t.Fatalf("check %s grant: %v", priv, err)
		}
		if !granted {
			t.Errorf("[P1] expected od_runtime to have %s on audit_log", priv)
		}
	}

	// Absent: mutating an append-only ledger is not the app's job (revoked by 0002).
	for _, priv := range []string{"UPDATE", "DELETE"} {
		var granted bool
		if err := pool.QueryRow(ctx,
			`SELECT has_table_privilege('od_runtime', 'public.audit_log', $1)`,
			priv,
		).Scan(&granted); err != nil {
			t.Fatalf("check %s grant: %v", priv, err)
		}
		if granted {
			t.Errorf("[P0] expected od_runtime to NOT have %s on audit_log (revoked by 0002 — audit_log is append-only)", priv)
		}
	}

	var canCreate bool
	if err := pool.QueryRow(ctx,
		`SELECT has_schema_privilege('od_runtime', 'public', 'CREATE')`,
	).Scan(&canCreate); err != nil {
		t.Fatalf("check schema CREATE: %v", err)
	}
	if canCreate {
		t.Error("[P0] od_runtime must NOT have CREATE on the public schema")
	}
}

// AC 3 (gate): connecting AS od_runtime, CREATE TABLE is denied with SQLSTATE
// 42501. Asserts on the typed pgconn error, never a message substring.
func TestRuntimeRole_CreateTableDenied(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	_, err := pool.Exec(ctx,
		`CREATE TABLE t_probe (id uuid PRIMARY KEY, created_at timestamptz DEFAULT now())`)
	if err == nil {
		// Should never happen; if it did, don't leave the probe table behind.
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS t_probe`)
		t.Fatal("[P0] expected CREATE TABLE to be denied for od_runtime, but it succeeded")
	}
	if !isPermissionDenied(err) {
		t.Errorf("[P0] expected permission denied (SQLSTATE %s), got: %v", sqlstatePermissionDenied, err)
	}
}

// AC 3 (gate): connecting AS od_runtime, ALTER TABLE audit_log is denied with
// SQLSTATE 42501.
func TestRuntimeRole_AlterTableDenied(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	_, err := pool.Exec(ctx, `ALTER TABLE audit_log ADD COLUMN probe_col text`)
	if err == nil {
		// Undo the unexpected mutation so the suite stays deterministic.
		_, _ = pool.Exec(ctx, `ALTER TABLE audit_log DROP COLUMN IF EXISTS probe_col`)
		t.Fatal("[P0] expected ALTER TABLE audit_log to be denied for od_runtime, but it succeeded")
	}
	if !isPermissionDenied(err) {
		t.Errorf("[P0] expected permission denied (SQLSTATE %s), got: %v", sqlstatePermissionDenied, err)
	}
}
