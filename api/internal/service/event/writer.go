package event

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type eventWriteQueries interface {
	CreateEvent(context.Context, sqlc.CreateEventParams) (sqlc.CreateEventRow, error)
	AddEventBranch(context.Context, sqlc.AddEventBranchParams) (int64, error)
	ArchiveEvent(context.Context, sqlc.ArchiveEventParams) (pgtype.UUID, error)
}

type queryFactory func(pgx.Tx) eventWriteQueries

// SQLCWriter binds every event mutation to the transaction supplied by the
// shared audited write boundary.
type SQLCWriter struct {
	queries queryFactory
}

func NewSQLCWriter() *SQLCWriter {
	return newSQLCWriter(func(tx pgx.Tx) eventWriteQueries { return sqlc.New(tx) })
}

func newSQLCWriter(factory queryFactory) *SQLCWriter {
	return &SQLCWriter{queries: factory}
}

type CreateWrite struct {
	Name      string
	EventDate time.Time
	BranchIDs []uuid.UUID
}

func (w *SQLCWriter) Create(ctx context.Context, tx pgx.Tx, input CreateWrite) (uuid.UUID, bool, error) {
	q := w.queries(tx)
	row, err := q.CreateEvent(ctx, sqlc.CreateEventParams{
		Name:      input.Name,
		EventDate: pgtype.Date{Time: input.EventDate, Valid: true},
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if !row.ID.Valid {
		return uuid.Nil, false, fmt.Errorf("event: create returned an invalid id")
	}

	for _, branchID := range input.BranchIDs {
		rows, err := q.AddEventBranch(ctx, sqlc.AddEventBranchParams{
			EventID:  row.ID,
			BranchID: dbUUID(branchID),
		})
		if err != nil {
			return uuid.Nil, false, err
		}
		if rows != 1 {
			return uuid.Nil, false, fmt.Errorf("event: branch association affected %d rows", rows)
		}
	}

	return uuid.UUID(row.ID.Bytes), true, nil
}

func (w *SQLCWriter) Archive(ctx context.Context, tx pgx.Tx, eventID, actorID uuid.UUID) (bool, error) {
	_, err := w.queries(tx).ArchiveEvent(ctx, sqlc.ArchiveEventParams{
		EventID:          dbUUID(eventID),
		ArchivedByUserID: dbUUID(actorID),
	})
	if err == nil {
		return true, nil
	}
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return false, err
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
