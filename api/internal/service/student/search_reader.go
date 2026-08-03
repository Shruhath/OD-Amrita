package student

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// SQLCSearchReader runs the bounded prefix search. It is a separate reader from
// SQLCReader so a caller that only imports the admin roster paths cannot reach
// the search query by accident, and so wiring the search route without wiring
// this reader fails loudly.
type SQLCSearchReader struct {
	queries *sqlc.Queries
}

func NewSQLCSearchReader(pool *pgxpool.Pool) *SQLCSearchReader {
	return &SQLCSearchReader{queries: sqlc.New(pool)}
}

func (r *SQLCSearchReader) SearchStudents(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	rows, err := r.queries.SearchStudentsByPrefix(ctx, sqlc.SearchStudentsByPrefixParams{
		Pattern:     query.Pattern,
		ExactRoll:   query.ExactRoll,
		ResultLimit: int64(query.Limit),
	})
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		if !row.ID.Valid {
			return nil, errors.New("student: search row has invalid id")
		}
		results = append(results, SearchResult{
			StudentID:   uuid.UUID(row.ID.Bytes).String(),
			RollNo:      row.RollNo,
			DisplayName: row.Name,
			Branch:      row.Branch,
			Section:     row.Section,
			Batch:       row.Batch,
		})
	}
	return results, nil
}
