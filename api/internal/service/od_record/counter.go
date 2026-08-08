package odrecord

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// SQLCCounter reads the two FR13 counters in a single round trip.
type SQLCCounter struct {
	queries *sqlc.Queries
}

func NewSQLCCounter(pool *pgxpool.Pool) *SQLCCounter {
	return &SQLCCounter{queries: sqlc.New(pool)}
}

// CountMarks returns (mine, categoryTotal) FOR ONE DAY.
//
// Both figures are read by a coordinator as "today". On a thirty-day festival an
// unscoped count would show a running cumulative total under a label that says
// otherwise — the first number to mislead would be the one they use to decide
// whether the queue has been cleared.
//
// An unparseable actor id yields a zero `mine` rather than an error: the actor
// arrives from the authenticated principal, so this is a defensive branch, and
// the combined total is still worth showing if it ever fires.
func (c *SQLCCounter) CountMarks(ctx context.Context, actorUserID, eventID, categoryID string, odDate time.Time) (int, int, error) {
	event, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return 0, 0, err
	}
	category, err := uuid.Parse(strings.TrimSpace(categoryID))
	if err != nil {
		return 0, 0, err
	}

	var actor pgtype.UUID
	if parsed, parseErr := uuid.Parse(strings.TrimSpace(actorUserID)); parseErr == nil {
		actor = dbUUID(parsed)
	}

	row, err := c.queries.CountMarksForCategory(ctx, sqlc.CountMarksForCategoryParams{
		ActorUserID: actor,
		EventID:     dbUUID(event),
		CategoryID:  dbUUID(category),
		OdDate:      dbDate(odDate),
	})
	if err != nil {
		return 0, 0, err
	}
	return int(row.Mine), int(row.CategoryTotal), nil
}
