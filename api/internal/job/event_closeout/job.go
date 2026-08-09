package eventcloseout

import (
	"context"
	"fmt"

	closeoutsvc "github.com/Shruhath/OD-Amrita/api/internal/service/eventcloseout"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

type Worker struct {
	river.WorkerDefaults[closeoutsvc.Args]
	Service *closeoutsvc.Service
}

func (w *Worker) Work(ctx context.Context, job *river.Job[closeoutsvc.Args]) error {
	if w == nil || w.Service == nil {
		return fmt.Errorf("event closeout: service is nil")
	}
	id, err := uuid.Parse(job.Args.CloseoutID)
	if err != nil {
		return fmt.Errorf("event closeout: invalid closeout id: %w", err)
	}
	return w.Service.Run(ctx, id)
}
