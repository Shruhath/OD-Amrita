package store

// pool.go — the runtime pgx connection pool. The DSN passed here must be the
// od_runtime (DML-only) DSN: the app never connects as od_migrator. Business
// queries do NOT live here — only the pool constructor and its Ping path
// (used by the /health handler). The same-transaction write helpers land in
// tx.go/audit.go in a later story.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool constructs the runtime connection pool for the given DSN. pgxpool
// connects lazily; use (*pgxpool.Pool).Ping to verify live connectivity.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse runtime dsn: %w", err)
	}
	// Give the pool a sane connection ceiling so the /health probe isn't starved
	// of a connection under request load — pgx's default is max(4, NumCPU),
	// easily saturated on a small host, which would flip /health to a false
	// db:down. A DSN that sets pool_max_conns wins. Prune dead connections
	// promptly too, so a dropped DB surfaces quickly on the readiness probe.
	if !strings.Contains(dsn, "pool_max_conns") {
		cfg.MaxConns = 10
	}
	if !strings.Contains(dsn, "pool_health_check_period") {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	// Spread connection retirement out. pgx retires a connection at
	// MaxConnLifetime (one hour by default) and, with no jitter, EVERY connection
	// in the pool is opened within a moment of the first request and therefore
	// expires within a moment of the others. All of them then redial the database
	// at once.
	//
	// This is not hypothetical: the Fly health check failed and recovered on very
	// close to an hourly cycle, on both machines, offset from each other by their
	// boot times. Against a hosted database each redial is a fresh TLS handshake
	// and authentication over the open internet, so the herd routinely outran the
	// probe's 5s timeout — and a failed probe pulls the machine out of rotation,
	// which on a two-machine deployment is half the capacity for marking.
	//
	// Jitter makes pgx retire each connection somewhere in the five minutes BEFORE
	// its deadline instead of exactly on it, so the pool always keeps live
	// connections while the rest cycle. MinConns then keeps a warm connection in
	// hand rather than making the health probe itself pay for the first dial.
	if !strings.Contains(dsn, "pool_max_conn_lifetime_jitter") {
		cfg.MaxConnLifetimeJitter = 5 * time.Minute
	}
	if !strings.Contains(dsn, "pool_min_conns") {
		cfg.MinConns = 2
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: create pgx pool: %w", err)
	}
	return pool, nil
}
