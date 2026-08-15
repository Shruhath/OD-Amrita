package teacher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type SQLCReader struct {
	queries *sqlc.Queries
}

func NewSQLCReader(pool *pgxpool.Pool) *SQLCReader {
	return &SQLCReader{queries: sqlc.New(pool)}
}

func (r *SQLCReader) GetTeacherByEmail(ctx context.Context, email string) (resolvedUser, error) {
	row, err := r.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return resolvedUser{}, ErrUserNotFound
		}
		return resolvedUser{}, err
	}
	if !row.ID.Valid {
		return resolvedUser{}, errors.New("teacher: user row has invalid id")
	}
	return resolvedUser{ID: uuid.UUID(row.ID.Bytes).String(), Role: row.Role}, nil
}

func (r *SQLCReader) GetTeacherProfile(ctx context.Context, userID uuid.UUID) (TeacherProfile, error) {
	row, err := r.queries.GetTeacherProfile(ctx, dbUUID(userID))
	if err != nil {
		return TeacherProfile{}, err
	}
	return mapTeacherProfile(row.ID, row.Email, row.Name, row.CreatedAt, row.BranchesJson, row.CategoriesJson)
}

func (r *SQLCReader) TeacherExists(ctx context.Context, userID uuid.UUID) (bool, error) {
	return r.queries.TeacherExists(ctx, dbUUID(userID))
}

func (r *SQLCReader) ListTeachers(ctx context.Context, input ListInput) ([]TeacherProfile, int64, error) {
	total, err := r.queries.CountTeachers(ctx)
	if err != nil {
		return nil, 0, err
	}
	offset, withinRange := pageOffset(total, input.Page, input.PageSize)
	if !withinRange {
		return []TeacherProfile{}, total, nil
	}
	rows, err := r.queries.ListTeachers(ctx, sqlc.ListTeachersParams{
		PageOffset: offset,
		PageSize:   int64(input.PageSize),
	})
	if err != nil {
		return nil, 0, err
	}
	teachers := make([]TeacherProfile, 0, len(rows))
	for _, row := range rows {
		profile, mapErr := mapTeacherProfile(row.ID, row.Email, row.Name, row.CreatedAt, row.BranchesJson, row.CategoriesJson)
		if mapErr != nil {
			return nil, 0, mapErr
		}
		teachers = append(teachers, profile)
	}
	return teachers, total, nil
}

func (r *SQLCReader) CountBranchesByIDs(ctx context.Context, branchIDs []uuid.UUID) (int, error) {
	ids := make([]pgtype.UUID, len(branchIDs))
	for index, id := range branchIDs {
		ids[index] = dbUUID(id)
	}
	count, err := r.queries.CountBranchesByIDs(ctx, ids)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func (r *SQLCReader) CountCategoriesByIDs(ctx context.Context, categoryIDs []uuid.UUID) (int, error) {
	ids := make([]pgtype.UUID, len(categoryIDs))
	for index, id := range categoryIDs {
		ids[index] = dbUUID(id)
	}
	count, err := r.queries.CountCategoriesByIDs(ctx, ids)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func pageOffset(total int64, page, pageSize int) (int64, bool) {
	if total <= 0 || page < 1 || pageSize < 1 {
		return 0, false
	}
	pageIndex := int64(page - 1)
	size := int64(pageSize)
	if pageIndex > (total-1)/size {
		return 0, false
	}
	return pageIndex * size, true
}

func mapTeacherProfile(
	id pgtype.UUID,
	email string,
	name pgtype.Text,
	createdAt pgtype.Timestamptz,
	branchesJSON string,
	categoriesJSON string,
) (TeacherProfile, error) {
	if !id.Valid || !createdAt.Valid {
		return TeacherProfile{}, errors.New("teacher: row contains invalid required values")
	}
	var branches []Branch
	if err := json.Unmarshal([]byte(branchesJSON), &branches); err != nil {
		return TeacherProfile{}, fmt.Errorf("teacher: decode branches: %w", err)
	}
	if branches == nil {
		branches = []Branch{}
	}
	var categories []Category
	if err := json.Unmarshal([]byte(categoriesJSON), &categories); err != nil {
		return TeacherProfile{}, fmt.Errorf("teacher: decode categories: %w", err)
	}
	if categories == nil {
		categories = []Category{}
	}
	profile := TeacherProfile{
		UserID:     uuid.UUID(id.Bytes).String(),
		Email:      email,
		Branches:   branches,
		Categories: categories,
		CreatedAt:  createdAt.Time,
	}
	if name.Valid {
		value := name.String
		profile.Name = &value
	}
	return profile, nil
}
