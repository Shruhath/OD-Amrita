package student

import (
	"context"
	"errors"

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

// ListStudents returns one ordered page of roster rows plus the unpaginated
// total for the same filters.
//
// Ordering is the database's, not the client's: branch code, batch, section,
// canonical roll_no, then id as a total-order tiebreaker. Without the id the
// order would be unstable across pages for rows that tie on every other column,
// which would silently skip or repeat a student mid-population.
func (r *SQLCReader) ListStudents(ctx context.Context, input ListInput) ([]Student, int, error) {
	branchID, err := optionalUUID(input.BranchID)
	if err != nil {
		return nil, 0, err
	}
	offset := int64(input.Page-1) * int64(input.PageSize)

	rows, err := r.queries.ListStudentsForCardPopulation(ctx, sqlc.ListStudentsForCardPopulationParams{
		BranchID:   branchID,
		Batch:      input.Batch,
		Section:    input.Section,
		PageOffset: offset,
		PageSize:   int64(input.PageSize),
	})
	if err != nil {
		return nil, 0, err
	}

	students := make([]Student, 0, len(rows))
	for _, row := range rows {
		if !row.ID.Valid {
			return nil, 0, errors.New("student: roster row has invalid id")
		}
		students = append(students, Student{
			StudentID:   uuid.UUID(row.ID.Bytes).String(),
			RollNo:      row.RollNo,
			DisplayName: row.Name,
			Branch:      row.Branch,
			Section:     row.Section,
			Batch:       row.Batch,
		})
	}

	total, err := r.queries.CountStudentsForCardPopulation(ctx, sqlc.CountStudentsForCardPopulationParams{
		BranchID: branchID,
		Batch:    input.Batch,
		Section:  input.Section,
	})
	if err != nil {
		return nil, 0, err
	}
	return students, int(total), nil
}

// optionalUUID maps the "unfiltered" empty string to a NULL uuid parameter,
// which the query treats as "no branch filter".
func optionalUUID(raw string) (pgtype.UUID, error) {
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func (r *SQLCReader) ResolveBranch(ctx context.Context, code string) (Branch, error) {
	row, err := r.queries.GetBranchByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Branch{}, ErrBranchNotFound
	}
	if err != nil {
		return Branch{}, err
	}
	if !row.ID.Valid {
		return Branch{}, errors.New("student: branch row has invalid id")
	}
	return Branch{ID: uuid.UUID(row.ID.Bytes).String(), Code: row.Code}, nil
}
