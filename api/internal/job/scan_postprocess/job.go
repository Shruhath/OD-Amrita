// Package scanpostprocess contains durable follow-up work for accepted scans.
package scanpostprocess

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type Args struct {
	ScanID string `json:"scan_id"`
}

func (Args) Kind() string { return "scan_post_processing" }

type Worker struct {
	river.WorkerDefaults[Args]
	Pool *pgxpool.Pool
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	if w == nil || w.Pool == nil {
		return errors.New("scan post-processing: runtime is incomplete")
	}
	scanID, err := uuid.Parse(job.Args.ScanID)
	if err != nil {
		return fmt.Errorf("scan post-processing: invalid scan id: %w", err)
	}
	var eventID, recordID uuid.UUID
	if err := w.Pool.QueryRow(ctx, `
		UPDATE scan_post_processings
		SET status = 'processing', attempts = attempts + 1, started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE scan_id = $1 AND status IN ('queued', 'processing')
		RETURNING event_id, record_id`, scanID).Scan(&eventID, &recordID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var status string
			if statusErr := w.Pool.QueryRow(ctx, `SELECT status FROM scan_post_processings WHERE scan_id = $1`, scanID).Scan(&status); statusErr != nil {
				return fmt.Errorf("scan post-processing: missing scan: %w", statusErr)
			}
			if status == "completed" || status == "failed" {
				return nil
			}
		}
		return err
	}
	var exists bool
	if err := w.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM od_records WHERE id = $1 AND event_id = $2)`, recordID, eventID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("scan post-processing: attendance record %s is not visible yet", recordID)
	}
	if err := w.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_log WHERE target_table = 'od_records' AND target_id = $1)`, recordID.String()).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("scan post-processing: audit record %s is not visible yet", recordID)
	}
	_, err = w.Pool.Exec(ctx, `
		UPDATE scan_post_processings
		SET status = 'completed', audit_verified = true, completed_at = now(), updated_at = now(), error_summary = NULL
		WHERE scan_id = $1 AND status = 'processing'`, scanID)
	return err
}
