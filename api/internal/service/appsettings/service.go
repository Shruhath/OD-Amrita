// Package appsettings holds the runtime switches an administrator may flip
// without a deploy.
//
// ONE SWITCH SO FAR — extended slots — and the package is deliberately shaped so
// a second one costs a constant and a method rather than a table.
//
// WHY THE VALUE IS CACHED. It is read on the MARKING path, which is the latency
// that matters most in this system: a coordinator scanning a card is waiting on
// it. The database is a trans-Pacific hop away (the API is in Singapore, Neon in
// Virginia — see the runbook's H7), so an uncached read would add most of a
// second to every scan to answer a question whose answer changes about once a
// term. The cache is short enough that flipping the switch takes effect while
// the administrator is still looking at the screen.
//
// WHY A MISSING ROW IS "OFF" RATHER THAN AN ERROR. The default has to be the
// behaviour production already has. A switch that failed closed by refusing to
// answer would take the marking path down the first time somebody deleted a row;
// a switch that failed OPEN would silently widen what the app accepts. Off is
// the only reading that is safe in both directions.
package appsettings

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// ExtendedSlotsKey admits slots 13..20 — the evening slots — when true.
const ExtendedSlotsKey = "extended_slots_enabled"

// cacheTTL bounds how stale the switch may be.
//
// Ten seconds: long enough that a burst of scans costs one read, short enough
// that an administrator who flips the toggle and walks to a coordinator's phone
// finds it already applied. It is NOT configurable — a knob here would only
// invite somebody to set it high enough to make the toggle look broken.
const cacheTTL = 10 * time.Second

// Service reads and writes the runtime switches.
type Service struct {
	queries *sqlc.Queries
	cache   *cache.TTL[bool]
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{queries: sqlc.New(pool), cache: cache.New[bool](cacheTTL)}
}

// ExtendedSlotsEnabled reports whether slots 13..20 are currently admitted.
//
// A read failure returns the error rather than defaulting: the caller is the
// marking path, and quietly answering "off" during a database blip would tell a
// coordinator mid-event that the slot they are standing in does not exist. A
// MISSING ROW is different and does default to off — see the package comment.
func (s *Service) ExtendedSlotsEnabled(ctx context.Context) (bool, error) {
	return s.cache.Do(ctx, ExtendedSlotsKey, func(ctx context.Context) (bool, error) {
		row, err := s.queries.GetAppSetting(ctx, ExtendedSlotsKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		// ParseBool, not `== "true"`. The column is text and the row can be
		// written by a migration, by this service, or by an operator with psql;
		// accepting only one spelling would make "TRUE" silently mean off.
		enabled, parseErr := strconv.ParseBool(strings.TrimSpace(row.Value))
		if parseErr != nil {
			// A value nobody can read is a misconfiguration, not a preference.
			// Off is the safe reading and it matches the seeded default.
			return false, nil
		}
		return enabled, nil
	})
}

// SetExtendedSlots writes the switch and returns the value now in force.
func (s *Service) SetExtendedSlots(ctx context.Context, enabled bool, actorUserID string) (bool, time.Time, error) {
	actor := pgtype.UUID{}
	if trimmed := strings.TrimSpace(actorUserID); trimmed != "" {
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return false, time.Time{}, domain.DomainError{
				Code:    contracts.ErrorCodeVALIDATIONFAILED,
				Message: "Setting update validation failed.",
				Details: []contracts.ValidationErrorDetail{{
					Field: "actor", Code: "invalid_format", Message: "actor must be a UUID.",
				}},
			}
		}
		actor = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	row, err := s.queries.UpsertAppSetting(ctx, sqlc.UpsertAppSettingParams{
		Key:       ExtendedSlotsKey,
		Value:     strconv.FormatBool(enabled),
		UpdatedBy: actor,
	})
	if err != nil {
		return false, time.Time{}, err
	}

	// INVALIDATED, not updated to the value just written. The two are different
	// when several machines are serving — Fly runs two — and only this one saw
	// the write. Dropping the entry means the next read on THIS machine goes to
	// the database, and the others expire within the ttl; writing the value in
	// would make this machine confident about something it has no more authority
	// on than its neighbours.
	s.cache.Forget(ExtendedSlotsKey)

	var updatedAt time.Time
	if row.UpdatedAt.Valid {
		updatedAt = row.UpdatedAt.Time
	}
	return enabled, updatedAt, nil
}
