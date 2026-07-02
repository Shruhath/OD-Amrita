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
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: create pgx pool: %w", err)
	}
	return pool, nil
}
