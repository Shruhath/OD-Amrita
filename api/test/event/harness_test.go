//go:build integration

package eventtest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func poolFromEnv(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(name)
	if dsn == "" {
		t.Skipf("%s not set; skipping database integration test", name)
	}
	pool, err := pgxpool.New(testContext(t), dsn)
	if err != nil {
		t.Fatalf("connect %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool
}
