// Package reconciliation verifies durable attendance and audit invariants for
// a closed event. It reports source-data conflicts rather than rewriting
// attendance rows automatically; only derived data is safe to repair without
// administrator approval.
package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRunAlreadyComplete = errors.New("reconciliation: run already complete")

type RunStatus string

const (
	StatusQueued          RunStatus = "queued"
	StatusRunning         RunStatus = "running"
	StatusCompleted       RunStatus = "completed"
	StatusCompletedIssues RunStatus = "completed_with_issues"
	StatusFailed          RunStatus = "failed"
)

type Summary struct {
	RunID             uuid.UUID
	EventID           uuid.UUID
	Revision          int
	Status            RunStatus
	ScannedRecords    int64
	IssueCount        int64
	RepairCount       int64
	MissingAuditCount int64
}

type Service struct {
	pool *pgxpool.Pool
}

// DueEvents returns ended or archived events that have never received a
// reconciliation run. The caller owns enqueueing; keeping discovery separate
// makes the query easy to test and lets River's leader-elected periodic job
// perform the scheduling without duplicate revisions.
func (s *Service) DueEvents(ctx context.Context) ([]uuid.UUID, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("reconciliation: database pool is nil")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id
		FROM events e
		WHERE (e.status = 'archived' OR e.end_date < CURRENT_DATE)
		  AND NOT EXISTS (
		      SELECT 1 FROM reconciliation_runs r WHERE r.event_id = e.id
		  )
		ORDER BY e.end_date ASC, e.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: find due events: %w", err)
	}
	defer rows.Close()
	var eventIDs []uuid.UUID
	for rows.Next() {
		var eventID uuid.UUID
		if err := rows.Scan(&eventID); err != nil {
			return nil, fmt.Errorf("reconciliation: scan due event: %w", err)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reconciliation: iterate due events: %w", err)
	}
	return eventIDs, nil
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Run is repeatable for one event revision. A completed revision is a stable
// result and is not silently rerun; a later revision creates a new run. The
// whole inspection and issue write happens in one transaction, so River retry
// cannot leave a permanently running run behind after a database failure.
func (s *Service) Run(ctx context.Context, eventID uuid.UUID, revision int) (Summary, error) {
	if s == nil || s.pool == nil {
		return Summary{}, errors.New("reconciliation: database pool is nil")
	}
	if eventID == uuid.Nil {
		return Summary{}, errors.New("reconciliation: event id is empty")
	}
	if revision < 1 {
		return Summary{}, errors.New("reconciliation: revision must be positive")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("reconciliation: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var summary Summary
	var status string
	err = tx.QueryRow(ctx, `
		INSERT INTO reconciliation_runs (event_id, revision)
		VALUES ($1, $2)
		ON CONFLICT (event_id, revision) DO UPDATE SET updated_at = now()
		RETURNING id, event_id, revision, status`, eventID, revision).
		Scan(&summary.RunID, &summary.EventID, &summary.Revision, &status)
	if err != nil {
		return Summary{}, fmt.Errorf("reconciliation: create run: %w", err)
	}
	if RunStatus(status) == StatusCompleted || RunStatus(status) == StatusCompletedIssues {
		return Summary{RunID: summary.RunID, EventID: summary.EventID, Revision: summary.Revision, Status: RunStatus(status)}, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE reconciliation_runs
		SET status = 'running', started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1`, summary.RunID); err != nil {
		return Summary{}, fmt.Errorf("reconciliation: start run: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		SELECT count(*)::bigint,
		       count(*) FILTER (WHERE a.id IS NULL)::bigint
		FROM od_records r
		LEFT JOIN audit_log a
		  ON a.target_table = 'od_records'
		 AND a.target_id = r.id::text
		WHERE r.event_id = $1 AND r.status = 'present'`, eventID).
		Scan(&summary.ScannedRecords, &summary.MissingAuditCount); err != nil {
		return Summary{}, fmt.Errorf("reconciliation: collect stats: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT r.id
		FROM od_records r
		LEFT JOIN audit_log a
		  ON a.target_table = 'od_records'
		 AND a.target_id = r.id::text
		WHERE r.event_id = $1 AND r.status = 'present' AND a.id IS NULL
		ORDER BY r.od_date ASC, r.slot ASC, r.id ASC`, eventID)
	if err != nil {
		return Summary{}, fmt.Errorf("reconciliation: list issues: %w", err)
	}
	for rows.Next() {
		var recordID uuid.UUID
		if err := rows.Scan(&recordID); err != nil {
			rows.Close()
			return Summary{}, fmt.Errorf("reconciliation: scan issue: %w", err)
		}
		details, _ := json.Marshal(map[string]string{
			"reason": "attendance record has no matching immutable audit entry",
		})
		if _, err := tx.Exec(ctx, `
			INSERT INTO reconciliation_issues (run_id, event_id, issue_key, kind, record_id, details)
			VALUES ($1, $2, $3, 'missing_audit', $4, $5)
			ON CONFLICT (run_id, issue_key) DO UPDATE
			SET details = EXCLUDED.details, updated_at = now()`,
			summary.RunID, eventID, "missing_audit:"+recordID.String(), recordID, details); err != nil {
			rows.Close()
			return Summary{}, fmt.Errorf("reconciliation: write issue: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Summary{}, fmt.Errorf("reconciliation: iterate issues: %w", err)
	}
	rows.Close()

	summary.IssueCount = summary.MissingAuditCount
	if summary.IssueCount == 0 {
		summary.Status = StatusCompleted
	} else {
		summary.Status = StatusCompletedIssues
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reconciliation_runs
		SET status = $2, completed_at = now(), scanned_records = $3,
		    issue_count = $4, repair_count = $5, updated_at = now()
		WHERE id = $1`, summary.RunID, summary.Status, summary.ScannedRecords, summary.IssueCount, summary.RepairCount); err != nil {
		return Summary{}, fmt.Errorf("reconciliation: complete run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Summary{}, fmt.Errorf("reconciliation: commit: %w", err)
	}
	return summary, nil
}
