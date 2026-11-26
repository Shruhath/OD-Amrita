package event

// coordinator_removal.go — an administrator takes one coordinator off one event.
//
// WHAT GOES, IN ONE AUDITED TRANSACTION (coordinator.removed)
//
//	1. the assignment row is locked, so a concurrent fallback-credit debit
//	   (which FK-checks the assignment) waits and then fails rather than
//	   inserting a usage row the snapshot below would miss;
//	2. the assignment's fallback-credit usage counters are summed for the audit
//	   snapshot — they are deleted WITH the assignment by the ON DELETE CASCADE
//	   foreign key (0027); od_runtime holds no DELETE on them;
//	3. the user's category access on THIS event's categories — it means nothing
//	   without the event assignment, and leaving it would hand the access back
//	   on a later re-assignment;
//	4. the assignment itself.
//
// WHAT STAYS: the user's role (still coordinator, even with no events left —
// demotion is the separate, explicit operation), their assignments on other
// events, and every OD record they marked (od_records reference users, never
// the assignment).
//
// Archived events are refused before the transaction, exactly as assignment
// is, and the active-event trigger refuses the delete again if the event is
// archived in between.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// ErrCoordinatorAssignmentNotFound is the writer sentinel for a removal whose
// assignment row is gone by the time the transaction locks it (a concurrent
// removal won the race).
var ErrCoordinatorAssignmentNotFound = errors.New("event: coordinator assignment not found")

type RemoveCoordinatorAssignmentInput struct {
	EventID string
	UserID  string
}

type RemoveCoordinatorAssignmentWrite struct {
	EventID uuid.UUID
	UserID  uuid.UUID
}

// CoordinatorRemoval is the snapshot of what a removal deleted, recorded in
// the audit payload.
type CoordinatorRemoval struct {
	AssignmentID          string
	UserID                string
	EventID               string
	Email                 string
	FallbackCreditLimit   int32
	RemovedCategoryIDs    []string
	RemovedCreditRows     int64
	RemovedCreditConsumed int64
}

// CoordinatorRemovalWriter is the optional writer capability removal needs. It
// is asserted rather than added to Writer so the existing Writer fakes do not
// all have to learn a method they never exercise; *SQLCWriter implements it.
type CoordinatorRemovalWriter interface {
	RemoveCoordinatorAssignment(context.Context, pgx.Tx, RemoveCoordinatorAssignmentWrite) (CoordinatorRemoval, error)
}

// RemoveCoordinatorAssignment deletes one user's coordinator assignment for one
// event (see the file comment for exactly what goes with it).
//
// Status codes follow the sibling category-access update: malformed ids are
// 400, a missing event is EVENT_NOT_FOUND, a user who is not assigned is
// USER_NOT_FOUND "Coordinator assignment not found." (so a repeat is 404, not
// a silent 204), and an archived event is EVENT_ARCHIVED.
func (s *Service) RemoveCoordinatorAssignment(ctx context.Context, actor Actor, input RemoveCoordinatorAssignmentInput) error {
	eventID, err := parseCoordinatorEventID(input.EventID)
	if err != nil {
		return err
	}
	userID, err := uuid.Parse(strings.TrimSpace(input.UserID))
	if err != nil {
		return coordinatorValidation("user_id", "invalid_format", "User id must be a UUID.")
	}
	if actor.Role != "admin" {
		return forbidden()
	}
	remover, ok := s.writer.(CoordinatorRemovalWriter)
	if !ok {
		return errors.New("event: coordinator removal writer is not configured")
	}
	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return err
	}
	if status == StatusArchived {
		return eventArchived()
	}
	exists, err := s.reader.CoordinatorAssignmentExists(ctx, userID, eventID)
	if err != nil {
		return err
	}
	if !exists {
		return coordinatorAssignmentNotFound()
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "event_coordinator_assignments"
	var targetID string
	var removed CoordinatorRemoval
	evt := store.AuditEvent{
		Action:       domain.ActionCoordinatorRemoved,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		PayloadFunc: func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"assignment_id":                 removed.AssignmentID,
				"user_id":                       removed.UserID,
				"event_id":                      removed.EventID,
				"email":                         removed.Email,
				"fallback_credit_limit":         removed.FallbackCreditLimit,
				"removed_category_ids":          removed.RemovedCategoryIDs,
				"removed_credit_usage_rows":     removed.RemovedCreditRows,
				"removed_credit_usage_consumed": removed.RemovedCreditConsumed,
			})
		},
	}
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		row, writeErr := remover.RemoveCoordinatorAssignment(ctx, tx, RemoveCoordinatorAssignmentWrite{EventID: eventID, UserID: userID})
		if writeErr != nil {
			return false, writeErr
		}
		removed = row
		targetID = row.AssignmentID
		return true, nil
	})
	if errors.Is(err, ErrCoordinatorAssignmentNotFound) {
		return coordinatorAssignmentNotFound()
	}
	if err != nil {
		return mapCoordinatorAssignmentWriteError(err)
	}
	return nil
}

// RemoveCoordinatorAssignment runs the four steps on the audited transaction.
func (w *SQLCWriter) RemoveCoordinatorAssignment(ctx context.Context, tx pgx.Tx, input RemoveCoordinatorAssignmentWrite) (CoordinatorRemoval, error) {
	q := sqlc.New(tx)
	locked, err := q.LockCoordinatorAssignment(ctx, sqlc.LockCoordinatorAssignmentParams{
		EventID: dbUUID(input.EventID), UserID: dbUUID(input.UserID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CoordinatorRemoval{}, ErrCoordinatorAssignmentNotFound
	}
	if err != nil {
		return CoordinatorRemoval{}, err
	}
	usage, err := q.SumCoordinatorCaptureCreditUsage(ctx, sqlc.SumCoordinatorCaptureCreditUsageParams{
		EventID: dbUUID(input.EventID), UserID: dbUUID(input.UserID),
	})
	if err != nil {
		return CoordinatorRemoval{}, err
	}
	categoryIDs, err := q.DeleteUserCategoriesForEvent(ctx, sqlc.DeleteUserCategoriesForEventParams{
		UserID: dbUUID(input.UserID), EventID: dbUUID(input.EventID),
	})
	if err != nil {
		return CoordinatorRemoval{}, err
	}
	deleted, err := q.DeleteEventCoordinatorAssignment(ctx, sqlc.DeleteEventCoordinatorAssignmentParams{
		EventID: dbUUID(input.EventID), UserID: dbUUID(input.UserID),
	})
	if err != nil {
		return CoordinatorRemoval{}, err
	}
	if deleted != 1 {
		return CoordinatorRemoval{}, fmt.Errorf("event: coordinator removal deleted %d assignment rows, want 1", deleted)
	}
	removedCategories := make([]string, 0, len(categoryIDs))
	for _, id := range categoryIDs {
		if id.Valid {
			removedCategories = append(removedCategories, uuid.UUID(id.Bytes).String())
		}
	}
	return CoordinatorRemoval{
		AssignmentID:          uuid.UUID(locked.ID.Bytes).String(),
		UserID:                input.UserID.String(),
		EventID:               input.EventID.String(),
		Email:                 locked.Email,
		FallbackCreditLimit:   locked.FallbackCreditLimit,
		RemovedCategoryIDs:    removedCategories,
		RemovedCreditRows:     usage.Rows,
		RemovedCreditConsumed: usage.Consumed,
	}, nil
}

func coordinatorAssignmentNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeUSERNOTFOUND, Message: "Coordinator assignment not found."}
}
