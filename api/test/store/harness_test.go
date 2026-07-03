//go:build integration

// Package storetest is the Story 1.4 acceptance harness for the same-transaction
// write helper (store.WithAudit). It lives in its OWN directory/package — not in
// api/test/db — on purpose: these tests reference store.* symbols that do not
// exist until Story 1.4 lands, so keeping them here means the api/test/db package
// (the green Story 1.3 integration suite) still compiles and runs unaffected.
//
// INTEGRATION tests: `//go:build integration` + DSN-gated, so `go test ./...`
// (no DB) skips them cleanly. This file mirrors the minimal helpers from
// api/test/db/harness_test.go (they cannot be imported across test packages).
//
// Priority tags: [P0] critical gate · [P1] important · [P2] guard.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The two DSNs, same names as the Story 1.3 harness. Migrator (owner) sets up the
// probe table + inspects rows; runtime (DML-only) is what store.Store connects as.
const (
	envMigratorDSN = "DATABASE_URL_MIGRATOR"
	envRuntimeDSN  = "DATABASE_URL_RUNTIME"
)

// dsnOrSkip returns the DSN from env, or skips so the no-DB path stays clean.
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

// testCtx returns a context with a sane per-test timeout.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// randHex returns 16 hex chars of randomness for unique probe-table names and
// per-run audit markers (so parallel/repeat runs never collide, and the
// conservation test can count only the audit rows it wrote).
func randHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("randHex: %v", err)
	}
	return hex.EncodeToString(b)
}

// newProbeTable creates a throwaway "business" table AS od_migrator (the owner),
// grants od_runtime the DML it needs, and drops it in cleanup. This is the
// Decision-5 probe table: this epic has no real write tables yet, so WithAudit
// co-commits against this disposable target. The single-column UNIQUE key lets
// the dedup no-op path (INSERT ... ON CONFLICT (k) DO NOTHING) affect 0 rows.
func newProbeTable(t *testing.T, ctx context.Context, migratorPool *pgxpool.Pool) string {
	t.Helper()
	name := "tx_probe_" + randHex(t)

	if _, err := migratorPool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (k text PRIMARY KEY)`, name)); err != nil {
		t.Fatalf("create probe table %s: %v", name, err)
	}
	// od_runtime needs INSERT (the business write) + SELECT (assertions).
	if _, err := migratorPool.Exec(ctx, fmt.Sprintf(`GRANT SELECT, INSERT ON %s TO od_runtime`, name)); err != nil {
		t.Fatalf("grant probe table %s: %v", name, err)
	}
	t.Cleanup(func() {
		// Best-effort drop on a fresh context (the test's ctx may be done).
		_, _ = migratorPool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, name))
	})
	return name
}

// auditRowCountForRun returns how many audit rows carry this run's marker in
// target_id. Filtering by a per-run marker isolates a test's rows from every
// other test's audit writes in the shared (append-only) table.
func auditRowCountForRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE target_id = $1`, runID,
	).Scan(&n); err != nil {
		t.Fatalf("count audit rows for run %s: %v", runID, err)
	}
	return n
}

// probeRowCount returns how many rows exist in the probe table (used to prove a
// business write did / did not land).
func probeRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, probe string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, probe)).Scan(&n); err != nil {
		t.Fatalf("count probe rows in %s: %v", probe, err)
	}
	return n
}
