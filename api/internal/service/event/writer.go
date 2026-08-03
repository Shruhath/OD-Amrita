package event

import (
	"context"
	"errors"
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
	CreateCategory(context.Context, sqlc.CreateCategoryParams) (sqlc.CreateCategoryRow, error)
	LockCoordinatorAssignmentUser(context.Context, pgtype.UUID) (sqlc.LockCoordinatorAssignmentUserRow, error)
	PromoteUserToCoordinator(context.Context, pgtype.UUID) (int64, error)
	InsertEventCoordinatorAssignment(context.Context, sqlc.InsertEventCoordinatorAssignmentParams) (sqlc.InsertEventCoordinatorAssignmentRow, error)
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

// CreateCategory inserts one category on the audited transaction and returns the
// row the database actually wrote — id and created_at are server-assigned, so
// the caller must never synthesize them.
func (w *SQLCWriter) CreateCategory(ctx context.Context, tx pgx.Tx, input CreateCategoryWrite) (Category, bool, error) {
	row, err := w.queries(tx).CreateCategory(ctx, sqlc.CreateCategoryParams{
		EventID: dbUUID(input.EventID),
		Name:    input.Name,
	})
	if err != nil {
		return Category{}, false, err
	}
	category, mapErr := mapCategory(row.ID, row.EventID, row.Name, row.CreatedAt)
	if mapErr != nil {
		return Category{}, false, mapErr
	}
	return category, true, nil
}

func (w *SQLCWriter) CreateCoordinatorAssignment(ctx context.Context, tx pgx.Tx, input CreateCoordinatorAssignmentWrite) (CoordinatorAssignment, bool, error) {
	q := w.queries(tx)
	user, err := q.LockCoordinatorAssignmentUser(ctx, dbUUID(input.UserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CoordinatorAssignment{}, false, ErrCoordinatorUserNotFound
		}
		return CoordinatorAssignment{}, false, err
	}
	if user.Role == "student" {
		changed, promoteErr := q.PromoteUserToCoordinator(ctx, dbUUID(input.UserID))
		if promoteErr != nil {
			return CoordinatorAssignment{}, false, promoteErr
		}
		if changed != 1 {
			return CoordinatorAssignment{}, false, ErrCoordinatorRoleAlreadyAssigned
		}
	} else if user.Role != "coordinator" {
		return CoordinatorAssignment{}, false, ErrCoordinatorRoleAlreadyAssigned
	}

	inserted, err := q.InsertEventCoordinatorAssignment(ctx, sqlc.InsertEventCoordinatorAssignmentParams{
		UserID:          dbUUID(input.UserID),
		EventID:         dbUUID(input.EventID),
		CreatedByUserID: dbUUID(input.CreatedByUserID),
	})
	if err != nil {
		return CoordinatorAssignment{}, false, err
	}
	assignment, mapErr := mapCoordinatorAssignment(inserted.ID, inserted.UserID, user.Email, user.Name, inserted.EventID, inserted.CreatedAt)
	if mapErr != nil {
		return CoordinatorAssignment{}, false, mapErr
	}
	return assignment, true, nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
