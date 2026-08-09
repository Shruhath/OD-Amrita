// Package jobs owns the River runtime shared by all background jobs.
//
// The package deliberately exposes registration and lifecycle seams rather
// than starting a client by itself. A River client with no registered worker
// is a configuration error, not a useful background runtime; the application
// must register at least one real job before calling NewClient.
package jobs

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const (
	// QueueDefault is the shared queue for ordinary background work.
	QueueDefault = river.QueueDefault

	// DefaultMaxWorkers bounds concurrent work per API process. Jobs must still
	// use bounded database batches; this is only the process-level concurrency
	// ceiling.
	DefaultMaxWorkers = 10

	// DefaultShutdownTimeout is long enough for a bounded job batch to finish
	// while keeping deploy shutdown finite.
	DefaultShutdownTimeout = 10 * time.Second
)

// Registry is the only construction seam for the application's River workers.
type Registry struct {
	workers *river.Workers
}

// NewRegistry creates an empty registry. Callers must add a real worker before
// constructing a running client.
func NewRegistry() *Registry {
	return &Registry{workers: river.NewWorkers()}
}

// Workers returns the River registry for typed worker registration.
func (r *Registry) Workers() *river.Workers {
	if r == nil {
		return nil
	}
	return r.workers
}

// NewClient constructs a River client with the application's queue policy.
// It refuses an empty registry so a deployment cannot appear operational while
// silently executing no jobs.
type ClientOption func(*river.Config)

// WithPeriodicJobs adds River-owned periodic enqueue definitions to the
// application client. Periodic jobs are leader-elected by River, so multiple
// API instances do not duplicate scheduled work.
func WithPeriodicJobs(periodicJobs ...*river.PeriodicJob) ClientOption {
	return func(config *river.Config) { config.PeriodicJobs = append(config.PeriodicJobs, periodicJobs...) }
}

func NewClient(pool *pgxpool.Pool, registry *Registry, options ...ClientOption) (*river.Client[pgx.Tx], error) {
	if pool == nil {
		return nil, fmt.Errorf("jobs: database pool is nil")
	}
	if registry == nil || registry.workers == nil {
		return nil, fmt.Errorf("jobs: worker registry is nil")
	}
	config := &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueDefault: {MaxWorkers: DefaultMaxWorkers},
		},
		Workers: registry.workers,
	}
	for _, option := range options {
		if option != nil {
			option(config)
		}
	}
	return river.NewClient(riverpgxv5.New(pool), config)
}

// Start starts a configured River client and fails if the worker registry is
// empty. River performs the final validation; this wrapper keeps lifecycle
// calls consistent at the application boundary.
func Start(ctx context.Context, client *river.Client[pgx.Tx]) error {
	if client == nil {
		return fmt.Errorf("jobs: client is nil")
	}
	return client.Start(ctx)
}

// Stop drains the River client with a bounded timeout. Shutdown errors are
// logged and returned so callers can report them without skipping HTTP drain.
func Stop(parent context.Context, client *river.Client[pgx.Tx]) error {
	if client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, DefaultShutdownTimeout)
	defer cancel()
	if err := client.Stop(ctx); err != nil {
		log.Printf("jobs: error stopping River client: %v", err)
		return err
	}
	return nil
}
