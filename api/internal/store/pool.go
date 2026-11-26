package store

// pool.go — the runtime pgx connection pool. The DSN passed here must be the
// od_runtime (DML-only) DSN: the app never connects as od_migrator. Business
// queries do NOT live here — only the pool constructor and its Ping path
// (used by the /health handler). The same-transaction write helpers live in
// tx.go/audit.go.

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
	// The health-check ticker is what re-creates connections. On a database that
	// suspends when idle, every tick is a chance to wake it, so this is a cost
	// knob as much as a liveness one. A minute is frequent enough to prune a
	// dead connection before a request finds it and rare enough not to be a
	// heartbeat.
	if !strings.Contains(dsn, "pool_health_check_period") {
		cfg.HealthCheckPeriod = 1 * time.Minute
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
	// connections while the rest cycle.
	if !strings.Contains(dsn, "pool_max_conn_lifetime_jitter") {
		cfg.MaxConnLifetimeJitter = 5 * time.Minute
	}
	// ── MinConns MUST STAY ZERO AGAINST A SUSPENDING DATABASE ──────────────────
	//
	// This used to be 2, to keep a warm connection so the /health probe would not
	// pay for the first dial. That justification died with the probe's ping (see
	// internal/handler/health), and what it left behind was a bill.
	//
	// The mechanism: Neon closes every connection when it suspends. pgx's
	// health-check ticker then sees the pool below MinConns and IMMEDIATELY opens
	// replacements — which wakes the compute straight back up. The database can
	// never stay suspended, so it is billed around the clock no matter how quiet
	// the app is. It is a well-known pgx-on-serverless-Postgres failure mode, and
	// it is why silencing /health alone would have changed nothing.
	//
	// Zero means the pool holds nothing open when nobody is asking. The cost is
	// that the first request after an idle period pays a dial — and, if the
	// database had suspended, its ~300-500ms resume. That is the trade, and it is
	// paid by a background poll far more often than by a person.
	//
	// A DSN that sets pool_min_conns still wins, so this is overridable in an
	// emergency without a deploy.
	if !strings.Contains(dsn, "pool_min_conns") {
		cfg.MinConns = 0
	}
	// ── AND AN IDLE CONNECTION MUST NOT OUTLIVE THE IDLE PERIOD ────────────────
	//
	// MinConns = 0 says the pool need not KEEP a connection. It does not say the
	// pool will let go of one it already has: pgx retires an idle connection at
	// MaxConnIdleTime, which defaults to THIRTY MINUTES. Neon suspends after five
	// of no client activity, so a single request at any point in a quiet half hour
	// left an open connection sitting across the whole suspend window — and an
	// open connection is activity. The compute stayed awake and billed, and the
	// MinConns work above bought nothing on its own.
	//
	// Two minutes is comfortably inside Neon's five-minute idle timeout, so the
	// pool is empty well before the compute decides to suspend, and comfortably
	// longer than any burst of marking, so a coordinator working steadily never
	// redials. Overridable through the DSN like the rest.
	if !strings.Contains(dsn, "pool_max_conn_idle_time") {
		cfg.MaxConnIdleTime = 2 * time.Minute
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: create pgx pool: %w", err)
	}
	return pool, nil
}
