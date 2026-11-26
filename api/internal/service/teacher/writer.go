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
	AddUserBranchesBulk(context.Context, sqlc.AddUserBranchesBulkParams) (int64, error)
	AssignUserCategoriesBulk(context.Context, sqlc.AssignUserCategoriesBulkParams) (int64, error)
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

	// One statement for every branch, rather than a round trip each. The counts
	// are small, but this runs inside the transaction that creates the teacher,
	// against a database half a second away, so an administrator waits on every
	// one of them.
	//
	// The row-count assertion is preserved exactly: the loop demanded one row per
	// branch, so the bulk insert must affect exactly as many rows as there were
	// branch ids. user_branches has no ON CONFLICT clause, so a duplicate id in
	// the request still raises rather than being silently absorbed.
	if len(input.BranchIDs) > 0 {
		branchIDs := make([]pgtype.UUID, 0, len(input.BranchIDs))
		for _, branchID := range input.BranchIDs {
			branchIDs = append(branchIDs, dbUUID(branchID))
		}
		rows, err := q.AddUserBranchesBulk(ctx, sqlc.AddUserBranchesBulkParams{
			UserID:    row.ID,
			BranchIds: branchIDs,
		})
		if err != nil {
			return "", false, err
		}
		if rows != int64(len(branchIDs)) {
			return "", false, fmt.Errorf("teacher: branch association affected %d rows, want %d", rows, len(branchIDs))
		}
	}

	if err := assignCategories(ctx, q, row.ID, input.CategoryIDs, input.ActorUserID); err != nil {
		return "", false, err
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
	if err := assignCategories(ctx, q, dbUUID(input.UserID), input.CategoryIDs, input.ActorUserID); err != nil {
		return false, err
	}
	return true, nil
}

// assignCategories grants every category in one statement.
//
// Shared by the create path and the replace path because they were already the
// same loop written twice, and the bulk form is the place where letting them
// drift would be least visible.
//
// It does NOT assert a row count. AssignUserCategoriesBulk carries
// ON CONFLICT DO NOTHING, so the rows affected are the ones NEWLY granted, not
// the ones requested — re-granting a category the user already holds is a
// legitimate no-op, and the per-row loop treated it as one too (it swallowed
// pgx.ErrNoRows for exactly that reason).
func assignCategories(
	ctx context.Context, q teacherWriteQueries,
	userID pgtype.UUID, categoryIDs []uuid.UUID, actorUserID uuid.UUID,
) error {
	if len(categoryIDs) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(categoryIDs))
	for _, categoryID := range categoryIDs {
		ids = append(ids, dbUUID(categoryID))
	}
	_, err := q.AssignUserCategoriesBulk(ctx, sqlc.AssignUserCategoriesBulkParams{
		UserID:          userID,
		CategoryIds:     ids,
		CreatedByUserID: dbUUID(actorUserID),
	})
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	return nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
