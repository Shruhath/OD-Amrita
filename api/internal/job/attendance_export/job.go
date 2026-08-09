// Package attendanceexport contains the River worker for CSV report exports.
package attendanceexport

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	exportsvc "github.com/Shruhath/OD-Amrita/api/internal/service/attendanceexport"
)

type Worker struct {
	river.WorkerDefaults[exportsvc.Args]
	Service *exportsvc.Service
}

func (w *Worker) Work(ctx context.Context, job *river.Job[exportsvc.Args]) error {
	if w == nil || w.Service == nil {
		return fmt.Errorf("attendance export: service is nil")
	}
	exportID, err := uuid.Parse(job.Args.ExportID)
	if err != nil {
		return fmt.Errorf("attendance export: invalid export id: %w", err)
	}
	return w.Service.Run(ctx, exportID)
}
