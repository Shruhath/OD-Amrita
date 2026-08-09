// Package attendanceimport owns the durable CSV import workflow. Uploads are
// stored as bounded job input, while each source row gets its own idempotent
// state so a worker retry cannot create a second attendance record.
package attendanceimport

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	MaxUploadBytes = 10 << 20
	MaxRows        = 50_000
	MaxErrors      = 5_000
)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type CreateInput struct {
	EventID  string
	Filename string
	CSV      []byte
}

type Import struct {
	ID             uuid.UUID `json:"id"`
	EventID        uuid.UUID `json:"event_id"`
	Status         string    `json:"status"`
	TotalRows      int       `json:"total_rows"`
	ProcessedRows  int       `json:"processed_rows"`
	AcceptedRows   int       `json:"accepted_rows"`
	DuplicateRows  int       `json:"duplicate_rows"`
	InvalidRows    int       `json:"invalid_rows"`
	ErrorSummary   *string   `json:"error_summary,omitempty"`
	Errors         []RowError `json:"errors,omitempty"`
}

type RowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Args is the stable River payload. The CSV itself stays in the database.
type Args struct {
	ImportID string `json:"import_id"`
}

func (Args) Kind() string { return "attendance_import" }

type Service struct {
	pool  *pgxpool.Pool
	audit *store.Store
	client *river.Client[pgx.Tx]
}

type rowValidationError struct{ message string }

func (e rowValidationError) Error() string { return e.message }

func invalidRow(message string) error { return rowValidationError{message: message} }

func NewService(pool *pgxpool.Pool, client *river.Client[pgx.Tx]) *Service {
	return &Service{pool: pool, audit: store.New(pool), client: client}
}

func (s *Service) Create(ctx context.Context, actor Actor, input CreateInput) (Import, error) {
	if actor.Role != "admin" {
		return Import{}, errors.New("attendance import: admin role required")
	}
	eventID, err := uuid.Parse(strings.TrimSpace(input.EventID))
	if err != nil {
		return Import{}, fmt.Errorf("attendance import: invalid event id: %w", err)
	}
	if len(input.CSV) == 0 || len(input.CSV) > MaxUploadBytes {
		return Import{}, fmt.Errorf("attendance import: CSV must be between 1 byte and %d bytes", MaxUploadBytes)
	}
	filename := strings.TrimSpace(input.Filename)
	if filename == "" || len(filename) > 255 || !utf8.ValidString(filename) || strings.ContainsRune(filename, '\x00') {
		return Import{}, errors.New("attendance import: invalid filename")
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return Import{}, fmt.Errorf("attendance import: invalid actor id: %w", err)
	}

	var result Import
	account := strings.ToLower(strings.TrimSpace(actor.Email))
	role := actor.Role
	table := "attendance_imports"
	targetID := ""
	event := store.AuditEvent{
		Action:       domain.ActionAttendanceImportRequested,
		ActorAccount: &account,
		ActorRole:    &role,
		TargetTable:  &table,
		TargetID:     &targetID,
	}
	event.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]any{"event_id": eventID.String(), "source_filename": filename, "bytes": len(input.CSV)})
	}
	if err := s.audit.WithAudit(ctx, event, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		if err := tx.QueryRow(ctx, `
			SELECT id FROM events WHERE id = $1 AND status = 'active'`, eventID).Scan(&eventID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, errors.New("attendance import: event not found or archived")
			}
			return false, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO attendance_imports (event_id, requested_by_user_id, source_filename, source_csv)
			VALUES ($1, $2, $3, $4) RETURNING id, status`, eventID, actorID, filename, string(input.CSV)).
			Scan(&result.ID, &result.Status); err != nil {
			return false, err
		}
		targetID = result.ID.String()
		if s.client == nil {
			return false, errors.New("attendance import: River client is nil")
		}
		if _, err := s.client.InsertTx(ctx, tx, Args{ImportID: result.ID.String()}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
			return false, fmt.Errorf("attendance import: enqueue: %w", err)
		}
		return true, nil
	}); err != nil {
		return Import{}, err
	}
	result.EventID = eventID
	return result, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, eventID, importID string) (Import, error) {
	if actor.Role != "admin" {
		return Import{}, errors.New("attendance import: admin role required")
	}
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return Import{}, fmt.Errorf("attendance import: invalid event id: %w", err)
	}
	importUUID, err := uuid.Parse(strings.TrimSpace(importID))
	if err != nil {
		return Import{}, fmt.Errorf("attendance import: invalid import id: %w", err)
	}
	var result Import
	if err := s.pool.QueryRow(ctx, `
		SELECT id, event_id, status, total_rows, processed_rows, accepted_rows,
		       duplicate_rows, invalid_rows, error_summary
		FROM attendance_imports WHERE id = $1 AND event_id = $2`, importUUID, eventUUID).
		Scan(&result.ID, &result.EventID, &result.Status, &result.TotalRows, &result.ProcessedRows, &result.AcceptedRows, &result.DuplicateRows, &result.InvalidRows, &result.ErrorSummary); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Import{}, errors.New("attendance import: import not found")
		}
		return Import{}, err
	}
	return result, nil
}

func (s *Service) ListErrors(ctx context.Context, actor Actor, eventID, importID string) ([]RowError, error) {
	if actor.Role != "admin" {
		return nil, errors.New("attendance import: admin role required")
	}
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return nil, fmt.Errorf("attendance import: invalid event id: %w", err)
	}
	importUUID, err := uuid.Parse(strings.TrimSpace(importID))
	if err != nil {
		return nil, fmt.Errorf("attendance import: invalid import id: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.row_number, COALESCE(r.field, 'row'), COALESCE(r.error_code, 'invalid'), COALESCE(r.error_message, 'invalid row')
		FROM attendance_import_rows r
		JOIN attendance_imports i ON i.id = r.import_id
		WHERE r.import_id = $1 AND i.event_id = $2 AND r.status = 'invalid'
		ORDER BY r.row_number ASC LIMIT $3`, importUUID, eventUUID, MaxErrors)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RowError, 0)
	for rows.Next() {
		var item RowError
		if err := rows.Scan(&item.Row, &item.Field, &item.Code, &item.Message); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Run processes pending rows and resumes safely after a worker retry.
func (s *Service) Run(ctx context.Context, importID uuid.UUID) error {
	if s == nil || s.pool == nil || s.audit == nil {
		return errors.New("attendance import: runtime is incomplete")
	}
	var csvText, actorRole string
	var actorID uuid.UUID
	var eventID uuid.UUID
	var status string
	if err := s.pool.QueryRow(ctx, `
		UPDATE attendance_imports
		SET status = 'processing', started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'processing')
		RETURNING source_csv, requested_by_user_id, event_id, status`, importID).
		Scan(&csvText, &actorID, &eventID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var finalized string
			if statusErr := s.pool.QueryRow(ctx, `SELECT status FROM attendance_imports WHERE id = $1`, importID).Scan(&finalized); statusErr != nil {
				return fmt.Errorf("attendance import: import %s is missing: %w", importID, statusErr)
			}
			if finalized == "completed" || finalized == "completed_with_errors" || finalized == "failed" {
				return nil
			}
			return fmt.Errorf("attendance import: import %s could not be claimed", importID)
		}
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, actorID).Scan(&actorRole); err != nil {
		return fmt.Errorf("attendance import: load actor: %w", err)
	}

	r := csv.NewReader(strings.NewReader(csvText))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return s.fail(ctx, importID, "CSV header could not be parsed")
	}
	columns, err := mapHeaders(header)
	if err != nil {
		return s.fail(ctx, importID, err.Error())
	}
	for rowNumber := 2; ; rowNumber++ {
		record, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return s.fail(ctx, importID, fmt.Sprintf("CSV row %d could not be parsed", rowNumber))
		}
		if rowNumber-1 > MaxRows {
			return s.fail(ctx, importID, fmt.Sprintf("CSV exceeds %d rows", MaxRows))
		}
		if err := s.processRow(ctx, importID, eventID, actorID, actorRole, rowNumber, record, columns); err != nil {
			return err
		}
	}
	return s.complete(ctx, importID, actorID, actorRole)
}

type columns map[string]int

func mapHeaders(header []string) (columns, error) {
	result := columns{}
	for index, raw := range header {
		name := strings.ToLower(strings.TrimSpace(raw))
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate CSV column %q", name)
		}
		result[name] = index
	}
	for _, required := range []string{"student_id", "roll_no", "category_id", "slot", "od_date", "capture_method"} {
		if required != "student_id" && required != "roll_no" {
			if _, ok := result[required]; !ok {
				return nil, fmt.Errorf("missing CSV column %q", required)
			}
		}
	}
	if _, hasStudent := result["student_id"]; !hasStudent {
		if _, hasRoll := result["roll_no"]; !hasRoll {
			return nil, errors.New("CSV requires student_id or roll_no")
		}
	}
	return result, nil
}

func (s *Service) processRow(ctx context.Context, importID, eventID, actorID uuid.UUID, actorRole string, rowNumber int, record []string, columns columns) error {
	var current string
	if _, err := s.pool.Exec(ctx, `INSERT INTO attendance_import_rows (import_id, row_number) VALUES ($1, $2) ON CONFLICT DO NOTHING`, importID, rowNumber); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE attendance_imports SET total_rows = GREATEST(total_rows, $2), updated_at = now() WHERE id = $1`, importID, rowNumber-1); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT status FROM attendance_import_rows WHERE import_id = $1 AND row_number = $2`, importID, rowNumber).Scan(&current); err != nil {
		return err
	}
	if current != "pending" {
		return nil
	}
	value := func(name string) string {
		index, ok := columns[name]
		if !ok || index >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[index])
	}
	studentRaw, rollNo := value("student_id"), value("roll_no")
	categoryID, slotRaw, dateRaw, method := value("category_id"), value("slot"), value("od_date"), value("capture_method")
	var studentID uuid.UUID
	var err error
	if studentRaw != "" && rollNo != "" {
		err = invalidRow("provide only one of student_id or roll_no")
	}
	slot, slotErr := strconv.Atoi(slotRaw)
	if err == nil && slotErr != nil {
		err = invalidRow("slot must be an integer")
	}
	if err == nil && (slot < 1 || slot > 12) {
		err = invalidRow("slot must be between 1 and 12")
	}
	odDate, dateErr := time.Parse("2006-01-02", dateRaw)
	if err == nil && dateErr != nil {
		err = invalidRow("od_date must be YYYY-MM-DD")
	}
	if err == nil && method != "nfc" && method != "camera" && method != "search" {
		err = invalidRow("capture_method must be nfc, camera, or search")
	}
	if err == nil {
		if err = s.validateEventCategory(ctx, eventID, categoryID, odDate); err == nil {
			studentID, err = s.resolveStudent(ctx, studentRaw, rollNo)
		}
	}
	if err != nil {
		var validationErr rowValidationError
		if errors.As(err, &validationErr) {
			return s.markInvalid(ctx, importID, rowNumber, validationErr.Error())
		}
		return err
	}

	account := "bulk-import"
	role := actorRole
	table := "od_records"
	targetID := ""
	event := store.AuditEvent{Action: domain.ActionODCreated, ActorAccount: &account, ActorRole: &role, TargetTable: &table, TargetID: &targetID}
	event.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]any{"import_id": importID.String(), "source_row": rowNumber, "student_id": studentID.String(), "category_id": categoryID, "slot": slot, "od_date": dateRaw, "capture_method": method})
	}
	return s.audit.WithAudit(ctx, event, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var categoryEventID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT event_id FROM categories WHERE id = $1`, uuid.MustParse(categoryID)).Scan(&categoryEventID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, errors.New("attendance import: category was not found")
			}
			return false, err
		}
		if categoryEventID != eventID {
			return false, errors.New("attendance import: category does not belong to event")
		}
		var recordID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO od_records (student_id, event_id, category_id, slot, od_date, capture_method, actor_user_id, actor_role, server_received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT DO NOTHING RETURNING id`, studentID, eventID, uuid.MustParse(categoryID), slot, pgtype.Date{Time: odDate, Valid: true}, method, actorID, actorRole).Scan(&recordID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, s.markDuplicateTx(ctx, tx, importID, rowNumber)
		}
		if err != nil {
			return false, err
		}
		targetID = recordID.String()
		if _, err := tx.Exec(ctx, `UPDATE attendance_import_rows SET status = 'accepted', record_id = $3, updated_at = now() WHERE import_id = $1 AND row_number = $2`, importID, rowNumber, recordID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE attendance_imports SET processed_rows = processed_rows + 1, accepted_rows = accepted_rows + 1, updated_at = now() WHERE id = $1`, importID); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *Service) resolveStudent(ctx context.Context, studentRaw, rollNo string) (uuid.UUID, error) {
	if studentRaw != "" && rollNo != "" {
		return uuid.Nil, invalidRow("provide only one of student_id or roll_no")
	}
	if studentRaw != "" {
		id, err := uuid.Parse(studentRaw)
		if err != nil {
			return uuid.Nil, invalidRow("student_id must be a UUID")
		}
		var resolved uuid.UUID
		if err := s.pool.QueryRow(ctx, `SELECT id FROM students WHERE id = $1`, id).Scan(&resolved); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, invalidRow("student_id was not found")
			}
			return uuid.Nil, err
		}
		return resolved, nil
	}
	if rollNo == "" {
		return uuid.Nil, invalidRow("student_id or roll_no is required")
	}
	var studentID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM students WHERE roll_no = $1`, rollNo).Scan(&studentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, invalidRow("roll_no was not found")
		}
		return uuid.Nil, err
	}
	return studentID, nil
}

func (s *Service) validateEventCategory(ctx context.Context, eventID uuid.UUID, categoryID string, odDate time.Time) error {
	categoryUUID, err := uuid.Parse(categoryID)
	if err != nil {
		return invalidRow("category_id must be a UUID")
	}
	var status string
	var eventDate, endDate pgtype.Date
	if err := s.pool.QueryRow(ctx, `
		SELECT e.status, e.event_date, e.end_date
		FROM categories c JOIN events e ON e.id = c.event_id
		WHERE c.id = $1 AND c.event_id = $2`, categoryUUID, eventID).Scan(&status, &eventDate, &endDate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidRow("category was not found for event")
		}
		return err
	}
	if status != "active" {
		return invalidRow("event is archived")
	}
	if !eventDate.Valid || !endDate.Valid || odDate.Before(eventDate.Time) || odDate.After(endDate.Time) {
		return invalidRow("od_date is outside the event date range")
	}
	return nil
}

func (s *Service) markInvalid(ctx context.Context, importID uuid.UUID, rowNumber int, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE attendance_import_rows SET status = 'invalid', field = 'row', error_code = 'invalid', error_message = $3, updated_at = now() WHERE import_id = $1 AND row_number = $2`, importID, rowNumber, message)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE attendance_imports SET processed_rows = processed_rows + 1, invalid_rows = invalid_rows + 1, updated_at = now() WHERE id = $1`, importID)
	return err
}

func (s *Service) markDuplicateTx(ctx context.Context, tx pgx.Tx, importID uuid.UUID, rowNumber int) error {
	if _, err := tx.Exec(ctx, `UPDATE attendance_import_rows SET status = 'duplicate', updated_at = now() WHERE import_id = $1 AND row_number = $2`, importID, rowNumber); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE attendance_imports SET processed_rows = processed_rows + 1, duplicate_rows = duplicate_rows + 1, updated_at = now() WHERE id = $1`, importID)
	return err
}

func (s *Service) complete(ctx context.Context, importID, actorID uuid.UUID, actorRole string) error {
	var invalid int
	if err := s.pool.QueryRow(ctx, `SELECT invalid_rows FROM attendance_imports WHERE id = $1`, importID).Scan(&invalid); err != nil {
		return err
	}
	status := "completed"
	if invalid > 0 {
		status = "completed_with_errors"
	}
	account := "bulk-import"
	table := "attendance_imports"
	targetID := importID.String()
	event := store.AuditEvent{Action: domain.ActionAttendanceImportCompleted, ActorAccount: &account, ActorRole: &actorRole, TargetTable: &table, TargetID: &targetID}
	event.PayloadFunc = func() ([]byte, error) { return json.Marshal(map[string]any{"status": status, "invalid_rows": invalid}) }
	return s.audit.WithAudit(ctx, event, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		_, err := tx.Exec(ctx, `UPDATE attendance_imports SET status = $2, completed_at = now(), updated_at = now() WHERE id = $1 AND status = 'processing'`, importID, status)
		return err == nil, err
	})
}

func (s *Service) fail(ctx context.Context, importID uuid.UUID, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE attendance_imports SET status = 'failed', error_summary = $2, completed_at = now(), updated_at = now() WHERE id = $1`, importID, message)
	return err
}
