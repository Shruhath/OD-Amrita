package store

// pool.go — the runtime pgx connection pool. The DSN passed here must be the
// od_runtime (DML-only) DSN: the app never connects as od_migrator. Business
// queries do NOT live here — only the pool constructor and its Ping path
// (used by the /health handler). The same-transaction write helpers land in
// tx.go/audit.go in a later story.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool constructs the runtime connection pool for the given DSN. pgxpool
// connects lazily; use (*pgxpool.Pool).Ping to verify live connectivity.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: create pgx pool: %w", err)
	}
	return pool, nil
}
