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

// The pool must not retire every connection at the same instant.
//
// pgx's MaxConnLifetime defaults to an hour with NO jitter, and the pool fills
// on the first burst of traffic — so without this the whole pool reaches its
// deadline together and redials the hosted database as a herd, which is what
// made the Fly health check fail and recover roughly hourly on both machines.
// Asserted on the config rather than by waiting an hour, so it is a real test.
func TestNewPool_SpreadsConnectionRetirementAndKeepsConnectionsWarm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, "postgres://od_runtime:x@localhost:1/od_amrita?sslmode=disable")
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	cfg := pool.Config()
	if cfg.MaxConnLifetimeJitter <= 0 {
		t.Error("connections retire on an unjittered deadline, so the whole pool redials at once")
	}
	if cfg.MinConns < 1 {
		t.Error("no connection is kept warm, so the health probe pays for the first dial itself")
	}
}

// A DSN that states these settings wins, the same way it already does for
// pool_max_conns — an operator must be able to retune a live deployment through
// the connection string without waiting for a build.
func TestNewPool_DSNOverridesTheDefaults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, "postgres://od_runtime:x@localhost:1/od_amrita?sslmode=disable"+
		"&pool_max_conn_lifetime_jitter=90s&pool_min_conns=7")
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	cfg := pool.Config()
	if cfg.MaxConnLifetimeJitter != 90*time.Second {
		t.Errorf("MaxConnLifetimeJitter = %s, want the DSN's 90s", cfg.MaxConnLifetimeJitter)
	}
	if cfg.MinConns != 7 {
		t.Errorf("MinConns = %d, want the DSN's 7", cfg.MinConns)
	}
}
