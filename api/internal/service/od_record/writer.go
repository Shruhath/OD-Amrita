package odrecord

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

const (
	sqlstateUniqueViolation              = "23505"
	sqlstateForeignKeyViolation          = "23503"
	sqlstateCheckViolation               = "23514"
	sqlstateObjectNotInPrerequisiteState = "55000"
)

type odRecordWriteQueries interface {
	InsertODPresentRecord(context.Context, sqlc.InsertODPresentRecordParams) (sqlc.InsertODPresentRecordRow, error)
	ListExistingPresentMarksForBatch(context.Context, sqlc.ListExistingPresentMarksForBatchParams) ([]sqlc.ListExistingPresentMarksForBatchRow, error)
}

type writeQueryFactory func(pgx.Tx) odRecordWriteQueries

type SQLCWriter struct {
	queries writeQueryFactory
}

// ExistingPresentForBatch is the transaction-scoped duplicate preflight for a
// manual batch. The partial unique index remains the concurrency backstop; this
// read is what gives the portal actionable names before any row is written.
func (w *SQLCWriter) ExistingPresentForBatch(ctx context.Context, tx pgx.Tx, input BatchMarkInput, odDate time.Time) ([]Student, error) {
	eventID, err := uuid.Parse(strings.TrimSpace(input.EventID))
	if err != nil {
		return nil, err
	}
	studentIDs := make([]pgtype.UUID, 0, len(input.StudentIDs))
	for _, raw := range input.StudentIDs {
		studentID, parseErr := uuid.Parse(strings.TrimSpace(raw))
		if parseErr != nil {
			return nil, parseErr
		}
		studentIDs = append(studentIDs, dbUUID(studentID))
	}
	rows, err := w.queries(tx).ListExistingPresentMarksForBatch(ctx, sqlc.ListExistingPresentMarksForBatchParams{
		EventID: eventIDToDB(eventID), StudentIds: studentIDs, Slot: int32(input.Slot), OdDate: pgtype.Date{Time: odDate, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	students := make([]Student, 0, len(rows))
	for _, row := range rows {
		student, mapErr := mapStudent(row.StudentID, row.RollNo, row.Name, row.Branch, row.Section, row.Batch)
		if mapErr != nil {
			return nil, mapErr
		}
		students = append(students, student)
	}
	return students, nil
}

func eventIDToDB(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func NewSQLCWriter() *SQLCWriter {
	return &SQLCWriter{queries: func(tx pgx.Tx) odRecordWriteQueries { return sqlc.New(tx) }}
}

func (w *SQLCWriter) InsertPresent(ctx context.Context, tx pgx.Tx, input RecordInput) (MarkResult, error) {
	studentID, err := uuid.Parse(strings.TrimSpace(input.StudentID))
	if err != nil {
		return MarkResult{}, err
	}
	eventID, err := uuid.Parse(strings.TrimSpace(input.EventID))
	if err != nil {
		return MarkResult{}, err
	}
	categoryID, err := uuid.Parse(strings.TrimSpace(input.CategoryID))
	if err != nil {
		return MarkResult{}, err
	}
	actorUserID, err := uuid.Parse(strings.TrimSpace(input.ActorUserID))
	if err != nil {
		return MarkResult{}, err
	}
	row, err := w.queries(tx).InsertODPresentRecord(ctx, sqlc.InsertODPresentRecordParams{
		StudentID:        dbUUID(studentID),
		EventID:          dbUUID(eventID),
		CategoryID:       dbUUID(categoryID),
		Slot:             int32(input.Slot),
		OdDate:           pgtype.Date{Time: input.ODDate, Valid: true},
		ReasonNote:       dbText(input.ReasonNote),
		CaptureMethod:    string(input.CaptureMethod),
		ActorUserID:      dbUUID(actorUserID),
		ActorRole:        input.ActorRole,
		ServerReceivedAt: pgtype.Timestamptz{Time: input.ServerReceivedAt, Valid: true},
	})
	if err != nil {
		return MarkResult{}, mapPgError(err)
	}
	if !row.ID.Valid || !row.EventID.Valid || !row.CategoryID.Valid || !row.OdDate.Valid || !row.ServerReceivedAt.Valid {
		return MarkResult{}, invalidRowError("od_record")
	}
	return MarkResult{
		RecordID:         uuid.UUID(row.ID.Bytes).String(),
		Student:          input.Student,
		EventID:          uuid.UUID(row.EventID.Bytes).String(),
		CategoryID:       uuid.UUID(row.CategoryID.Bytes).String(),
		Slot:             int(row.Slot),
		ODDate:           row.OdDate.Time,
		CaptureMethod:    CaptureMethod(row.CaptureMethod),
		ReasonNote:       textPtr(row.ReasonNote),
		ServerReceivedAt: row.ServerReceivedAt.Time,
	}, nil
}

func dbText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case sqlstateUniqueViolation:
		// Keyed on od_date since the event became a range. The name is matched as
		// a literal, so renaming the index without changing this line turns the
		// FR11 duplicate path from a 409 into an unmapped 500 — while a test that
		// only asserts "the second write failed" stays green, because the row
		// still fails to insert.
		if pgErr.ConstraintName == "uq_od_records_present_student_event_date_slot" {
			return ErrDuplicatePresentRecord
		}
	case sqlstateCheckViolation:
		// The database's range guard. Unreachable for a coordinator mark by
		// construction — ActiveSlot already proved today is inside the range — but
		// a teacher may pick the day, and this is the mapping that
		// makes a bad pick a 400 on the field rather than a 500.
		if pgErr.ConstraintName == "od_records_od_date_event_range_ck" {
			return validation("od_date", "outside_event_range", "The OD date must fall inside the event's date range.")
		}
		// The manual-entry reason guard. Unreachable while the service and the
		// schema agree on what "manual" means — the service already refuses a
		// note-free search mark from a teacher or admin — so reaching it means
		// they have drifted apart, exactly as they did when admins were given
		// the live marking screen and 0026 had not yet been written.
		//
		// Mapped so that drift costs a 400 naming the field instead of the bare
		// 500 the caller got for every admin card tap until 0026.
		if pgErr.ConstraintName == "od_records_manual_reason_note_ck" {
			return validation("reason_note", "required", "reason_note is required for a manually entered record.")
		}
	case sqlstateObjectNotInPrerequisiteState:
		return ErrEventArchived
	case sqlstateForeignKeyViolation:
		if pgErr.ConstraintName == "od_records_event_id_fkey" {
			return ErrEventNotFound
		}
		if pgErr.ConstraintName == "od_records_category_event_fk" {
			return ErrCategoryNotFound
		}
		if pgErr.ConstraintName == "od_records_student_id_fkey" {
			return ErrStudentNotFound
		}
	}
	return err
}
