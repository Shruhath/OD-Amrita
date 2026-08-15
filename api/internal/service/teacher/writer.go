package teacher

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type teacherWriteQueries interface {
	AssignTeacherRole(context.Context, pgtype.UUID) (sqlc.AssignTeacherRoleRow, error)
	AddUserBranch(context.Context, sqlc.AddUserBranchParams) (int64, error)
	AssignUserCategory(context.Context, sqlc.AssignUserCategoryParams) (sqlc.UserCategoryAssignment, error)
	DeleteUserCategoriesExcept(context.Context, sqlc.DeleteUserCategoriesExceptParams) (int64, error)
}

type queryFactory func(pgx.Tx) teacherWriteQueries

// SQLCWriter binds every teacher-profile mutation to the transaction supplied
// by the shared audited write boundary.
type SQLCWriter struct {
	queries queryFactory
}

func NewSQLCWriter() *SQLCWriter {
	return newSQLCWriter(func(tx pgx.Tx) teacherWriteQueries { return sqlc.New(tx) })
}

func newSQLCWriter(factory queryFactory) *SQLCWriter {
	return &SQLCWriter{queries: factory}
}

// AssignRoleAndBranches elevates the user's role (student -> teacher) and
// associates every branch, in that order, inside the audited transaction. The
// AssignTeacherRole AND role = 'student' guard is the real race backstop: a
// concurrent second elevation returns pgx.ErrNoRows, translated to
// ErrRoleAlreadyAssigned — the service maps that to the same canonical
// conflict its own pre-check produces.
func (w *SQLCWriter) AssignRoleAndBranches(ctx context.Context, tx pgx.Tx, input AssignWrite) (string, bool, error) {
	q := w.queries(tx)
	row, err := q.AssignTeacherRole(ctx, dbUUID(input.UserID))
	if err == pgx.ErrNoRows {
		return "", false, ErrRoleAlreadyAssigned
	}
	if err != nil {
		return "", false, err
	}
	if !row.ID.Valid {
		return "", false, fmt.Errorf("teacher: assign role returned an invalid id")
	}

	for _, branchID := range input.BranchIDs {
		rows, err := q.AddUserBranch(ctx, sqlc.AddUserBranchParams{
			UserID:   row.ID,
			BranchID: dbUUID(branchID),
		})
		if err != nil {
			return "", false, err
		}
		if rows != 1 {
			return "", false, fmt.Errorf("teacher: branch association affected %d rows", rows)
		}
	}

	for _, categoryID := range input.CategoryIDs {
		if _, err := q.AssignUserCategory(ctx, sqlc.AssignUserCategoryParams{
			UserID:          row.ID,
			CategoryID:      dbUUID(categoryID),
			CreatedByUserID: dbUUID(input.ActorUserID),
		}); err != nil && err != pgx.ErrNoRows {
			return "", false, err
		}
	}

	return uuid.UUID(row.ID.Bytes).String(), true, nil
}

func (w *SQLCWriter) ReplaceCategoryAccess(ctx context.Context, tx pgx.Tx, input ReplaceCategoryAccessWrite) (bool, error) {
	q := w.queries(tx)
	keepIDs := make([]pgtype.UUID, 0, len(input.CategoryIDs))
	for _, categoryID := range input.CategoryIDs {
		keepIDs = append(keepIDs, dbUUID(categoryID))
	}
	if _, err := q.DeleteUserCategoriesExcept(ctx, sqlc.DeleteUserCategoriesExceptParams{
		UserID:          dbUUID(input.UserID),
		KeepCategoryIds: keepIDs,
	}); err != nil {
		return false, err
	}
	for _, categoryID := range input.CategoryIDs {
		if _, err := q.AssignUserCategory(ctx, sqlc.AssignUserCategoryParams{
			UserID:          dbUUID(input.UserID),
			CategoryID:      dbUUID(categoryID),
			CreatedByUserID: dbUUID(input.ActorUserID),
		}); err != nil && err != pgx.ErrNoRows {
			return false, err
		}
	}
	return true, nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
