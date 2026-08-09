// Command river-migrate applies River's own schema migrations and grants the
// runtime role only the DML permissions required by the worker client.
//
// River's tables are intentionally not goose migrations: River owns their
// versioned schema and its migrator must remain aligned with the pinned River
// module. Run this command as od_migrator before starting an API deployment.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const runtimeGrants = `
GRANT USAGE ON TYPE river_job_state TO od_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE river_job, river_leader, river_queue, river_notification TO od_runtime;
GRANT USAGE, SELECT ON SEQUENCE river_job_id_seq, river_notification_id_seq TO od_runtime;
`

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dsn := os.Getenv("DATABASE_URL_MIGRATOR")
	if dsn == "" {
		return fmt.Errorf("river-migrate: DATABASE_URL_MIGRATOR is required")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("river-migrate: create pool: %w", err)
	}
	defer pool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river-migrate: create migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river-migrate: migrate: %w", err)
	}
	if _, err := pool.Exec(ctx, runtimeGrants); err != nil {
		return fmt.Errorf("river-migrate: grant runtime permissions: %w", err)
	}
	return nil
}
