// Package eventcloseout owns the asynchronous closeout and report workflow for
// archived events.
package eventcloseout

import (
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
	"github.com/Shruhath/OD-Amrita/api/internal/service/reconciliation"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

type Actor struct{ UserID, Email, Role string }

type Closeout struct {
	ID           uuid.UUID `json:"id"`
	EventID      uuid.UUID `json:"event_id"`
	Status       string    `json:"status"`
	TotalRecords int64     `json:"total_records"`
	ErrorSummary *string   `json:"error_summary,omitempty"`
}

type Args struct {
	CloseoutID string `json:"closeout_id"`
}

func (Args) Kind() string { return "event_closeout" }

type Service struct {
	pool      *pgxpool.Pool
	audit     *store.Store
	client    *river.Client[pgx.Tx]
	reconcile *reconciliation.Service
}

func NewService(pool *pgxpool.Pool, client *river.Client[pgx.Tx]) *Service {
	return &Service{pool: pool, audit: store.New(pool), client: client, reconcile: reconciliation.NewService(pool)}
}

func (s *Service) Create(ctx context.Context, actor Actor, eventID string) (Closeout, error) {
	if actor.Role != "admin" {
		return Closeout{}, errors.New("event closeout: admin role required")
	}
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return Closeout{}, fmt.Errorf("event closeout: invalid event id: %w", err)
	}
	actorUUID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return Closeout{}, fmt.Errorf("event closeout: invalid actor id: %w", err)
	}
	if s == nil || s.pool == nil || s.audit == nil || s.client == nil {
		return Closeout{}, errors.New("event closeout: runtime is incomplete")
	}
	var result Closeout
	account, role, table, targetID := strings.ToLower(strings.TrimSpace(actor.Email)), actor.Role, "event_closeouts", ""
	event := store.AuditEvent{Action: domain.ActionEventCloseoutRequested, ActorAccount: &account, ActorRole: &role, TargetTable: &table, TargetID: &targetID}
	event.PayloadFunc = func() ([]byte, error) { return json.Marshal(map[string]string{"event_id": eventUUID.String()}) }
	if err := s.audit.WithAudit(ctx, event, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM events WHERE id = $1`, eventUUID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, errors.New("event closeout: event not found")
			}
			return false, err
		}
		if status != "archived" {
			return false, errors.New("event closeout: event must be archived")
		}
		if err := tx.QueryRow(ctx, `INSERT INTO event_closeouts (event_id, requested_by_user_id) VALUES ($1, $2) ON CONFLICT (event_id) DO UPDATE SET updated_at = now() RETURNING id, event_id, status`, eventUUID, actorUUID).Scan(&result.ID, &result.EventID, &result.Status); err != nil {
			return false, err
		}
		targetID = result.ID.String()
		_, err := s.client.InsertTx(ctx, tx, Args{CloseoutID: result.ID.String()}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}})
		return true, err
	}); err != nil {
		return Closeout{}, err
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, eventID, closeoutID string) (Closeout, error) {
	if actor.Role != "admin" {
		return Closeout{}, errors.New("event closeout: admin role required")
	}
	eventUUID, closeoutUUID, err := parseIDs(eventID, closeoutID)
	if err != nil {
		return Closeout{}, err
	}
	var result Closeout
	err = s.pool.QueryRow(ctx, `SELECT id, event_id, status, total_records, error_summary FROM event_closeouts WHERE id = $1 AND event_id = $2`, closeoutUUID, eventUUID).Scan(&result.ID, &result.EventID, &result.Status, &result.TotalRecords, &result.ErrorSummary)
	if errors.Is(err, pgx.ErrNoRows) {
		return Closeout{}, errors.New("event closeout: closeout not found")
	}
	return result, err
}

func (s *Service) Download(ctx context.Context, actor Actor, eventID, closeoutID string) (string, error) {
	result, err := s.Get(ctx, actor, eventID, closeoutID)
	if err != nil {
		return "", err
	}
	if result.Status != "completed" {
		return "", errors.New("event closeout: report is not complete")
	}
	var output string
	err = s.pool.QueryRow(ctx, `SELECT report_csv FROM event_closeouts WHERE id = $1`, result.ID).Scan(&output)
	return output, err
}

func (s *Service) Run(ctx context.Context, closeoutID uuid.UUID) error {
	if s == nil || s.pool == nil || s.reconcile == nil {
		return errors.New("event closeout: runtime is incomplete")
	}
	var eventID uuid.UUID
	if err := s.pool.QueryRow(ctx, `UPDATE event_closeouts SET status = 'processing', started_at = COALESCE(started_at, now()), updated_at = now() WHERE id = $1 AND status IN ('queued', 'processing') RETURNING event_id`, closeoutID).Scan(&eventID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var status string
			if e := s.pool.QueryRow(ctx, `SELECT status FROM event_closeouts WHERE id = $1`, closeoutID).Scan(&status); e == nil && (status == "completed" || status == "failed") {
				return nil
			}
		}
		return err
	}
	// The periodic end-of-day sweep owns revision 1. Closeout deliberately uses
	// a new revision so an operator request rechecks the final archived state
	// rather than accepting an earlier sweep's snapshot as the report boundary.
	if _, err := s.reconcile.Run(ctx, eventID, 2); err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, `SELECT s.roll_no, s.name, c.name, r.slot, r.od_date, r.status FROM od_records r JOIN students s ON s.id = r.student_id JOIN categories c ON c.id = r.category_id WHERE r.event_id = $1 ORDER BY r.od_date ASC, r.slot ASC, s.roll_no ASC, r.id ASC`, eventID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var output strings.Builder
	writer := csv.NewWriter(&output)
	_ = writer.Write([]string{"roll_no", "student_name", "category", "slot", "od_date", "status"})
	var total int64
	for rows.Next() {
		var roll, name, category, status string
		var slot int32
		var date time.Time
		if err := rows.Scan(&roll, &name, &category, &slot, &date, &status); err != nil {
			return err
		}
		_ = writer.Write([]string{roll, name, category, strconv.Itoa(int(slot)), date.Format("2006-01-02"), status})
		total++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	account, role, table, target := "event-closeout", "system", "event_closeouts", closeoutID.String()
	audit := store.AuditEvent{Action: domain.ActionEventCloseoutCompleted, ActorAccount: &account, ActorRole: &role, TargetTable: &table, TargetID: &target, PayloadFunc: func() ([]byte, error) {
		return json.Marshal(map[string]any{"event_id": eventID.String(), "total_records": total})
	}}
	return s.audit.WithAudit(ctx, audit, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		result, err := tx.Exec(ctx, `UPDATE event_closeouts SET status = 'completed', total_records = $2, report_csv = $3, completed_at = now(), updated_at = now() WHERE id = $1 AND status = 'processing'`, closeoutID, total, output.String())
		return result.RowsAffected() == 1, err
	})
}

func parseIDs(eventID, closeoutID string) (uuid.UUID, uuid.UUID, error) {
	e, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("event closeout: invalid event id: %w", err)
	}
	c, err := uuid.Parse(strings.TrimSpace(closeoutID))
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("event closeout: invalid closeout id: %w", err)
	}
	return e, c, nil
}
