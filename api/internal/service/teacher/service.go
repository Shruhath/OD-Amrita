// Package teacher implements teacher-profile creation and branch association
// (Story 2.3, FR19). A teacher profile is not part of the event aggregate (it
// has no event_id), so it gets its own service/handler packages rather than
// living in service/event or handler/admin.
package teacher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// ErrUserNotFound is the reader sentinel for "no users row matches this
// email" — the service translates it into the canonical USER_NOT_FOUND 404.
var ErrUserNotFound = errors.New("teacher: user not found")

// ErrRoleAlreadyAssigned is the writer sentinel for AssignTeacherRole's
// AND role = 'student' guard returning zero rows — the raced-concurrent-
// elevation path, mapped to the same canonical answer as the service's own
// pre-check.
var ErrRoleAlreadyAssigned = errors.New("teacher: role already assigned")

type Branch struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

type TeacherProfile struct {
	UserID    string
	Email     string
	Name      *string
	Branches  []Branch
	CreatedAt time.Time
}

// Actor mirrors event.Actor — small intentional duplication per that
// package's own precedent, not a shared cross-package type.
type Actor struct {
	UserID string
	Email  string
	Role   string
}

type CreateInput struct {
	Email     string
	BranchIDs []string
}

type ListInput struct {
	Page     int
	PageSize int
}

type Page struct {
	Data     []TeacherProfile
	Page     int
	PageSize int
	Total    int64
}

// resolvedUser is what the reader hands back for the email lookup: just
// enough to drive validation (id, current role) without importing the wider
// user-service shape.
type resolvedUser struct {
	ID   string
	Role string
}

type Reader interface {
	GetTeacherByEmail(ctx context.Context, email string) (resolvedUser, error)
	GetTeacherProfile(ctx context.Context, userID uuid.UUID) (TeacherProfile, error)
	ListTeachers(ctx context.Context, input ListInput) ([]TeacherProfile, int64, error)
	CountBranchesByIDs(ctx context.Context, branchIDs []uuid.UUID) (int, error)
}

type Writer interface {
	AssignRoleAndBranches(ctx context.Context, tx pgx.Tx, input AssignWrite) (string, bool, error)
}

// AssignWrite is the already-validated shape handed to the writer inside the
// audited transaction.
type AssignWrite struct {
	UserID    uuid.UUID
	BranchIDs []uuid.UUID
}

type AuditStore interface {
	WithAudit(context.Context, store.AuditEvent, func(context.Context, pgx.Tx) (bool, error)) error
}

type Service struct {
	reader Reader
	store  AuditStore
	writer Writer
}

func NewService(reader Reader, audited AuditStore, writer Writer) *Service {
	return &Service{reader: reader, store: audited, writer: writer}
}

func (s *Service) List(ctx context.Context, actor Actor, input ListInput) (Page, error) {
	if actor.Role != "admin" {
		return Page{}, forbidden()
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 20
	}
	if input.Page < 1 {
		return Page{}, validation("page", "minimum", "Page must be at least 1.")
	}
	if input.PageSize < 1 {
		return Page{}, validation("page_size", "minimum", "Page size must be at least 1.")
	}
	if input.PageSize > 100 {
		return Page{}, validation("page_size", "maximum", "Page size must not exceed 100.")
	}

	teachers, total, err := s.reader.ListTeachers(ctx, input)
	if err != nil {
		return Page{}, err
	}
	return Page{Data: teachers, Page: input.Page, PageSize: input.PageSize, Total: total}, nil
}

// Create elevates an existing provisioned user (role student -> teacher) and
// associates every requested branch, co-committing exactly one
// teacher.profile_created audit row with the business writes.
//
// Validation order (Dev Notes "Validation order in Create"): every rejection
// returns BEFORE a transaction opens, mirroring category.CreateCategory's own
// discipline, so a rejected create can never leave a role elevation or branch
// association behind.
func (s *Service) Create(ctx context.Context, actor Actor, input CreateInput) (TeacherProfile, error) {
	email := strings.TrimSpace(input.Email)
	if email == "" {
		return TeacherProfile{}, validation("email", "required", "Email is required.")
	}
	branchIDs, err := validateBranchIDs(input.BranchIDs)
	if err != nil {
		return TeacherProfile{}, err
	}
	if actor.Role != "admin" {
		return TeacherProfile{}, forbidden()
	}

	user, err := s.reader.GetTeacherByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return TeacherProfile{}, userNotFound()
	}
	if err != nil {
		return TeacherProfile{}, err
	}
	if user.Role != "student" {
		return TeacherProfile{}, roleAlreadyAssigned()
	}

	count, err := s.reader.CountBranchesByIDs(ctx, branchIDs)
	if err != nil {
		return TeacherProfile{}, err
	}
	if count != len(branchIDs) {
		// Not which specific id is missing — CountBranchesByIDs only returns a
		// count, mirroring event.Create's own choice of query. The first id is
		// pinned to keep the field error addressable, matching that shape.
		return TeacherProfile{}, validation(fmt.Sprintf("branch_ids[%d]", 0), "not_found", "Branch does not exist.")
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return TeacherProfile{}, fmt.Errorf("teacher: invalid user id: %w", err)
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "users"
	var targetID string
	payloadBranchIDs := make([]string, len(branchIDs))
	for index, id := range branchIDs {
		payloadBranchIDs[index] = id.String()
	}
	payload, _ := json.Marshal(map[string]any{
		"email":      email,
		"branch_ids": payloadBranchIDs,
	})
	evt := store.AuditEvent{
		Action:       domain.ActionTeacherProfileCreated,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		id, changed, writeErr := s.writer.AssignRoleAndBranches(ctx, tx, AssignWrite{UserID: userID, BranchIDs: branchIDs})
		if errors.Is(writeErr, ErrRoleAlreadyAssigned) {
			return false, roleAlreadyAssigned()
		}
		if writeErr != nil {
			return false, writeErr
		}
		if !changed {
			return false, errors.New("teacher: create returned an unexpected no-op")
		}
		targetID = id
		return true, nil
	})
	if err != nil {
		return TeacherProfile{}, err
	}
	return s.reader.GetTeacherProfile(ctx, uuid.MustParse(targetID))
}

func validateBranchIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, validation("branch_ids", "required", "Select at least one branch.")
	}
	if len(raw) > 100 {
		return nil, validation("branch_ids", "maximum", "Select no more than 100 branches.")
	}
	ids := make([]uuid.UUID, 0, len(raw))
	seen := make(map[uuid.UUID]struct{}, len(raw))
	for index, value := range raw {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, validation(fmt.Sprintf("branch_ids[%d]", index), "invalid_format", "Branch id must be a UUID.")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, validation("branch_ids", "duplicate", "Branch ids must be unique.")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Teacher profile validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required."}
}

func userNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeUSERNOTFOUND, Message: "No user has signed in with this email yet."}
}

func roleAlreadyAssigned() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeROLEALREADYASSIGNED, Message: "This user already has an assigned role."}
}
