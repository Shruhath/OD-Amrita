// Package attendanceimport contains the River worker for bulk attendance CSVs.
package attendanceimport

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	importsvc "github.com/Shruhath/OD-Amrita/api/internal/service/attendanceimport"
)

type Worker struct {
	river.WorkerDefaults[importsvc.Args]
	Service *importsvc.Service
}

func (w *Worker) Work(ctx context.Context, job *river.Job[importsvc.Args]) error {
	if w == nil || w.Service == nil {
		return fmt.Errorf("attendance import: service is nil")
	}
	importID, err := uuid.Parse(job.Args.ImportID)
	if err != nil {
		return fmt.Errorf("attendance import: invalid import id: %w", err)
	}
	return w.Service.Run(ctx, importID)
}
