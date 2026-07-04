//go:build integration

// Package db — Story 1.5c (DoD 3, 4, 9): the local deny-list table
// auth_session_revocations, its constraints/grants, the scope semantics, and the
// co-commit rollback guarantee. INTEGRATION test: needs a live Postgres + the two
// login roles, so it is DSN-gated and skips cleanly on a no-DB machine. It proves
// the SCHEMA supports the intended predicate via representative SQL, independent
// of generated pgtype signatures (the sqlc queries are gated by sqlc-check).
//
// Activate: go test -tags integration ./test/db/... -run Revocation
package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// seedAdminUser inserts a users row to satisfy the revoked_by_user_id FK and
// returns its id, cleaning it up afterward. Runtime has DML on users (Story 1.5b).
func seedAdminUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subject string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (supabase_subject, email, name, role)
		VALUES ($1, $2, 'Revoker Admin', 'admin')
		RETURNING id`, subject, subject+"@amrita.edu").Scan(&id); err != nil {
		t.Fatalf("seed admin user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

// insertRevocationErr runs one revocation insert in its own tx and rolls it back,
// returning the insert error (nil = accepted). Isolating each probe in its own tx
// means a CHECK/UNIQUE violation aborting the tx never poisons the next probe.
func insertRevocationErr(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subject string, sessionID *string, scope, reason, revokedBy string) error {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe tx: %v", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
		VALUES ($1, $2, $3, $4, $5)`, subject, sessionID, scope, reason, revokedBy)
	return err
}

func uuidPtr(s string) *string { return &s }

// TestRevocationsMigration_SchemaConstraintsAndGrants — DoD 3: the table exists
// with the expected shape, its two CHECKs and the idempotency UNIQUE hold, and
// od_runtime has DML but not DDL on it.
func TestRevocationsMigration_SchemaConstraintsAndGrants(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	// Columns + types (the load-bearing ones).
	wantCols := map[string]string{
		"id":                 "uuid",
		"supabase_subject":   "text",
		"session_id":         "uuid",
		"scope":              "text",
		"revoked_after":      "timestamp with time zone",
		"reason":             "text",
		"revoked_by_user_id": "uuid",
		"created_at":         "timestamp with time zone",
	}
	for col, wantType := range wantCols {
		var gotType string
		if err := migratorPool.QueryRow(ctx, `
			SELECT data_type FROM information_schema.columns
			WHERE table_name = 'auth_session_revocations' AND column_name = $1`, col).Scan(&gotType); err != nil {
			t.Errorf("[P0] missing column %q: %v", col, err)
			continue
		}
		if gotType != wantType {
			t.Errorf("[P0] column %q type: want %q, got %q", col, wantType, gotType)
		}
	}

	adminID := seedAdminUser(t, ctx, runtimePool, "revoker-schema")
	sid := uuidPtr("66666666-6666-6666-6666-666666666666")

	// Valid rows are accepted (runtime has INSERT).
	if err := insertRevocationErr(t, ctx, runtimePool, "target-sub", nil, "all", "leaver", adminID); err != nil {
		t.Errorf("[P0] runtime must be able to INSERT a valid scope=all revocation: %v", err)
	}
	if err := insertRevocationErr(t, ctx, runtimePool, "target-sub", sid, "session", "lost device", adminID); err != nil {
		t.Errorf("[P0] runtime must be able to INSERT a valid scope=session revocation: %v", err)
	}

	// CHECK: scope/session_id coherence.
	if err := insertRevocationErr(t, ctx, runtimePool, "target-sub", sid, "all", "bad", adminID); err == nil {
		t.Error("[P0] scope=all with a session_id must violate the coherence CHECK")
	}
	if err := insertRevocationErr(t, ctx, runtimePool, "target-sub", nil, "session", "bad", adminID); err == nil {
		t.Error("[P0] scope=session without a session_id must violate the coherence CHECK")
	}
	// CHECK: scope enum.
	if err := insertRevocationErr(t, ctx, runtimePool, "target-sub", nil, "everything", "bad", adminID); err == nil {
		t.Error("[P0] an out-of-enum scope must be rejected")
	}

	// DDL denial: runtime is DML-only.
	if _, err := runtimePool.Exec(ctx, `ALTER TABLE auth_session_revocations ADD COLUMN hacked text`); err == nil || !isPermissionDenied(err) {
		t.Errorf("[P0] od_runtime must NOT be able to ALTER the table (want permission denied), got: %v", err)
	}
}

// TestRevocationIdempotency_UniqueGuard — DoD 3: UNIQUE(supabase_subject,
// session_id) makes a repeated scope=session revoke a no-op, while scope=all
// (session_id NULL, distinct under SQL NULL semantics) can be recorded repeatedly
// so a later re-login can be force-logged-out again.
func TestRevocationIdempotency_UniqueGuard(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	adminID := seedAdminUser(t, ctx, migratorPool, "revoker-unique")
	subject := "unique-target"
	sid := "77777777-7777-7777-7777-777777777777"

	t.Cleanup(func() {
		_, _ = migratorPool.Exec(context.Background(), `DELETE FROM auth_session_revocations WHERE supabase_subject = $1`, subject)
	})

	// First session revoke: accepted.
	if _, err := migratorPool.Exec(ctx, `
		INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
		VALUES ($1, $2, 'session', 'first', $3)`, subject, sid, adminID); err != nil {
		t.Fatalf("[P0] first session revoke should insert: %v", err)
	}
	// Duplicate (same subject+session): must violate UNIQUE (idempotent no-op).
	if _, err := migratorPool.Exec(ctx, `
		INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
		VALUES ($1, $2, 'session', 'dup', $3)`, subject, sid, adminID); err == nil {
		t.Error("[P0] a duplicate (subject, session_id) session revoke must be rejected by the UNIQUE guard")
	}
	// Two scope=all rows for the same subject: both accepted (session_id NULL is distinct).
	for i := 0; i < 2; i++ {
		if _, err := migratorPool.Exec(ctx, `
			INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
			VALUES ($1, NULL, 'all', 'repeatable', $2)`, subject, adminID); err != nil {
			t.Errorf("[P1] scope=all revoke #%d for the same subject must be recordable: %v", i+1, err)
		}
	}
}

// TestRevocationCheck_ScopeSemantics — DoD 4/Dev Notes: the revocation predicate,
// expressed as representative SQL to prove the schema supports it (the sqlc
// IsSessionRevoked/GetRevocationForToken query is red-gated by sqlc-check):
//   - scope=session rejects only the matching session_id;
//   - scope=all rejects tokens with iat <= revoked_after and allows a re-login
//     whose iat is after revoked_after; revoked_after uses DB now().
func TestRevocationCheck_ScopeSemantics(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	adminID := seedAdminUser(t, ctx, pool, "revoker-semantics")
	subject := "semantics-target"
	revokedSession := "88888888-8888-8888-8888-888888888888"
	otherSession := "99999999-9999-9999-9999-999999999999"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth_session_revocations WHERE supabase_subject = $1`, subject)
	})

	// A scope=all revocation stamped at DB now().
	var revokedAfter time.Time
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
		VALUES ($1, NULL, 'all', 'offboard', $2)
		RETURNING revoked_after`, subject, adminID).Scan(&revokedAfter); err != nil {
		t.Fatalf("insert all-revocation: %v", err)
	}
	// And a scope=session revocation for one specific session.
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
		VALUES ($1, $2, 'session', 'lost device', $3)`, subject, revokedSession, adminID); err != nil {
		t.Fatalf("insert session-revocation: %v", err)
	}

	// isRevoked mirrors the intended check: a token (sub, session_id, iat) is
	// revoked if a matching session row exists OR an all row with revoked_after
	// at/after the token's iat exists.
	isRevoked := func(sessionID string, iat time.Time) bool {
		var revoked bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM auth_session_revocations
				WHERE supabase_subject = $1
				  AND (
				    (scope = 'session' AND session_id = $2)
				    OR (scope = 'all' AND revoked_after >= $3)
				  )
			)`, subject, sessionID, iat).Scan(&revoked); err != nil {
			t.Fatalf("run revocation check: %v", err)
		}
		return revoked
	}

	// Token issued before the all-revocation: revoked.
	if !isRevoked(otherSession, revokedAfter.Add(-time.Minute)) {
		t.Error("[P0] scope=all must revoke a token issued before revoked_after")
	}
	// Legitimate re-login: token issued after revoked_after, unrelated session: NOT revoked.
	if isRevoked(otherSession, revokedAfter.Add(time.Minute)) {
		t.Error("[P0] scope=all must allow a re-login issued after revoked_after")
	}
	// The specifically revoked session is rejected regardless of iat.
	if !isRevoked(revokedSession, revokedAfter.Add(time.Minute)) {
		t.Error("[P0] scope=session must revoke exactly the matching session_id")
	}
}

// TestRevocationCoCommit_AuditRollback — DoD 9: the revocation insert and its
// audit row co-commit through store.WithAudit. An injected audit-insert failure
// must roll back the revocation write (no orphan deny-list row), and a clean run
// must persist both.
func TestRevocationCoCommit_AuditRollback(t *testing.T) {
	ctx := testCtx(t)
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	adminID := seedAdminUser(t, ctx, runtimePool, "revoker-cocommit")
	subject := "cocommit-target"

	t.Cleanup(func() {
		_, _ = runtimePool.Exec(context.Background(), `DELETE FROM auth_session_revocations WHERE supabase_subject = $1`, subject)
	})

	insertRevocation := func(ctx context.Context, tx pgx.Tx) (bool, error) {
		_, err := tx.Exec(ctx, `
			INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
			VALUES ($1, NULL, 'all', 'co-commit probe', $2)`, subject, adminID)
		return true, err
	}
	evt := store.AuditEvent{Action: domain.ActionAuthSessionRevoked}

	// Rollback direction: audit insert fails -> the revocation must not persist.
	failing := store.New(runtimePool, store.WithAuditInserter(func(context.Context, pgx.Tx, store.AuditEvent) error {
		return context.DeadlineExceeded
	}))
	if err := failing.WithAudit(ctx, evt, insertRevocation); err == nil {
		t.Fatal("[P0] a failing audit insert must fail the WithAudit call")
	}
	if n := countRevocations(t, ctx, runtimePool, subject); n != 0 {
		t.Errorf("[P0] the revocation must be rolled back when the audit insert fails, found %d rows", n)
	}

	// Clean run: revocation + audit both persist.
	ok := store.New(runtimePool)
	if err := ok.WithAudit(ctx, evt, insertRevocation); err != nil {
		t.Fatalf("[P0] a clean revocation co-commit should succeed: %v", err)
	}
	if n := countRevocations(t, ctx, runtimePool, subject); n != 1 {
		t.Errorf("[P0] a committed revocation must persist exactly one row, found %d", n)
	}
}

func countRevocations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subject string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth_session_revocations WHERE supabase_subject = $1`, subject).Scan(&n); err != nil {
		t.Fatalf("count revocations: %v", err)
	}
	return n
}
