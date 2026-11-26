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
	UpsertStudentsBulk(context.Context, sqlc.UpsertStudentsBulkParams) ([]sqlc.UpsertStudentsBulkRow, error)
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

	// ONE STATEMENT, NOT ONE PER STUDENT.
	//
	// This was a Go loop around UpsertStudent, so a 2,000-row roster was 2,000
	// sequential round trips inside one open transaction. With the database on
	// another continent — roughly 480ms each way — that is a sixteen-minute
	// import holding a transaction the whole time, and it is the shape that made
	// the roster import the slowest operation in the product.
	//
	// The counting is what the loop got for free and this has to reconstruct:
	// the upsert's IS DISTINCT FROM guard means an UNCHANGED row updates nothing
	// and comes back not at all. So the statement returns each affected row's
	// ordinality, and every input position missing from the result is an
	// unchanged row.
	if len(input.Rows) > 0 {
		rollNos := make([]string, 0, len(input.Rows))
		names := make([]string, 0, len(input.Rows))
		branchIDs := make([]pgtype.UUID, 0, len(input.Rows))
		sections := make([]string, 0, len(input.Rows))
		batches := make([]string, 0, len(input.Rows))
		for _, row := range input.Rows {
			branchID, err := uuid.Parse(row.BranchID)
			if err != nil {
				return Result{}, fmt.Errorf("student: invalid branch id: %w", err)
			}
			rollNos = append(rollNos, row.RollNo)
			names = append(names, row.Name)
			branchIDs = append(branchIDs, dbUUID(branchID))
			sections = append(sections, row.Section)
			batches = append(batches, row.Batch)
		}

		upserted, err := q.UpsertStudentsBulk(ctx, sqlc.UpsertStudentsBulkParams{
			RollNos:   rollNos,
			Names:     names,
			BranchIds: branchIDs,
			Sections:  sections,
			Batches:   batches,
		})
		// pgx.ErrNoRows is not an error for a :many query, but the loop this
		// replaced treated it as "nothing changed" and a caller could reasonably
		// still surface it. Every row unchanged is a legitimate re-import of an
		// identical file.
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Result{}, err
		}

		for _, row := range upserted {
			if !row.ID.Valid {
				return Result{}, errors.New("student: upsert returned invalid id")
			}
			if row.Inserted {
				result.InsertedRows++
			} else {
				result.UpdatedRows++
			}
		}
		// Whatever the statement did not report back was left alone.
		result.UnchangedRows += len(input.Rows) - len(upserted)
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
