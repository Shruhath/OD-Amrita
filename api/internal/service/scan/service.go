// Package scan owns the authenticated batch transport used by the web offline
// queue. It deliberately delegates attendance truth to od_record.Service.
package scan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	postjob "github.com/Shruhath/OD-Amrita/api/internal/job/scan_postprocess"
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
	client *river.Client[pgx.Tx]
}

func NewService(marker Marker, pool *pgxpool.Pool, client *river.Client[pgx.Tx]) *Service {
	return &Service{marker: marker, pool: pool, client: client}
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
	markResult, markErr := s.marker.Mark(ctx, actor, input)
	if markErr != nil {
		var domainErr domain.DomainError
		if errors.As(markErr, &domainErr) && domainErr.Code == contracts.ErrorCodeSCANDUPLICATE {
			return Outcome{Status: "duplicate"}, nil
		}
		if errors.As(markErr, &domainErr) {
			return Outcome{Status: "rejected", Reason: string(domainErr.Code)}, nil
		}
		return Outcome{Status: "rejected", Reason: reasonFor(markErr)}, nil
	}
	if err := s.rememberAndSchedule(ctx, id, actor.UserID, markResult); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: "applied"}, nil
}

func (s *Service) rememberAndSchedule(ctx context.Context, id uuid.UUID, actorID string, result odsvc.MarkResult) error {
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return fmt.Errorf("scan: invalid actor id: %w", err)
	}
	if s.client == nil {
		return errors.New("scan: River client is nil")
	}
	eventID, err := uuid.Parse(result.EventID)
	if err != nil {
		return fmt.Errorf("scan: invalid marked event id: %w", err)
	}
	recordID, err := uuid.Parse(result.RecordID)
	if err != nil {
		return fmt.Errorf("scan: invalid marked record id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO scan_idempotency_keys (scan_id, actor_user_id)
		VALUES ($1, $2)
		ON CONFLICT (scan_id) DO NOTHING`, id, actor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO scan_post_processings (scan_id, event_id, record_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (scan_id) DO NOTHING`, id, eventID, recordID); err != nil {
		return err
	}
	if _, err := s.client.InsertTx(ctx, tx, postjob.Args{ScanID: id.String()}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
		return fmt.Errorf("scan: enqueue post-processing: %w", err)
	}
	return tx.Commit(ctx)
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
