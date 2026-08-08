package odrecord

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

var ErrCaptureCreditExhausted = errors.New("odrecord: capture credit exhausted")

const fallbackCreditWarningThreshold = 1

type FallbackCreditState struct {
	Limit            int
	Consumed         int
	Remaining        int
	Exhausted        bool
	WarningThreshold int
}

type CaptureCreditExhaustedError struct {
	State FallbackCreditState
}

func (e CaptureCreditExhaustedError) Error() string { return ErrCaptureCreditExhausted.Error() }
func (e CaptureCreditExhaustedError) Unwrap() error { return ErrCaptureCreditExhausted }

// CreditLedger is the fallback-credit pool, and every method is scoped to a DAY.
//
// The pool refills each day while the CAP stays per event, which is what keeps
// the documented NFC-failure fallback — an admin raising the cap mid-festival —
// a single change covering every remaining day.
type CreditLedger interface {
	Debit(ctx context.Context, tx pgx.Tx, eventID, userID string, usageDate time.Time) (FallbackCreditState, error)
	State(ctx context.Context, eventID, userID string, usageDate time.Time) (FallbackCreditState, error)
}

type SQLCCreditLedger struct {
	queries *sqlc.Queries
}

func NewSQLCCreditLedger(pool *pgxpool.Pool) *SQLCCreditLedger {
	return &SQLCCreditLedger{queries: sqlc.New(pool)}
}

func (l *SQLCCreditLedger) Debit(ctx context.Context, tx pgx.Tx, eventID, userID string, usageDate time.Time) (FallbackCreditState, error) {
	parsedEventID, parsedUserID, err := creditScope(eventID, userID)
	if err != nil {
		return FallbackCreditState{}, err
	}
	q := sqlc.New(tx)
	row, err := q.DebitCaptureCredit(ctx, sqlc.DebitCaptureCreditParams{
		EventID:   dbUUID(parsedEventID),
		UserID:    dbUUID(parsedUserID),
		UsageDate: dbDate(usageDate),
	})
	if err == nil {
		return creditStateFromInts(row.CreditLimit, row.Consumed, row.Remaining, row.Exhausted), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FallbackCreditState{}, mapPgError(err)
	}
	state, stateErr := q.GetCaptureCreditState(ctx, sqlc.GetCaptureCreditStateParams{
		EventID:   dbUUID(parsedEventID),
		UserID:    dbUUID(parsedUserID),
		UsageDate: dbDate(usageDate),
	})
	if stateErr != nil {
		return FallbackCreditState{}, stateErr
	}
	return creditStateFromInts(state.CreditLimit, state.Consumed, state.Remaining, true), CaptureCreditExhaustedError{
		State: creditStateFromInts(state.CreditLimit, state.Consumed, state.Remaining, true),
	}
}

func (l *SQLCCreditLedger) State(ctx context.Context, eventID, userID string, usageDate time.Time) (FallbackCreditState, error) {
	parsedEventID, parsedUserID, err := creditScope(eventID, userID)
	if err != nil {
		return FallbackCreditState{}, err
	}
	row, err := l.queries.GetCaptureCreditState(ctx, sqlc.GetCaptureCreditStateParams{
		EventID:   dbUUID(parsedEventID),
		UserID:    dbUUID(parsedUserID),
		UsageDate: dbDate(usageDate),
	})
	if err != nil {
		return FallbackCreditState{}, err
	}
	return creditStateFromInts(row.CreditLimit, row.Consumed, row.Remaining, row.Exhausted), nil
}

func creditScope(eventID, userID string) (uuid.UUID, uuid.UUID, error) {
	parsedEventID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return uuid.Nil, uuid.Nil, validation("event_id", "invalid_format", "event_id must be a UUID.")
	}
	parsedUserID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return uuid.Nil, uuid.Nil, validation("actor_user_id", "invalid_format", "authenticated user id must be a UUID.")
	}
	return parsedEventID, parsedUserID, nil
}

func dbDate(day time.Time) pgtype.Date {
	return pgtype.Date{Time: day, Valid: true}
}

func creditStateFromInts(limit, consumed, remaining int32, exhausted bool) FallbackCreditState {
	return FallbackCreditState{
		Limit:            int(limit),
		Consumed:         int(consumed),
		Remaining:        int(remaining),
		Exhausted:        exhausted,
		WarningThreshold: fallbackCreditWarningThreshold,
	}
}

func captureCreditLimitReached() domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeCAPTURECREDITLIMITREACHED,
		Message: "Fallback credit limit reached. Use NFC or ask an admin to reset or raise the cap.",
	}
}
