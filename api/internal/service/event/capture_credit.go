package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

type CaptureCreditConfig struct {
	EventID         string
	Limit           int
	UpdatedByUserID *string
	UpdatedAt       time.Time
}

type CaptureCreditUsage struct {
	EventID   string
	UserID    string
	Email     string
	Name      *string
	Limit     int
	Consumed  int
	Remaining int
	Exhausted bool
}

type CaptureCreditSummary struct {
	Config CaptureCreditConfig
	Data   []CaptureCreditUsage
}

type CaptureCreditConfigInput struct {
	EventID string
	Limit   int
}

type CaptureCreditConfigWrite struct {
	EventID         uuid.UUID
	Limit           int
	UpdatedByUserID uuid.UUID
}

type CaptureCreditResetWrite struct {
	EventID uuid.UUID
	UserID  uuid.UUID
}

type CaptureCreditReader interface {
	GetCaptureCreditConfig(context.Context, uuid.UUID) (CaptureCreditConfig, error)
	ListCaptureCreditUsageByEvent(context.Context, uuid.UUID) ([]CaptureCreditUsage, error)
	GetCaptureCreditUsageForCoordinator(context.Context, uuid.UUID, uuid.UUID) (CaptureCreditUsage, error)
}

type CaptureCreditWriter interface {
	UpsertCaptureCreditConfig(context.Context, pgx.Tx, CaptureCreditConfigWrite) (CaptureCreditConfig, bool, error)
	ResetCaptureCreditUsage(context.Context, pgx.Tx, CaptureCreditResetWrite) (bool, error)
}

var errCaptureCreditStoreMissing = errors.New("event: capture credit store missing")

func (s *Service) ListCaptureCredits(ctx context.Context, actor Actor, rawEventID string) (CaptureCreditSummary, error) {
	if s.creditReader == nil {
		return CaptureCreditSummary{}, errCaptureCreditStoreMissing
	}
	if actor.Role != "admin" {
		return CaptureCreditSummary{}, forbidden()
	}
	eventID, err := parseCoordinatorEventID(rawEventID)
	if err != nil {
		return CaptureCreditSummary{}, err
	}
	if _, err := s.eventStatus(ctx, eventID); err != nil {
		return CaptureCreditSummary{}, err
	}
	config, err := s.creditReader.GetCaptureCreditConfig(ctx, eventID)
	if err != nil {
		return CaptureCreditSummary{}, err
	}
	rows, err := s.creditReader.ListCaptureCreditUsageByEvent(ctx, eventID)
	if err != nil {
		return CaptureCreditSummary{}, err
	}
	return CaptureCreditSummary{Config: config, Data: rows}, nil
}

func (s *Service) UpdateCaptureCreditConfig(ctx context.Context, actor Actor, input CaptureCreditConfigInput) (CaptureCreditConfig, error) {
	if s.creditReader == nil || s.creditWriter == nil {
		return CaptureCreditConfig{}, errCaptureCreditStoreMissing
	}
	if actor.Role != "admin" {
		return CaptureCreditConfig{}, forbidden()
	}
	eventID, err := parseCoordinatorEventID(input.EventID)
	if err != nil {
		return CaptureCreditConfig{}, err
	}
	if input.Limit < 0 {
		return CaptureCreditConfig{}, coordinatorValidation("limit", "minimum", "Limit must be at least 0.")
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return CaptureCreditConfig{}, fmt.Errorf("event: invalid principal id: %w", err)
	}
	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return CaptureCreditConfig{}, err
	}
	if status == StatusArchived {
		return CaptureCreditConfig{}, eventArchived()
	}
	previous, err := s.creditReader.GetCaptureCreditConfig(ctx, eventID)
	if err != nil {
		return CaptureCreditConfig{}, err
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "event_capture_credit_config"
	targetID := eventID.String()
	var updated CaptureCreditConfig
	evt := store.AuditEvent{
		Action:       domain.ActionCaptureCreditCapChanged,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		PayloadFunc: func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"event_id":       targetID,
				"actor_user_id":  actorID.String(),
				"previous_limit": previous.Limit,
				"new_limit":      updated.Limit,
			})
		},
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		row, changed, writeErr := s.creditWriter.UpsertCaptureCreditConfig(ctx, tx, CaptureCreditConfigWrite{
			EventID:         eventID,
			Limit:           input.Limit,
			UpdatedByUserID: actorID,
		})
		if writeErr != nil {
			return false, writeErr
		}
		updated = row
		return changed, nil
	})
	if err != nil {
		return CaptureCreditConfig{}, mapCaptureCreditWriteError(err)
	}
	return updated, nil
}

func (s *Service) ResetCaptureCredits(ctx context.Context, actor Actor, rawEventID, rawUserID string) (CaptureCreditUsage, error) {
	if s.creditReader == nil || s.creditWriter == nil {
		return CaptureCreditUsage{}, errCaptureCreditStoreMissing
	}
	if actor.Role != "admin" {
		return CaptureCreditUsage{}, forbidden()
	}
	eventID, err := parseCoordinatorEventID(rawEventID)
	if err != nil {
		return CaptureCreditUsage{}, err
	}
	userID, err := uuid.Parse(strings.TrimSpace(rawUserID))
	if err != nil {
		return CaptureCreditUsage{}, coordinatorValidation("user_id", "invalid_format", "User id must be a UUID.")
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return CaptureCreditUsage{}, fmt.Errorf("event: invalid principal id: %w", err)
	}
	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return CaptureCreditUsage{}, err
	}
	if status == StatusArchived {
		return CaptureCreditUsage{}, eventArchived()
	}
	previous, err := s.creditReader.GetCaptureCreditUsageForCoordinator(ctx, eventID, userID)
	if err != nil {
		if errors.Is(err, ErrCoordinatorUserNotFound) {
			return CaptureCreditUsage{}, coordinatorUserNotFound()
		}
		return CaptureCreditUsage{}, err
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "event_capture_credit_usage"
	targetID := eventID.String() + ":" + userID.String()
	evt := store.AuditEvent{
		Action:       domain.ActionCaptureCreditReset,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		PayloadFunc: func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"event_id":          eventID.String(),
				"user_id":           userID.String(),
				"actor_user_id":     actorID.String(),
				"previous_consumed": previous.Consumed,
				"new_consumed":      0,
			})
		},
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return s.creditWriter.ResetCaptureCreditUsage(ctx, tx, CaptureCreditResetWrite{EventID: eventID, UserID: userID})
	})
	if err != nil {
		return CaptureCreditUsage{}, mapCaptureCreditWriteError(err)
	}
	return s.creditReader.GetCaptureCreditUsageForCoordinator(ctx, eventID, userID)
}

func mapCaptureCreditWriteError(err error) error {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "55000" {
		return eventArchived()
	}
	return err
}
