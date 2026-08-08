package odrecord

import (
	"context"
	"errors"
	"strings"

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
}

type writeQueryFactory func(pgx.Tx) odRecordWriteQueries

type SQLCWriter struct {
	queries writeQueryFactory
}

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
		ServerReceivedAt: row.ServerReceivedAt.Time,
	}, nil
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
		// Story 0.12b lets a teacher pick the day, and this is the mapping that
		// makes a bad pick a 400 on the field rather than a 500.
		if pgErr.ConstraintName == "od_records_od_date_event_range_ck" {
			return validation("od_date", "outside_event_range", "The OD date must fall inside the event's date range.")
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
