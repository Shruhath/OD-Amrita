// Package userrole takes a staff role away: an administrator demotes a teacher,
// coordinator or admin back to student ("remove staff access").
//
// WHAT A DEMOTION DOES, IN ONE AUDITED TRANSACTION (user.demoted)
//
//	users.role                      -> 'student'
//	coordinator assignments         deleted on every ACTIVE event, after their
//	                                fallback-credit usage counters (FK RESTRICT)
//	category assignments            all deleted (they are per user, not per event)
//	teacher branch associations     all deleted
//	sessions                        a scope=all revocation is recorded, so every
//	                                live token stops working on its next request
//
// WHAT IT LEAVES ALONE
//
//	Coordinator rows (and their credit counters) on ARCHIVED events: archived
//	events are immutable — the active-event trigger refuses the delete — and the
//	rows stay as the record of who coordinated them. They grant nothing to a
//	student: every coordinator surface is behind a role check and only lists
//	active events.
//	OD records the user marked: od_records point at users, not at any access row.
//
// WHY THE FORCED LOGOUT. The role is read through a per-machine principal cache.
// The caller drops it fleet-wide through the cache bus, but with no broker a peer
// machine could keep serving the old role for the cache window. A scope=all
// revocation makes the change land on the next request regardless (the deny-list
// cache is far shorter, and the guard checks it on every request), and a fresh
// sign-in — which is still allowed — arrives as a student.
//
// REFUSED: demoting yourself (SELF_DEMOTION_REFUSED), and leaving the system with
// no admin (LAST_ADMIN_DEMOTION_REFUSED). The second is enforced under a lock on
// every admin row, so two administrators demoting each other at the same moment
// cannot both succeed.
package userrole

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
)

const (
	RoleAdmin   = "admin"
	RoleStudent = "student"

	// RevocationReason is the deny-list reason recorded with a demotion.
	RevocationReason = "staff access removed (demoted to student)"
)

var (
	// ErrUserNotFound: no users row has the target id.
	ErrUserNotFound = errors.New("userrole: user not found")
	// ErrLastAdmin: demoting the target would leave no admin.
	ErrLastAdmin = errors.New("userrole: target is the last admin")
	// ErrActorNotAdmin: the acting admin lost the role before the transaction
	// locked the admin rows (a concurrent demotion of the actor won).
	ErrActorNotAdmin = errors.New("userrole: actor is no longer an admin")
)

// Actor is the acting principal (the RBAC principal RequireAdmin loaded).
type Actor struct {
	UserID string
	Email  string
	Role   string
}

// Result is what the demotion did. Subject is the target's Supabase subject,
// kept for the caller's cache invalidation and never sent on the wire.
type Result struct {
	UserID                     string
	Subject                    string
	Email                      string
	PreviousRole               string
	Role                       string
	Changed                    bool
	RemovedCoordinatorEventIDs []string
	RemovedCreditUsageRows     int64
	RemovedCreditUsageConsumed int64
	RemovedCategoryAssignments int64
	RemovedTeacherBranches     int64
	RevocationID               string
	SessionsRevoked            bool
}

// RemovedCoordinatorAssignments is len(RemovedCoordinatorEventIDs).
func (r Result) RemovedCoordinatorAssignments() int {
	return len(r.RemovedCoordinatorEventIDs)
}

// DemoteWrite is the validated input the writer runs on the audited tx.
type DemoteWrite struct {
	TargetID uuid.UUID
	ActorID  uuid.UUID
}

// Writer runs the whole demotion on the transaction WithAudit opened.
type Writer interface {
	Demote(ctx context.Context, tx pgx.Tx, input DemoteWrite) (Result, error)
}

// AuditStore is the write boundary (*store.Store).
type AuditStore interface {
	WithAudit(context.Context, store.AuditEvent, func(context.Context, pgx.Tx) (bool, error)) error
}

type Service struct {
	store  AuditStore
	writer Writer
}

func NewService(audited AuditStore, writer Writer) *Service {
	return &Service{store: audited, writer: writer}
}

// Demote takes rawUserID's staff role away on behalf of actor. Demoting a user
// who is already a student is an idempotent no-op: Changed=false, no audit row.
func (s *Service) Demote(ctx context.Context, actor Actor, rawUserID string) (Result, error) {
	targetID, err := uuid.Parse(strings.TrimSpace(rawUserID))
	if err != nil {
		return Result{}, validation("user_id", "invalid_format", "User id must be a UUID.")
	}
	if actor.Role != RoleAdmin {
		return Result{}, forbidden()
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return Result{}, fmt.Errorf("userrole: invalid principal id: %w", err)
	}
	if targetID == actorID {
		return Result{}, domain.DomainError{
			Code:    contracts.ErrorCodeSELFDEMOTIONREFUSED,
			Message: "You cannot remove your own staff access.",
		}
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "users"
	targetIDText := targetID.String()
	var result Result
	evt := store.AuditEvent{
		Action:       domain.ActionUserDemoted,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetIDText,
		PayloadFunc: func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"user_id":                       result.UserID,
				"email":                         result.Email,
				"previous_role":                 result.PreviousRole,
				"role":                          result.Role,
				"removed_coordinator_event_ids": result.RemovedCoordinatorEventIDs,
				"removed_credit_usage_rows":     result.RemovedCreditUsageRows,
				"removed_credit_usage_consumed": result.RemovedCreditUsageConsumed,
				"removed_category_assignments":  result.RemovedCategoryAssignments,
				"removed_teacher_branches":      result.RemovedTeacherBranches,
				"revocation_id":                 result.RevocationID,
			})
		},
	}
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		res, writeErr := s.writer.Demote(ctx, tx, DemoteWrite{TargetID: targetID, ActorID: actorID})
		if writeErr != nil {
			return false, writeErr
		}
		result = res
		return res.Changed, nil
	})
	switch {
	case errors.Is(err, ErrUserNotFound):
		return Result{}, domain.DomainError{Code: contracts.ErrorCodeUSERNOTFOUND, Message: "User not found."}
	case errors.Is(err, ErrLastAdmin):
		return Result{}, domain.DomainError{
			Code:    contracts.ErrorCodeLASTADMINDEMOTIONREFUSED,
			Message: "This is the last administrator; promote another admin first.",
		}
	case errors.Is(err, ErrActorNotAdmin):
		return Result{}, forbidden()
	case err != nil:
		return Result{}, err
	}
	return result, nil
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Demotion validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required."}
}
