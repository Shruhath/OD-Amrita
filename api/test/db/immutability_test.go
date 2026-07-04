//go:build integration

package db

// immutability_test.go — RED-PHASE ATDD scaffold for Story 1.4, AC 1 & AC 2
// (audit_log immutability trigger + runtime-role append-only hardening).
//
// INTEGRATION tests: `//go:build integration` + DSN-gated (dsnOrSkip), so the
// no-DB developer path (`go test ./...`) skips them cleanly. They reuse the
// Story 1.3 harness (harness_test.go): connect, testCtx, dsnOrSkip, runGoose,
// requireGoose, isPermissionDenied, sqlstatePermissionDenied, and the env consts.
//
// RED PHASE (behavioral): these COMPILE today (pgx is already a dependency) but
// FAIL against a database that has not yet applied migration 0002 — the owner
// UPDATE/DELETE would succeed (no trigger), and the runtime DDL would not yet be
// distinguishable. They turn green once 0002 (trigger + revoke) lands and CI
// runs `goose up`.
//
// Priority tags: [P0] critical gate · [P1] important · [P2] guard.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sqlstateTriggerRaise is the SQLSTATE a bare `RAISE EXCEPTION` (no ERRCODE)
// yields — exactly what audit_log_immutable() raises (0002). Asserting on it (not
// merely "not 42501") proves the immutability TRIGGER is the cause, ruling out any
// incidental non-privilege PgError.
const sqlstateTriggerRaise = "P0001"

// pgErrorCode extracts the Postgres SQLSTATE from err, reporting whether err was
// a *pgconn.PgError at all. Assert on the typed error/SQLSTATE, never a message
// substring — the AC-1 gate depends on distinguishing the trigger's raise from a
// privilege error, and messages are not a stable contract.
func pgErrorCode(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code, true
	}
	return "", false
}

// insertOneAuditRow inserts a single audit row AS THE CONNECTED ROLE and returns
// its id. Used to give the UPDATE/DELETE immutability tests a real target row.
// `action` is free text at the DB layer (registry validation is enforced in Go,
// not by a CHECK), so any value is fine here.
func insertOneAuditRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO audit_log (action) VALUES ('od.created') RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("seed audit row: %v", err)
	}
	return id
}

// TestAuditLog_OwnerUpdateRaises is the NFR19 gate. Connected AS od_migrator —
// the OWNER, the most-privileged non-superuser — an UPDATE on audit_log must
// still be blocked by the trigger's RAISE EXCEPTION. Critically the error must
// NOT be 42501 (permission denied): if it were, immutability would be resting on
// a grant (which a service/superuser role bypasses), not on the trigger. A
// non-privilege PgError (the trigger's P0001 or a custom errcode) is the proof
// that immutability is enforced BELOW role privilege.
func TestAuditLog_OwnerUpdateRaises(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	id := insertOneAuditRow(t, ctx, pool)

	_, err := pool.Exec(ctx, `UPDATE audit_log SET action = 'tampered' WHERE id = $1`, id)
	if err == nil {
		t.Fatal("[P0] expected UPDATE audit_log to be blocked by the immutability trigger, but it succeeded")
	}
	code, ok := pgErrorCode(err)
	if !ok {
		t.Fatalf("[P0] expected a typed *pgconn.PgError from the immutability trigger, got %T: %v", err, err)
	}
	if code == sqlstatePermissionDenied {
		t.Errorf("[P0] UPDATE was blocked by PRIVILEGE (42501), not the trigger — immutability must hold even for the owner (NFR19). The trigger must RAISE for every role.")
	}
	if code != sqlstateTriggerRaise {
		t.Errorf("[P0] expected the immutability trigger raise (SQLSTATE %s), got %s — the block must come from the trigger, not an incidental error", sqlstateTriggerRaise, code)
	}
}

// TestAuditLog_OwnerDeleteRaises — same as above for DELETE. Owner DELETE must be
// blocked by the trigger (non-42501 PgError), not by a grant.
func TestAuditLog_OwnerDeleteRaises(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	id := insertOneAuditRow(t, ctx, pool)

	_, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, id)
	if err == nil {
		t.Fatal("[P0] expected DELETE audit_log to be blocked by the immutability trigger, but it succeeded")
	}
	code, ok := pgErrorCode(err)
	if !ok {
		t.Fatalf("[P0] expected a typed *pgconn.PgError from the immutability trigger, got %T: %v", err, err)
	}
	if code == sqlstatePermissionDenied {
		t.Errorf("[P0] DELETE was blocked by PRIVILEGE (42501), not the trigger — immutability must hold even for the owner (NFR19).")
	}
	if code != sqlstateTriggerRaise {
		t.Errorf("[P0] expected the immutability trigger raise (SQLSTATE %s), got %s — the block must come from the trigger, not an incidental error", sqlstateTriggerRaise, code)
	}
}

// TestAuditLog_OwnerTruncateRaises — AC 1 defense-in-depth. The BEFORE TRUNCATE
// statement trigger must block TRUNCATE even for the OWNER (od_migrator, who DOES
// hold TRUNCATE), proving the guard fires BELOW role privilege — not merely that
// the runtime role lacks the grant. Row UPDATE/DELETE triggers do NOT fire on
// TRUNCATE, so this is the only proof the dedicated audit_log_no_truncate trigger
// actually fires. The error must be the trigger's raise (P0001), not 42501.
func TestAuditLog_OwnerTruncateRaises(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	_, err := pool.Exec(ctx, `TRUNCATE audit_log`)
	if err == nil {
		t.Fatal("[P0] expected TRUNCATE audit_log to be blocked by the BEFORE TRUNCATE trigger even for the owner, but it succeeded")
	}
	code, ok := pgErrorCode(err)
	if !ok {
		t.Fatalf("[P0] expected a typed *pgconn.PgError from the TRUNCATE trigger, got %T: %v", err, err)
	}
	if code == sqlstatePermissionDenied {
		t.Errorf("[P0] owner TRUNCATE was blocked by PRIVILEGE (42501), not the trigger — the BEFORE TRUNCATE guard must RAISE below role level (the owner holds TRUNCATE)")
	}
	if code != sqlstateTriggerRaise {
		t.Errorf("[P0] expected the TRUNCATE trigger raise (SQLSTATE %s), got %s", sqlstateTriggerRaise, code)
	}
}

// TestAuditLog_RuntimeTruncateDenied — AC 2. Connected AS od_runtime, TRUNCATE
// audit_log must fail with 42501: the runtime role was never granted TRUNCATE and
// owns nothing, so a compromised runtime credential cannot wipe the ledger.
func TestAuditLog_RuntimeTruncateDenied(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	_, err := pool.Exec(ctx, `TRUNCATE audit_log`)
	if err == nil {
		t.Fatal("[P0] expected TRUNCATE audit_log to be denied for od_runtime, but it succeeded")
	}
	if !isPermissionDenied(err) {
		t.Errorf("[P0] expected permission denied (SQLSTATE %s) for runtime TRUNCATE, got: %v", sqlstatePermissionDenied, err)
	}
}

// TestAuditLog_RuntimeAlterAddColumnDenied — AC 2. Connected AS od_runtime, a
// plain ALTER TABLE … ADD COLUMN on audit_log must fail with 42501: ALTER requires
// table ownership, which od_runtime does not have (owns nothing, no schema CREATE).
// Completes the AC-2 triad — TRUNCATE / ALTER / DISABLE TRIGGER (Task 7(b)).
func TestAuditLog_RuntimeAlterAddColumnDenied(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	_, err := pool.Exec(ctx, `ALTER TABLE audit_log ADD COLUMN review_probe_col text`)
	if err == nil {
		// Defensive: should be unreachable, but if the ALTER somehow succeeded,
		// drop the column (as the owner) so it can't corrupt later tests' schema
		// signature. od_runtime cannot drop it, so use a migrator connection
		// directly (not connect(), whose t.Fatalf would mask the real assertion).
		if mig := os.Getenv(envMigratorDSN); mig != "" {
			if mp, cerr := pgxpool.New(ctx, mig); cerr == nil {
				_, _ = mp.Exec(ctx, `ALTER TABLE audit_log DROP COLUMN IF EXISTS review_probe_col`)
				mp.Close()
			}
		}
		t.Fatal("[P0] expected ALTER TABLE audit_log ADD COLUMN to be denied for od_runtime, but it succeeded")
	}
	if !isPermissionDenied(err) {
		t.Errorf("[P0] expected permission denied (SQLSTATE %s) for runtime ALTER ADD COLUMN, got: %v", sqlstatePermissionDenied, err)
	}
}

// TestAuditLog_RuntimeDisableTriggerDenied — AC 2. Connected AS od_runtime,
// disabling the immutability trigger must fail with 42501. ALTER TABLE requires
// ownership, which od_runtime does not have — so it cannot switch off the guard.
// (DISABLE TRIGGER USER targets all user triggers without coupling to the
// trigger's exact name.)
func TestAuditLog_RuntimeDisableTriggerDenied(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	_, err := pool.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER USER`)
	if err == nil {
		t.Fatal("[P0] expected ALTER TABLE audit_log DISABLE TRIGGER to be denied for od_runtime, but it succeeded")
	}
	if !isPermissionDenied(err) {
		t.Errorf("[P0] expected permission denied (SQLSTATE %s) for runtime DISABLE TRIGGER, got: %v", sqlstatePermissionDenied, err)
	}
}

// alwaysEnabledTriggerCount counts the non-internal, ENABLE ALWAYS triggers on
// audit_log. tgenabled='A' is exactly the "ENABLE ALWAYS" state the story
// mandates so a `session_replication_role = replica` session cannot bypass the
// guard — a plain (origin-mode, 'O') trigger would not satisfy this.
func alwaysEnabledTriggerCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		WHERE c.relname = 'audit_log'
		  AND NOT tg.tgisinternal
		  AND tg.tgenabled = 'A'`).Scan(&n); err != nil {
		t.Fatalf("count ENABLE ALWAYS triggers: %v", err)
	}
	return n
}

// runtimeHasUpdateOnAuditLog reports whether od_runtime currently holds UPDATE on
// audit_log. Used to prove the 0002 REVOKE is symmetric: Down must restore the
// 1.3 grant so up->down->up round-trips to the exact prior state.
func runtimeHasUpdateOnAuditLog(t *testing.T, ctx context.Context, pool *pgxpool.Pool) bool {
	t.Helper()
	var granted bool
	if err := pool.QueryRow(ctx,
		`SELECT has_table_privilege('od_runtime', 'public.audit_log', 'UPDATE')`,
	).Scan(&granted); err != nil {
		t.Fatalf("check od_runtime UPDATE grant: %v", err)
	}
	return granted
}

// TestAuditImmutabilityMigration_Reversible — AC 1 DoD: `goose down` drops the
// trigger + function cleanly and restores the grant; up->down->up is idempotent.
//
// Flow (drives goose directly; MUST leave 0002 re-applied so later tests see the
// installed trigger, mirroring the 1.3 round-trip test's restore contract):
//
//	after up   : exactly 2 ENABLE ALWAYS triggers present, od_runtime UPDATE revoked
//	after down : 0 immutability triggers, function gone, od_runtime UPDATE restored
//	after up   : both triggers reinstalled, revoke re-applied
//
// We revert down TO version 1 (`down-to 1`) rather than a single `down`: 0002 is
// no longer the migration tip (0003_users sits on top), so one `down` would only
// revert 0003 and leave 0002's triggers installed. `down-to 1` unwinds everything
// above 0001, exercising 0002's Down regardless of how many migrations follow it,
// while leaving the audit_log TABLE (0001) intact — exactly the scope this checks.
// The closing `up` re-applies every migration above 0001 to restore full state.
func TestAuditImmutabilityMigration_Reversible(t *testing.T) {
	requireGoose(t)
	ctx := testCtx(t)
	migratorDSN := dsnOrSkip(t, envMigratorDSN)
	pool := connect(t, ctx, migratorDSN)

	// Precondition: CI has run `goose up`, so 0002 is applied. 0002 installs
	// EXACTLY two ENABLE ALWAYS triggers (audit_log_no_update_delete +
	// audit_log_no_truncate); asserting == 2 (not >= 1) guards the defense-in-depth
	// TRUNCATE trigger against a silent regression the row trigger would mask.
	if got := alwaysEnabledTriggerCount(t, ctx, pool); got != 2 {
		t.Fatalf("[P1] expected exactly 2 ENABLE ALWAYS triggers on audit_log after goose up, got %d (was 0002 applied? did the TRUNCATE trigger regress?)", got)
	}
	if runtimeHasUpdateOnAuditLog(t, ctx, pool) {
		t.Error("[P1] expected od_runtime UPDATE on audit_log to be REVOKED after 0002")
	}

	// Revert everything above 0001 (0003 users + 0002 immutability), so 0002's
	// Down is exercised even though it is no longer the migration tip.
	runGoose(t, migratorDSN, "down-to", "1")

	if got := alwaysEnabledTriggerCount(t, ctx, pool); got != 0 {
		t.Errorf("[P1] expected 0 immutability triggers after goose down, got %d", got)
	}
	var fnExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'audit_log_immutable')`,
	).Scan(&fnExists); err != nil {
		t.Fatalf("check trigger function after down: %v", err)
	}
	if fnExists {
		t.Error("[P1] expected audit_log_immutable() function to be dropped after goose down")
	}
	if !runtimeHasUpdateOnAuditLog(t, ctx, pool) {
		t.Error("[P1] expected od_runtime UPDATE grant RESTORED after goose down (symmetric Down)")
	}

	// Re-apply every migration above 0001 to restore state for subsequent tests.
	runGoose(t, migratorDSN, "up")

	if got := alwaysEnabledTriggerCount(t, ctx, pool); got != 2 {
		t.Errorf("[P1] expected both immutability triggers reinstalled after goose up (exactly 2 ENABLE ALWAYS), got %d", got)
	}
	if runtimeHasUpdateOnAuditLog(t, ctx, pool) {
		t.Error("[P1] expected od_runtime UPDATE to be re-revoked after goose up")
	}
}
