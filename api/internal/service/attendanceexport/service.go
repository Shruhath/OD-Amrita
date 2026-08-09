// Package attendanceexport owns asynchronous, admin-only attendance CSV
// generation. Output is written to the database in bounded chunks so a large
// report never has to live in one application buffer.
package attendanceexport

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type Export struct {
	ID            uuid.UUID `json:"id"`
	EventID       uuid.UUID `json:"event_id"`
	Format        string    `json:"format"`
	Status        string    `json:"status"`
	RowCount      int       `json:"row_count"`
	ErrorSummary  *string   `json:"error_summary,omitempty"`
}

type Args struct {
	ExportID string `json:"export_id"`
}

func (Args) Kind() string { return "attendance_export" }

type Service struct {
	pool   *pgxpool.Pool
	audit  *store.Store
	client *river.Client[pgx.Tx]
}

func NewService(pool *pgxpool.Pool, client *river.Client[pgx.Tx]) *Service {
	return &Service{pool: pool, audit: store.New(pool), client: client}
}

func (s *Service) Create(ctx context.Context, actor Actor, eventID string) (Export, error) {
	if actor.Role != "admin" {
		return Export{}, errors.New("attendance export: admin role required")
	}
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return Export{}, fmt.Errorf("attendance export: invalid event id: %w", err)
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return Export{}, fmt.Errorf("attendance export: invalid actor id: %w", err)
	}
	if s == nil || s.audit == nil || s.client == nil {
		return Export{}, errors.New("attendance export: runtime is incomplete")
	}
	var result Export
	account := strings.ToLower(strings.TrimSpace(actor.Email))
	role := actor.Role
	table := "attendance_exports"
	targetID := ""
	event := store.AuditEvent{Action: domain.ActionAttendanceExportRequested, ActorAccount: &account, ActorRole: &role, TargetTable: &table, TargetID: &targetID}
	event.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]string{"event_id": eventUUID.String(), "format": "csv"})
	}
	if err := s.audit.WithAudit(ctx, event, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		if err := tx.QueryRow(ctx, `SELECT id FROM events WHERE id = $1`, eventUUID).Scan(&eventUUID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, errors.New("attendance export: event not found")
			}
			return false, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO attendance_exports (event_id, requested_by_user_id)
			VALUES ($1, $2) RETURNING id, event_id, format, status`, eventUUID, actorID).
			Scan(&result.ID, &result.EventID, &result.Format, &result.Status); err != nil {
			return false, err
		}
		targetID = result.ID.String()
		if _, err := s.client.InsertTx(ctx, tx, Args{ExportID: result.ID.String()}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
			return false, fmt.Errorf("attendance export: enqueue: %w", err)
		}
		return true, nil
	}); err != nil {
		return Export{}, err
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, eventID, exportID string) (Export, error) {
	if actor.Role != "admin" {
		return Export{}, errors.New("attendance export: admin role required")
	}
	eventUUID, exportUUID, err := parseIDs(eventID, exportID)
	if err != nil {
		return Export{}, err
	}
	var result Export
	if err := s.pool.QueryRow(ctx, `
		SELECT id, event_id, format, status, row_count, error_summary
		FROM attendance_exports WHERE id = $1 AND event_id = $2`, exportUUID, eventUUID).
		Scan(&result.ID, &result.EventID, &result.Format, &result.Status, &result.RowCount, &result.ErrorSummary); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Export{}, errors.New("attendance export: export not found")
		}
		return Export{}, err
	}
	return result, nil
}

func (s *Service) Download(ctx context.Context, actor Actor, eventID, exportID string) (string, error) {
	result, err := s.Get(ctx, actor, eventID, exportID)
	if err != nil {
		return "", err
	}
	if result.Status != "completed" {
		return "", errors.New("attendance export: export is not complete")
	}
	var output string
	if err := s.pool.QueryRow(ctx, `SELECT output_csv FROM attendance_exports WHERE id = $1`, result.ID).Scan(&output); err != nil {
		return "", err
	}
	return output, nil
}

func (s *Service) Run(ctx context.Context, exportID uuid.UUID) error {
	if s == nil || s.pool == nil || s.audit == nil {
		return errors.New("attendance export: runtime is incomplete")
	}
	var eventID, actorID uuid.UUID
	var status, actorRole string
	if err := s.pool.QueryRow(ctx, `
		UPDATE attendance_exports
		SET status = 'processing', output_csv = '', started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'processing')
		RETURNING event_id, requested_by_user_id, status`, exportID).
		Scan(&eventID, &actorID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var finalized string
			if statusErr := s.pool.QueryRow(ctx, `SELECT status FROM attendance_exports WHERE id = $1`, exportID).Scan(&finalized); statusErr != nil {
				return fmt.Errorf("attendance export: missing export: %w", statusErr)
			}
			if finalized == "completed" || finalized == "failed" {
				return nil
			}
		}
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, actorID).Scan(&actorRole); err != nil {
		return err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT s.roll_no, s.name, c.name, r.slot, r.od_date, r.capture_method, r.actor_role, r.server_received_at
		FROM od_records r
		JOIN students s ON s.id = r.student_id
		JOIN categories c ON c.id = r.category_id
		WHERE r.event_id = $1 AND r.status = 'present'
		ORDER BY r.od_date ASC, r.slot ASC, s.roll_no ASC, r.id ASC`, eventID)
	if err != nil {
		return err
	}
	defer rows.Close()

	if err := s.appendChunk(ctx, exportID, "roll_no,student_name,category,slot,od_date,capture_method,actor_role,server_received_at\n"); err != nil {
		return err
	}
	var chunk bytes.Buffer
	writer := csv.NewWriter(&chunk)
	rowCount := 0
	flush := func() error {
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		if chunk.Len() == 0 {
			return nil
		}
		if err := s.appendChunk(ctx, exportID, chunk.String()); err != nil {
			return err
		}
		chunk.Reset()
		writer = csv.NewWriter(&chunk)
		return nil
	}
	for rows.Next() {
		var rollNo, studentName, category, method, role string
		var slot int32
		var odDate time.Time
		var received time.Time
		if err := rows.Scan(&rollNo, &studentName, &category, &slot, &odDate, &method, &role, &received); err != nil {
			return err
		}
		writer.Write([]string{rollNo, studentName, category, strconv.Itoa(int(slot)), odDate.Format("2006-01-02"), method, role, received.UTC().Format(time.RFC3339Nano)})
		rowCount++
		if rowCount%500 == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}

	account := "attendance-export"
	table := "attendance_exports"
	targetID := exportID.String()
	event := store.AuditEvent{Action: domain.ActionAttendanceExportCompleted, ActorAccount: &account, ActorRole: &actorRole, TargetTable: &table, TargetID: &targetID}
	event.PayloadFunc = func() ([]byte, error) { return json.Marshal(map[string]any{"event_id": eventID.String(), "row_count": rowCount}) }
	return s.audit.WithAudit(ctx, event, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		_, err := tx.Exec(ctx, `UPDATE attendance_exports SET status = 'completed', row_count = $2, completed_at = now(), updated_at = now() WHERE id = $1 AND status = 'processing'`, exportID, rowCount)
		return err == nil, err
	})
}

func (s *Service) appendChunk(ctx context.Context, exportID uuid.UUID, chunk string) error {
	_, err := s.pool.Exec(ctx, `UPDATE attendance_exports SET output_csv = output_csv || $2, updated_at = now() WHERE id = $1 AND status = 'processing'`, exportID, chunk)
	return err
}

func parseIDs(eventID, exportID string) (uuid.UUID, uuid.UUID, error) {
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("attendance export: invalid event id: %w", err)
	}
	exportUUID, err := uuid.Parse(strings.TrimSpace(exportID))
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("attendance export: invalid export id: %w", err)
	}
	return eventUUID, exportUUID, nil
}
