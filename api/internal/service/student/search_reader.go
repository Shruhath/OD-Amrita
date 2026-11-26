package student

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// SQLCSearchReader runs the bounded roster search. It is a separate reader from
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
	// Refused rather than passed through: `~~ ALL` over an empty array matches
	// every row, so an empty Patterns would turn a search into a roster dump.
	// buildSearchQuery cannot produce one — this is the seam that stops a future
	// caller constructing a SearchQuery by hand from doing it anyway.
	if len(query.Patterns) == 0 {
		return nil, errors.New("student: search query carries no terms")
	}
	// The seed must be one OF the patterns, or the redundant index predicate
	// stops being redundant and starts silently narrowing the result. Guarded at
	// the same seam and for the same reason as the empty-patterns check above:
	// buildSearchQuery always sets it, so this catches a hand-built SearchQuery.
	seed := query.SeedPattern
	if seed == "" {
		seed = query.Patterns[0]
	}
	rows, err := r.queries.SearchStudents(ctx, sqlc.SearchStudentsParams{
		Patterns:    query.Patterns,
		SeedPattern: seed,
		HeadPattern: query.HeadPattern,
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
