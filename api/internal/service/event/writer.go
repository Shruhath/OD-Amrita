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
	SetCategoryPublicVisibility(context.Context, sqlc.SetCategoryPublicVisibilityParams) (sqlc.SetCategoryPublicVisibilityRow, error)
	LockCoordinatorAssignmentUser(context.Context, pgtype.UUID) (sqlc.LockCoordinatorAssignmentUserRow, error)
	PromoteUserToCoordinator(context.Context, pgtype.UUID) (int64, error)
	InsertEventCoordinatorAssignment(context.Context, sqlc.InsertEventCoordinatorAssignmentParams) (sqlc.InsertEventCoordinatorAssignmentRow, error)
	AssignUserCategory(context.Context, sqlc.AssignUserCategoryParams) (sqlc.UserCategoryAssignment, error)
	UpsertCaptureCreditConfig(context.Context, sqlc.UpsertCaptureCreditConfigParams) (sqlc.UpsertCaptureCreditConfigRow, error)
	ResetCaptureCreditUsage(context.Context, sqlc.ResetCaptureCreditUsageParams) (sqlc.ResetCaptureCreditUsageRow, error)
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
	EndDate   time.Time
	BranchIDs []uuid.UUID
}

func (w *SQLCWriter) Create(ctx context.Context, tx pgx.Tx, input CreateWrite) (uuid.UUID, bool, error) {
	q := w.queries(tx)
	row, err := q.CreateEvent(ctx, sqlc.CreateEventParams{
		Name:      input.Name,
		EventDate: pgtype.Date{Time: input.EventDate, Valid: true},
		EndDate:   pgtype.Date{Time: input.EndDate, Valid: true},
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
	category, mapErr := mapCategory(row.ID, row.EventID, row.Name, row.VisibleOnPublicDashboard, row.CreatedAt)
	if mapErr != nil {
		return Category{}, false, mapErr
	}
	return category, true, nil
}

func (w *SQLCWriter) UpdateCategoryVisibility(ctx context.Context, tx pgx.Tx, categoryID uuid.UUID, visible bool) (Category, error) {
	row, err := w.queries(tx).SetCategoryPublicVisibility(ctx, sqlc.SetCategoryPublicVisibilityParams{
		CategoryID:               dbUUID(categoryID),
		VisibleOnPublicDashboard: visible,
	})
	if err != nil {
		return Category{}, err
	}
	return mapCategory(row.ID, row.EventID, row.Name, row.VisibleOnPublicDashboard, row.CreatedAt)
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
	categories := make([]Category, 0, len(input.CategoryIDs))
	for _, categoryID := range input.CategoryIDs {
		if _, err := q.AssignUserCategory(ctx, sqlc.AssignUserCategoryParams{
			UserID:          dbUUID(input.UserID),
			CategoryID:      dbUUID(categoryID),
			CreatedByUserID: dbUUID(input.CreatedByUserID),
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return CoordinatorAssignment{}, false, err
		}
		categories = append(categories, Category{ID: categoryID.String(), EventID: input.EventID.String()})
	}
	assignment, mapErr := mapCoordinatorAssignment(inserted.ID, inserted.UserID, user.Email, user.Name, inserted.EventID, categories, inserted.CreatedAt)
	if mapErr != nil {
		return CoordinatorAssignment{}, false, mapErr
	}
	return assignment, true, nil
}

func (w *SQLCWriter) UpsertCaptureCreditConfig(ctx context.Context, tx pgx.Tx, input CaptureCreditConfigWrite) (CaptureCreditConfigWriteResult, bool, error) {
	row, err := w.queries(tx).UpsertCaptureCreditConfig(ctx, sqlc.UpsertCaptureCreditConfigParams{
		EventID:         dbUUID(input.EventID),
		CreditLimit:     int32(input.Limit),
		UpdatedByUserID: dbUUID(input.UpdatedByUserID),
	})
	if err != nil {
		return CaptureCreditConfigWriteResult{}, false, err
	}
	config, mapErr := mapCaptureCreditConfig(row.EventID, row.CreditLimit, row.UpdatedByUserID, row.UpdatedAt)
	if mapErr != nil {
		return CaptureCreditConfigWriteResult{}, false, mapErr
	}
	return CaptureCreditConfigWriteResult{Config: config, PreviousLimit: int(row.PreviousLimit)}, true, nil
}

func (w *SQLCWriter) ResetCaptureCreditUsage(ctx context.Context, tx pgx.Tx, input CaptureCreditResetWrite) (CaptureCreditResetWriteResult, bool, error) {
	row, err := w.queries(tx).ResetCaptureCreditUsage(ctx, sqlc.ResetCaptureCreditUsageParams{
		EventID:   dbUUID(input.EventID),
		UserID:    dbUUID(input.UserID),
		UsageDate: dbDate(input.UsageDate),
	})
	if err == nil {
		return CaptureCreditResetWriteResult{PreviousConsumed: int(row.PreviousConsumed)}, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return CaptureCreditResetWriteResult{}, false, ErrCoordinatorUserNotFound
	}
	return CaptureCreditResetWriteResult{}, false, err
}

func dbDate(day time.Time) pgtype.Date {
	return pgtype.Date{Time: day, Valid: true}
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
