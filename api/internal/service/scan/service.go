// Package scan owns the authenticated batch transport used by the web offline
// queue. It deliberately delegates attendance truth to od_record.Service.
package scan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

type Item struct {
	ID          string
	EventID     string
	CategoryID  string
	Method      string
	StudentID   *string
	CardToken   *string
	CardVersion *string
}

type Actor = odsvc.Actor

type Marker interface {
	Mark(context.Context, odsvc.Actor, odsvc.MarkInput) (odsvc.MarkResult, error)
}

type Service struct {
	marker Marker
	pool   *pgxpool.Pool
}

func NewService(marker Marker, pool *pgxpool.Pool) *Service {
	return &Service{marker: marker, pool: pool}
}

type Outcome struct {
	Status string
	Reason string
}

func (s *Service) Submit(ctx context.Context, actor Actor, item Item) (Outcome, error) {
	id, err := uuid.Parse(strings.TrimSpace(item.ID))
	if err != nil || id.Version() != 7 {
		return Outcome{Status: "rejected", Reason: string(contracts.ErrorCodeINVALIDSCANID)}, nil
	}
	if s.pool == nil {
		return Outcome{}, errors.New("scan: database pool is nil")
	}
	var seen bool
	err = s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM scan_idempotency_keys
			WHERE scan_id = $1
		)`, id).Scan(&seen)
	if err != nil {
		return Outcome{}, fmt.Errorf("scan: check idempotency key: %w", err)
	}
	if seen {
		return Outcome{Status: "duplicate"}, nil
	}

	input := odsvc.MarkInput{
		EventID:       item.EventID,
		CategoryID:    item.CategoryID,
		CaptureMethod: odsvc.CaptureMethod(item.Method),
		StudentID:     item.StudentID,
	}
	if item.CardToken != nil {
		input.Card = &odsvc.CardInput{Token: *item.CardToken}
		if item.CardVersion != nil {
			input.Card.Version = *item.CardVersion
		}
	}
	_, markErr := s.marker.Mark(ctx, actor, input)
	if markErr != nil {
		var domainErr domain.DomainError
		if errors.As(markErr, &domainErr) && domainErr.Code == contracts.ErrorCodeSCANDUPLICATE {
			if err := s.remember(ctx, id, actor.UserID); err != nil {
				return Outcome{}, err
			}
			return Outcome{Status: "duplicate"}, nil
		}
		if errors.As(markErr, &domainErr) {
			return Outcome{Status: "rejected", Reason: string(domainErr.Code)}, nil
		}
		return Outcome{Status: "rejected", Reason: reasonFor(markErr)}, nil
	}
	if err := s.remember(ctx, id, actor.UserID); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: "applied"}, nil
}

func (s *Service) remember(ctx context.Context, id uuid.UUID, actorID string) error {
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return fmt.Errorf("scan: invalid actor id: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO scan_idempotency_keys (scan_id, actor_user_id)
		VALUES ($1, $2)
		ON CONFLICT (scan_id) DO NOTHING`, id, actor)
	return err
}

func reasonFor(err error) string {
	message := err.Error()
	for _, code := range []contracts.ErrorCode{
		contracts.ErrorCodeVALIDATIONFAILED,
		contracts.ErrorCodeRBACFORBIDDEN,
		contracts.ErrorCodeEVENTNOTFOUND,
		contracts.ErrorCodeEVENTARCHIVED,
		contracts.ErrorCodeCATEGORYNOTFOUND,
		contracts.ErrorCodeSTUDENTNOTFOUND,
		contracts.ErrorCodeCARDNOTRECOGNIZED,
		contracts.ErrorCodeSLOTCLOSED,
		contracts.ErrorCodeCAPTURECREDITLIMITREACHED,
	} {
		if strings.Contains(message, string(code)) {
			return string(code)
		}
	}
	return string(contracts.ErrorCodeINTERNAL)
}
