package student

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type studentWriteQueries interface {
	UpsertStudent(context.Context, sqlc.UpsertStudentParams) (sqlc.UpsertStudentRow, error)
	CountStudents(context.Context) (int32, error)
	InsertStudentRosterImport(context.Context, sqlc.InsertStudentRosterImportParams) (pgtype.UUID, error)
}

type queryFactory func(pgx.Tx) studentWriteQueries

type SQLCWriter struct {
	queries queryFactory
}

func NewSQLCWriter() *SQLCWriter {
	return newSQLCWriter(func(tx pgx.Tx) studentWriteQueries { return sqlc.New(tx) })
}

func newSQLCWriter(factory queryFactory) *SQLCWriter {
	return &SQLCWriter{queries: factory}
}

func (w *SQLCWriter) Import(ctx context.Context, tx pgx.Tx, input ImportWrite) (Result, error) {
	q := w.queries(tx)
	result := Result{TotalRows: input.TotalRows, InvalidRows: input.InvalidRows, Errors: input.Errors}

	for _, row := range input.Rows {
		branchID, err := uuid.Parse(row.BranchID)
		if err != nil {
			return Result{}, fmt.Errorf("student: invalid branch id: %w", err)
		}
		upserted, err := q.UpsertStudent(ctx, sqlc.UpsertStudentParams{
			RollNo:   row.RollNo,
			Name:     row.Name,
			BranchID: dbUUID(branchID),
			Section:  row.Section,
			Batch:    row.Batch,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			result.UnchangedRows++
			continue
		}
		if err != nil {
			return Result{}, err
		}
		if !upserted.ID.Valid {
			return Result{}, errors.New("student: upsert returned invalid id")
		}
		if upserted.Inserted {
			result.InsertedRows++
		} else {
			result.UpdatedRows++
		}
	}

	count, err := q.CountStudents(ctx)
	if err != nil {
		return Result{}, err
	}
	result.DatabaseCount = int(count)

	actorID := pgtype.UUID{}
	if parsed, parseErr := uuid.Parse(input.Actor.UserID); parseErr == nil {
		actorID = dbUUID(parsed)
	}
	importID, err := q.InsertStudentRosterImport(ctx, sqlc.InsertStudentRosterImportParams{
		SourceFilename: input.SourceFilename,
		TotalRows:      int32(result.TotalRows),
		InsertedRows:   int32(result.InsertedRows),
		UpdatedRows:    int32(result.UpdatedRows),
		UnchangedRows:  int32(result.UnchangedRows),
		InvalidRows:    int32(result.InvalidRows),
		DatabaseCount:  int32(result.DatabaseCount),
		ActorUserID:    actorID,
		ActorEmail:     textOrNull(input.Actor.Email),
	})
	if err != nil {
		return Result{}, err
	}
	if !importID.Valid {
		return Result{}, errors.New("student: import summary returned invalid id")
	}
	result.ImportID = uuid.UUID(importID.Bytes).String()
	return result, nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func textOrNull(value string) pgtype.Text {
	value = strings.TrimSpace(value)
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}
