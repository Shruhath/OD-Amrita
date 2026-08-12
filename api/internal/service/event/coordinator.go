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
const (
	coordinatorAssignmentUserFK      = "event_coordinator_assignments_user_id_fkey"
	coordinatorAssignmentCreatedByFK = "event_coordinator_assignments_created_by_user_id_fkey"
)

var (
	ErrCoordinatorUserNotFound        = errors.New("event: coordinator user not found")
	ErrCoordinatorRoleAlreadyAssigned = errors.New("event: coordinator role already assigned")
)

type CoordinatorAssignment struct {
	ID         string
	UserID     string
	Email      string
	Name       *string
	EventID    string
	Categories []Category
	CreatedAt  time.Time
}

type CoordinatorCandidate struct {
	ID    string
	Email string
	Name  *string
	Role  string
}

type CreateCoordinatorAssignmentInput struct {
	EventID     string
	Email       string
	CategoryIDs []string
}

type UpdateCoordinatorCategoryAccessInput struct {
	EventID     string
	UserID      string
	CategoryIDs []string
}

type CreateCoordinatorAssignmentWrite struct {
	UserID          uuid.UUID
	EventID         uuid.UUID
	CreatedByUserID uuid.UUID
	CategoryIDs     []uuid.UUID
}

type ReplaceCoordinatorCategoryAccessWrite struct {
	UserID      uuid.UUID
	EventID     uuid.UUID
	ActorUserID uuid.UUID
	CategoryIDs []uuid.UUID
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
	categoryIDs, err := validateCategoryIDs(input.CategoryIDs)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	categoryReader, ok := s.reader.(categoryLookupReader)
	if !ok {
		return CoordinatorAssignment{}, errors.New("event: category lookup reader is not configured")
	}
	for _, categoryID := range categoryIDs {
		category, categoryErr := categoryReader.GetCategory(ctx, categoryID)
		if errors.Is(categoryErr, ErrCategoryNotFound) || (categoryErr == nil && category.EventID != eventID.String()) {
			return CoordinatorAssignment{}, coordinatorValidation("category_ids", "not_found", "Category does not exist for this event.")
		}
		if categoryErr != nil {
			return CoordinatorAssignment{}, categoryErr
		}
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
	var created CoordinatorAssignment
	evt := store.AuditEvent{
		Action:       domain.ActionCoordinatorAssigned,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		PayloadFunc: func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"user_id":  created.UserID,
				"event_id": created.EventID,
				"email":    created.Email,
			})
		},
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		row, changed, writeErr := s.writer.CreateCoordinatorAssignment(ctx, tx, CreateCoordinatorAssignmentWrite{
			UserID:          userID,
			EventID:         eventID,
			CreatedByUserID: actorID,
			CategoryIDs:     categoryIDs,
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

func (s *Service) UpdateCoordinatorCategoryAccess(ctx context.Context, actor Actor, input UpdateCoordinatorCategoryAccessInput) (CoordinatorAssignment, error) {
	eventID, err := parseCoordinatorEventID(input.EventID)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	userID, err := uuid.Parse(strings.TrimSpace(input.UserID))
	if err != nil {
		return CoordinatorAssignment{}, coordinatorValidation("user_id", "invalid_format", "User id must be a UUID.")
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
	exists, err := s.reader.CoordinatorAssignmentExists(ctx, userID, eventID)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	if !exists {
		return CoordinatorAssignment{}, domain.DomainError{Code: contracts.ErrorCodeUSERNOTFOUND, Message: "Coordinator assignment not found."}
	}
	categoryIDs, err := validateCategoryIDs(input.CategoryIDs)
	if err != nil {
		return CoordinatorAssignment{}, err
	}
	categoryReader, ok := s.reader.(categoryLookupReader)
	if !ok {
		return CoordinatorAssignment{}, errors.New("event: category lookup reader is not configured")
	}
	for _, categoryID := range categoryIDs {
		category, categoryErr := categoryReader.GetCategory(ctx, categoryID)
		if errors.Is(categoryErr, ErrCategoryNotFound) || (categoryErr == nil && category.EventID != eventID.String()) {
			return CoordinatorAssignment{}, coordinatorValidation("category_ids", "not_found", "Category does not exist for this event.")
		}
		if categoryErr != nil {
			return CoordinatorAssignment{}, categoryErr
		}
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "user_category_assignments"
	targetID := userID.String()
	payloadCategoryIDs := uuidStringsForAudit(categoryIDs)
	payload, _ := json.Marshal(map[string]any{
		"user_id":      userID.String(),
		"event_id":     eventID.String(),
		"category_ids": payloadCategoryIDs,
	})
	evt := store.AuditEvent{
		Action:       domain.ActionCoordinatorCategoryAccessUpdated,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return s.writer.ReplaceCoordinatorCategoryAccess(ctx, tx, ReplaceCoordinatorCategoryAccessWrite{
			UserID: userID, EventID: eventID, ActorUserID: actorID, CategoryIDs: categoryIDs,
		})
	})
	if err != nil {
		return CoordinatorAssignment{}, mapCoordinatorAssignmentWriteError(err)
	}
	return s.reader.GetCoordinatorAssignment(ctx, eventID, userID)
}

func uuidStringsForAudit(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for index, id := range ids {
		out[index] = id.String()
	}
	return out
}

func parseCoordinatorEventID(raw string) (uuid.UUID, error) {
	eventID, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, coordinatorValidation("event_id", "invalid_format", "Event id must be a UUID.")
	}
	return eventID, nil
}

func validateCategoryIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, coordinatorValidation("category_ids", "required", "Select at least one category.")
	}
	if len(raw) > 100 {
		return nil, coordinatorValidation("category_ids", "maximum", "Select no more than 100 categories.")
	}
	ids := make([]uuid.UUID, 0, len(raw))
	seen := make(map[uuid.UUID]struct{}, len(raw))
	for index, value := range raw {
		id, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil {
			return nil, coordinatorValidation(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
		if _, ok := seen[id]; ok {
			return nil, coordinatorValidation("category_ids", "duplicate", "Category ids must be unique.")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
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
	case pgErr.Code == "23503" && pgErr.ConstraintName == coordinatorAssignmentUserFK:
		return coordinatorUserNotFound()
	case pgErr.Code == "23503" && pgErr.ConstraintName == coordinatorAssignmentCreatedByFK:
		return err
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
