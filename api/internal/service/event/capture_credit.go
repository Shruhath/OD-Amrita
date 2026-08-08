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

	"github.com/Shruhath/OD-Amrita/api/internal/campus"
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

type CaptureCreditConfigWriteResult struct {
	Config        CaptureCreditConfig
	PreviousLimit int
}

type CaptureCreditResetWrite struct {
	EventID uuid.UUID
	UserID  uuid.UUID
	// UsageDate is the day whose pool is being reset. The credit pool is per day
	// from Story 0.12a; a reset with no day would zero one arbitrary row.
	UsageDate time.Time
}

type CaptureCreditResetWriteResult struct {
	PreviousConsumed int
}

// CaptureCreditReader reads the cap and the per-day usage.
//
// GetCaptureCreditConfig takes NO day: the cap is per event and stays that way,
// which is what keeps a mid-festival cap raise one call covering every remaining
// day. The two usage reads take one, because usage is per day.
type CaptureCreditReader interface {
	GetCaptureCreditConfig(context.Context, uuid.UUID) (CaptureCreditConfig, error)
	ListCaptureCreditUsageByEvent(context.Context, uuid.UUID, time.Time) ([]CaptureCreditUsage, error)
	GetCaptureCreditUsageForCoordinator(context.Context, uuid.UUID, uuid.UUID, time.Time) (CaptureCreditUsage, error)
}

type CaptureCreditWriter interface {
	UpsertCaptureCreditConfig(context.Context, pgx.Tx, CaptureCreditConfigWrite) (CaptureCreditConfigWriteResult, bool, error)
	ResetCaptureCreditUsage(context.Context, pgx.Tx, CaptureCreditResetWrite) (CaptureCreditResetWriteResult, bool, error)
}

var errCaptureCreditStoreMissing = errors.New("event: capture credit store missing")

const maxCaptureCreditLimit = 1<<31 - 1

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
	// The admin surface acts on TODAY. A live cap intervention is only ever about
	// the day in progress, and no day parameter is offered on these endpoints: an
	// admin choosing a past day would be asking to change a number that can no
	// longer affect a mark.
	rows, err := s.creditReader.ListCaptureCreditUsageByEvent(ctx, eventID, s.campusToday())
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
	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return CaptureCreditConfig{}, err
	}
	if status == StatusArchived {
		return CaptureCreditConfig{}, eventArchived()
	}
	if input.Limit < 0 {
		return CaptureCreditConfig{}, coordinatorValidation("limit", "minimum", "Limit must be at least 0.")
	}
	if input.Limit > maxCaptureCreditLimit {
		return CaptureCreditConfig{}, coordinatorValidation("limit", "maximum", "Limit must not exceed 2147483647.")
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return CaptureCreditConfig{}, fmt.Errorf("event: invalid principal id: %w", err)
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "event_capture_credit_config"
	targetID := eventID.String()
	var updated CaptureCreditConfig
	var previousLimit int
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
				"previous_limit": previousLimit,
				"new_limit":      updated.Limit,
			})
		},
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		written, changed, writeErr := s.creditWriter.UpsertCaptureCreditConfig(ctx, tx, CaptureCreditConfigWrite{
			EventID:         eventID,
			Limit:           input.Limit,
			UpdatedByUserID: actorID,
		})
		if writeErr != nil {
			return false, writeErr
		}
		updated = written.Config
		previousLimit = written.PreviousLimit
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
	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return CaptureCreditUsage{}, err
	}
	if status == StatusArchived {
		return CaptureCreditUsage{}, eventArchived()
	}
	userID, err := uuid.Parse(strings.TrimSpace(rawUserID))
	if err != nil {
		return CaptureCreditUsage{}, coordinatorValidation("user_id", "invalid_format", "User id must be a UUID.")
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return CaptureCreditUsage{}, fmt.Errorf("event: invalid principal id: %w", err)
	}

	// One campus-date read serves the reset write, its audit payload and the read
	// back below, so all three name the same day.
	usageDate := s.campusToday()

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "event_capture_credit_usage"
	targetID := eventID.String() + ":" + userID.String()
	var previousConsumed int
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
				"usage_date":        usageDate.Format("2006-01-02"),
				"previous_consumed": previousConsumed,
				"new_consumed":      0,
			})
		},
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		written, changed, writeErr := s.creditWriter.ResetCaptureCreditUsage(ctx, tx, CaptureCreditResetWrite{
			EventID:   eventID,
			UserID:    userID,
			UsageDate: usageDate,
		})
		if writeErr != nil {
			return false, writeErr
		}
		previousConsumed = written.PreviousConsumed
		return changed, nil
	})
	if err != nil {
		return CaptureCreditUsage{}, mapCaptureCreditWriteError(err)
	}
	return s.creditReader.GetCaptureCreditUsageForCoordinator(ctx, eventID, userID, usageDate)
}

// campusToday is the day the admin credit surface acts on.
func (s *Service) campusToday() time.Time {
	return campus.Date(time.Now())
}

func mapCaptureCreditWriteError(err error) error {
	if errors.Is(err, ErrCoordinatorUserNotFound) {
		return coordinatorUserNotFound()
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "55000" {
		return eventArchived()
	}
	return err
}
