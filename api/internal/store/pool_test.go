package store

import (
	"context"
	"testing"
	"time"
)

// These are no-DB unit tests: pgxpool constructs lazily, so NewPool must parse
// the DSN without dialing. Live connectivity is covered by the integration
// suite under api/test/db (build-tagged) and the /health harness.

func TestNewPool_InvalidDSNRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := NewPool(ctx, "not-a-postgres-dsn://%%%"); err == nil {
		t.Fatal("expected NewPool to reject an unparseable DSN")
	}
}

func TestNewPool_ValidDSNConstructsLazily(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, "postgres://od_runtime:x@localhost:1/od_amrita?sslmode=disable")
	if err != nil {
		t.Fatalf("NewPool must construct lazily without dialing: %v", err)
	}
	defer pool.Close()

	// Nothing listens on port 1 — the Ping path must surface the failure.
	pingCtx, cancelPing := context.WithTimeout(ctx, 2*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err == nil {
		t.Fatal("expected Ping to fail with no server listening")
	}
}
