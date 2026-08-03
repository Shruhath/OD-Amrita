package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const coordinatorAssignmentUniqueConstraint = "event_coordinator_assignments_user_event_unique"

var (
	ErrCoordinatorUserNotFound        = errors.New("event: coordinator user not found")
	ErrCoordinatorRoleAlreadyAssigned = errors.New("event: coordinator role already assigned")
)

type CoordinatorAssignment struct {
	ID        string
	UserID    string
	Email     string
	Name      *string
	EventID   string
	CreatedAt time.Time
}

type CoordinatorCandidate struct {
	ID    string
	Email string
	Name  *string
	Role  string
}

type CreateCoordinatorAssignmentInput struct {
	EventID string
	Email   string
}

type CreateCoordinatorAssignmentWrite struct {
	UserID          uuid.UUID
	EventID         uuid.UUID
	CreatedByUserID uuid.UUID
}

func (s *Service) ListCoordinatorAssignments(ctx context.Context, actor Actor, rawEventID string) ([]CoordinatorAssignment, error) {
	eventID, err := parseCoordinatorEventID(rawEventID)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" {
		return nil, forbidden()
	}
	if _, err := s.eventStatus(ctx, eventID); err != nil {
		return nil, err
	}
	return s.reader.ListCoordinatorAssignments(ctx, eventID)
}

func (s *Service) CreateCoordinatorAssignment(ctx context.Context, actor Actor, input CreateCoordinatorAssignmentInput) (CoordinatorAssignment, error) {
	email := strings.TrimSpace(input.Email)
	if email == "" {
		return CoordinatorAssignment{}, coordinatorValidation("email", "required", "Coordinator email is required.")
	}
	if utf8.RuneCountInString(email) > 320 {
		return CoordinatorAssignment{}, coordinatorValidation("email", "maximum", "Coordinator email must not exceed 320 characters.")
	}
	eventID, err := parseCoordinatorEventID(input.EventID)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	if actor.Role != "admin" {
		return CoordinatorAssignment{}, forbidden()
	}
	actorID, err := uuid.Parse(actor.UserID)
	if err != nil {
		return CoordinatorAssignment{}, fmt.Errorf("event: invalid principal id: %w", err)
	}

	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	if status == StatusArchived {
		return CoordinatorAssignment{}, eventArchived()
	}

	user, err := s.reader.GetCoordinatorCandidateByEmail(ctx, email)
	if errors.Is(err, ErrCoordinatorUserNotFound) {
		return CoordinatorAssignment{}, coordinatorUserNotFound()
	}
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	if user.Role != "student" && user.Role != "coordinator" {
		return CoordinatorAssignment{}, coordinatorRoleAlreadyAssigned()
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return CoordinatorAssignment{}, fmt.Errorf("event: invalid coordinator user id: %w", err)
	}
	exists, err := s.reader.CoordinatorAssignmentExists(ctx, userID, eventID)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	if exists {
		return CoordinatorAssignment{}, coordinatorAssignmentExists()
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "event_coordinator_assignments"
	var targetID string
	payload, _ := json.Marshal(map[string]any{
		"user_id":  userID.String(),
		"event_id": eventID.String(),
		"email":    user.Email,
	})
	evt := store.AuditEvent{
		Action:       domain.ActionCoordinatorAssigned,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	var created CoordinatorAssignment
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		row, changed, writeErr := s.writer.CreateCoordinatorAssignment(ctx, tx, CreateCoordinatorAssignmentWrite{
			UserID:          userID,
			EventID:         eventID,
			CreatedByUserID: actorID,
		})
		if writeErr != nil {
			return false, writeErr
		}
		if !changed {
			return false, errors.New("event: coordinator assignment returned an unexpected no-op")
		}
		created = row
		targetID = row.ID
		return true, nil
	})
	if err != nil {
		return CoordinatorAssignment{}, mapCoordinatorAssignmentWriteError(err)
	}
	return created, nil
}

func parseCoordinatorEventID(raw string) (uuid.UUID, error) {
	eventID, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, coordinatorValidation("event_id", "invalid_format", "Event id must be a UUID.")
	}
	return eventID, nil
}

func mapCoordinatorAssignmentWriteError(err error) error {
	switch {
	case errors.Is(err, ErrCoordinatorUserNotFound):
		return coordinatorUserNotFound()
	case errors.Is(err, ErrCoordinatorRoleAlreadyAssigned):
		return coordinatorRoleAlreadyAssigned()
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && pgErr.ConstraintName == coordinatorAssignmentUniqueConstraint:
		return coordinatorAssignmentExists()
	case pgErr.Code == "55000" && strings.Contains(pgErr.Message, "event_coordinator_assignments"):
		return eventArchived()
	case pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "event_id"):
		return domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
	case pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "user_id"):
		return coordinatorUserNotFound()
	default:
		return err
	}
}

func coordinatorValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Coordinator assignment validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func coordinatorUserNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeUSERNOTFOUND, Message: "No user has signed in with this email yet."}
}

func coordinatorRoleAlreadyAssigned() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeROLEALREADYASSIGNED, Message: "This user already has an assigned role."}
}

func coordinatorAssignmentExists() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeCOORDINATORASSIGNMENTEXISTS, Message: "This coordinator is already assigned to the event."}
}
