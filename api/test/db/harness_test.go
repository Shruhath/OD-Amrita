//go:build integration

// Package db is the DB-foundation acceptance harness: goose
// migrations round-trip, the dual-role model (od_migrator DDL vs od_runtime
// DML-only), and the runtime-role DDL-denial gate.
//
// These are INTEGRATION tests — they need a real Postgres and the two login
// roles. They are excluded from the default build by the `integration` build
// tag, so `go test ./...` (no tag) stays green with no DB present. Run them as:
//
//	# with a live Postgres + the two roles bootstrapped:
//	DATABASE_URL_MIGRATOR=postgres://od_migrator:...@localhost:5432/od_amrita?sslmode=disable \
//	DATABASE_URL_RUNTIME=postgres://od_runtime:...@localhost:5432/od_amrita?sslmode=disable \
//	go test -tags integration ./test/db/... -v
//
// Each test env-gates on the DSN it needs and t.Skip()s when it is absent, so a
// no-DB developer machine skips cleanly. CI (services: postgres:17) provides the
// DSNs, runs `goose up`, runs these, then `goose down`.
//
// Without the migrations and roles applied, running with `-tags integration`
// fails (no schema / no roles).
//
// Priority tags in test names: [P0] critical gate · [P1] important · [P2] guard.
package db

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Env var names for the two DSNs (see api/.env.example, which documents this
// migrator/runtime split).
const (
	envMigratorDSN = "DATABASE_URL_MIGRATOR"
	envRuntimeDSN  = "DATABASE_URL_RUNTIME"
)

// sqlstatePermissionDenied is Postgres' insufficient_privilege code — the
// signal AC 3 asserts when the runtime role attempts DDL.
const sqlstatePermissionDenied = "42501"

// dsnOrSkip returns the DSN from env, or skips the test when it is absent so the
// no-DB developer path (`go test ./...`) and a partially-configured CI both stay
// clean rather than failing on a missing dependency.
func dsnOrSkip(t *testing.T, env string) string {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s not set; skipping DB integration test (set it to run against Postgres)", env)
	}
	return dsn
}

// connect opens a pgx pool for the given DSN and registers cleanup.
func connect(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// isPermissionDenied reports whether err is a Postgres permission-denied error
// (SQLSTATE 42501). It asserts on the typed pgconn error, never on a message
// substring — the AC-3 gate depends on this being exact.
func isPermissionDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlstatePermissionDenied
}

// testCtx returns a context with a sane per-test timeout.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// migrationsDir resolves api/db/migrations relative to this package
// (api/test/db → ../../db/migrations).
func migrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "db", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}

// requireGoose skips (environmental, not a red-phase pass) when the goose binary
// is unavailable, mirroring the contract harness' requireTool pattern.
func requireGoose(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("goose"); err != nil {
		t.Skip("goose not on PATH; skipping migration round-trip (environmental)")
	}
}

// runGoose runs `goose -dir <migrations> postgres "<dsn>" <sub>` as the migrator.
func runGoose(t *testing.T, dsn, sub string, args ...string) {
	t.Helper()
	argv := append([]string{"-dir", migrationsDir(t), "postgres", dsn, sub}, args...)
	cmd := exec.Command("goose", argv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("goose %s failed: %v\n%s", sub, err, out)
	}
}

// clearRollbackBlockers empties the tables whose CONTENT — not whose schema —
// can make a legitimate rollback impossible, so that a reset-based test starts
// from a defined state instead of inheriting whatever ran before it.
//
// Why this is needed at all: 0016's Down restores the pre-multi-day unique index
// `(student_id, event_id, slot)`. That is correct and deliberately does not
// collapse the colliding rows, because they are attendance records and deleting
// a student's OD to make a rollback succeed would be worse than refusing. But it
// means `goose reset` fails on any database where a student has been marked in
// the same slot on two days — which is the headline feature, so `make
// test-integration` run twice against one database went red on the second run,
// reporting a migration error for what is actually leftover data.
//
// This does NOT weaken the round-trip gate. Every Down and every Up still runs
// and the schema signature is still compared; reset drops these tables outright
// moments later, so nothing asserted is affected. It only stops the test
// depending on package execution order.
func clearRollbackBlockers(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []string{
		`DELETE FROM disputes`,
		`DELETE FROM od_records`,
		`DELETE FROM event_capture_credit_usage`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("clear rollback blockers (%s): %v", statement, err)
		}
	}
}

// auditLogColumns returns an ordered signature of the audit_log columns
// ("name:type:nullable"), used to prove the migration round-trip is idempotent.
func auditLogColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_name = 'audit_log'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("query audit_log columns: %v", err)
	}
	defer rows.Close()

	var sig []string
	for rows.Next() {
		var name, typ, nullable string
		if err := rows.Scan(&name, &typ, &nullable); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		sig = append(sig, name+":"+typ+":"+nullable)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	return sig
}
