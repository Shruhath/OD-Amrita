// Package eodaudit contains River jobs for end-of-day attendance checks.
package eodaudit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Shruhath/OD-Amrita/api/internal/service/reconciliation"
)

type Args struct {
	EventID  string `json:"event_id"`
	Revision int    `json:"revision"`
}

func (Args) Kind() string { return "eod_reconciliation" }

type SweepArgs struct{}

func (SweepArgs) Kind() string { return "eod_reconciliation_sweep" }

type Worker struct {
	river.WorkerDefaults[Args]
	Service *reconciliation.Service
}

type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Service *reconciliation.Service
	Client  *river.Client[pgx.Tx]
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	if w == nil || w.Service == nil || w.Client == nil {
		return fmt.Errorf("eod reconciliation sweep: runtime is incomplete")
	}
	eventIDs, err := w.Service.DueEvents(ctx)
	if err != nil {
		return err
	}
	for _, eventID := range eventIDs {
		if err := Enqueue(ctx, w.Client, eventID, 1); err != nil {
			return err
		}
	}
	return nil
}

// PeriodicSweep runs frequently enough to cover event closeout without making
// the API request path responsible for scheduling. River elects one periodic
// producer leader across API instances.
func PeriodicSweep() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) {
			return SweepArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
		},
		&river.PeriodicJobOpts{RunOnStart: true},
	)
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	if w == nil || w.Service == nil {
		return fmt.Errorf("eod reconciliation: service is nil")
	}
	eventID, err := uuid.Parse(job.Args.EventID)
	if err != nil {
		return fmt.Errorf("eod reconciliation: invalid event id: %w", err)
	}
	if job.Args.Revision < 1 {
		return fmt.Errorf("eod reconciliation: revision must be positive")
	}
	_, err = w.Service.Run(ctx, eventID, job.Args.Revision)
	return err
}

func Enqueue(ctx context.Context, client *river.Client[pgx.Tx], eventID uuid.UUID, revision int) error {
	if client == nil {
		return fmt.Errorf("eod reconciliation: River client is nil")
	}
	if eventID == uuid.Nil || revision < 1 {
		return fmt.Errorf("eod reconciliation: invalid enqueue arguments")
	}
	_, err := client.Insert(ctx, Args{EventID: eventID.String(), Revision: revision}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	return err
}
