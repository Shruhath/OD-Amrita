package student

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type SQLCReader struct {
	queries *sqlc.Queries
}

func NewSQLCReader(pool *pgxpool.Pool) *SQLCReader {
	return &SQLCReader{queries: sqlc.New(pool)}
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
