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

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
	StatusAll      Status = "all"
)

var ErrEventNotFound = errors.New("event: not found")

type Branch struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

type Event struct {
	ID         string
	Name       string
	EventDate  string
	Status     Status
	Branches   []Branch
	CreatedAt  time.Time
	ArchivedAt *time.Time
}

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type CreateInput struct {
	Name      string
	EventDate string
	BranchIDs []string
}

type ListInput struct {
	Status   Status
	Page     int
	PageSize int
}

type Page struct {
	Data     []Event
	Page     int
	PageSize int
	Total    int64
}

type Reader interface {
	ListBranches(context.Context) ([]Branch, error)
	GetEvent(context.Context, uuid.UUID) (Event, error)
	ListEvents(context.Context, ListInput) ([]Event, int64, error)
	// EventStatus is the lifecycle lookup the category paths need. It returns
	// ErrEventNotFound for an unknown id, which the service translates into the
	// canonical 404 — distinct from an archived event's 409.
	EventStatus(context.Context, uuid.UUID) (Status, error)
	ListCategories(context.Context, uuid.UUID) ([]Category, error)
}

type Writer interface {
	Create(context.Context, pgx.Tx, CreateWrite) (uuid.UUID, bool, error)
	Archive(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error)
	CreateCategory(context.Context, pgx.Tx, CreateCategoryWrite) (Category, bool, error)
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

func (s *Service) Branches(ctx context.Context) ([]Branch, error) {
	return s.reader.ListBranches(ctx)
}

func (s *Service) List(ctx context.Context, input ListInput) (Page, error) {
	if input.Status == "" {
		input.Status = StatusAll
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 20
	}
	if input.Status != StatusAll && input.Status != StatusActive && input.Status != StatusArchived {
		return Page{}, validation("status", "invalid_enum", "Status must be active, archived, or all.")
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

	events, total, err := s.reader.ListEvents(ctx, input)
	if err != nil {
		return Page{}, err
	}
	return Page{Data: events, Page: input.Page, PageSize: input.PageSize, Total: total}, nil
}

// Get reads one event by id.
//
// This is the item read behind every per-event admin surface. Before it existed
// the portal resolved an event by fetching a page of the collection and filtering
// locally, which reported a truthful-looking "Event not found" for a real, active
// event as soon as the event count outgrew one page.
func (s *Service) Get(ctx context.Context, actor Actor, rawEventID string) (Event, error) {
	if actor.Role != "admin" {
		return Event{}, forbidden()
	}
	eventID, err := uuid.Parse(rawEventID)
	if err != nil {
		return Event{}, validation("event_id", "invalid_format", "Event id must be a UUID.")
	}
	event, err := s.reader.GetEvent(ctx, eventID)
	if errors.Is(err, ErrEventNotFound) {
		return Event{}, domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
	}
	return event, err
}

func (s *Service) Create(ctx context.Context, actor Actor, input CreateInput) (Event, error) {
	name, date, branchIDs, err := validateCreate(input)
	if err != nil {
		return Event{}, err
	}
	if actor.Role != "admin" {
		return Event{}, forbidden()
	}
	branches, err := s.reader.ListBranches(ctx)
	if err != nil {
		return Event{}, err
	}
	known := make(map[uuid.UUID]struct{}, len(branches))
	for _, branch := range branches {
		id, parseErr := uuid.Parse(branch.ID)
		if parseErr == nil {
			known[id] = struct{}{}
		}
	}
	for index, id := range branchIDs {
		if _, ok := known[id]; !ok {
			return Event{}, validation(fmt.Sprintf("branch_ids[%d]", index), "not_found", "Branch does not exist.")
		}
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "events"
	var targetID string
	payloadBranchIDs := make([]string, len(branchIDs))
	for index, id := range branchIDs {
		payloadBranchIDs[index] = id.String()
	}
	payload, _ := json.Marshal(map[string]any{
		"event_date": date.Format("2006-01-02"),
		"branch_ids": payloadBranchIDs,
	})
	evt := store.AuditEvent{
		Action:       domain.ActionEventCreated,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		id, changed, writeErr := s.writer.Create(ctx, tx, CreateWrite{Name: name, EventDate: date, BranchIDs: branchIDs})
		if writeErr != nil {
			return false, writeErr
		}
		if !changed {
			return false, errors.New("event: create returned an unexpected no-op")
		}
		targetID = id.String()
		return true, nil
	})
	if err != nil {
		return Event{}, err
	}
	return s.reader.GetEvent(ctx, uuid.MustParse(targetID))
}

func (s *Service) Archive(ctx context.Context, actor Actor, rawEventID string) (Event, error) {
	if actor.Role != "admin" {
		return Event{}, forbidden()
	}
	eventID, err := uuid.Parse(rawEventID)
	if err != nil {
		return Event{}, validation("event_id", "invalid_format", "Event id must be a UUID.")
	}
	actorID, err := uuid.Parse(actor.UserID)
	if err != nil {
		return Event{}, fmt.Errorf("event: invalid principal id: %w", err)
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "events"
	targetID := eventID.String()
	payload, _ := json.Marshal(map[string]string{"prior_status": "active", "new_status": "archived"})
	evt := store.AuditEvent{
		Action:       domain.ActionEventArchived,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	if err := s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return s.writer.Archive(ctx, tx, eventID, actorID)
	}); err != nil {
		return Event{}, err
	}
	event, err := s.reader.GetEvent(ctx, eventID)
	if errors.Is(err, ErrEventNotFound) {
		return Event{}, domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
	}
	return event, err
}

func validateCreate(input CreateInput) (string, time.Time, []uuid.UUID, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", time.Time{}, nil, validation("name", "required", "Name is required.")
	}
	if utf8.RuneCountInString(name) > 200 {
		return "", time.Time{}, nil, validation("name", "maximum", "Name must not exceed 200 characters.")
	}
	date, err := time.Parse("2006-01-02", input.EventDate)
	if err != nil || date.Format("2006-01-02") != input.EventDate {
		return "", time.Time{}, nil, validation("event_date", "invalid_format", "Event date must be a valid YYYY-MM-DD date.")
	}
	if len(input.BranchIDs) == 0 {
		return "", time.Time{}, nil, validation("branch_ids", "required", "Select at least one branch.")
	}
	if len(input.BranchIDs) > 100 {
		return "", time.Time{}, nil, validation("branch_ids", "maximum", "Select no more than 100 branches.")
	}

	ids := make([]uuid.UUID, 0, len(input.BranchIDs))
	seen := make(map[uuid.UUID]struct{}, len(input.BranchIDs))
	for index, raw := range input.BranchIDs {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return "", time.Time{}, nil, validation(fmt.Sprintf("branch_ids[%d]", index), "invalid_format", "Branch id must be a UUID.")
		}
		if _, duplicate := seen[id]; duplicate {
			return "", time.Time{}, nil, validation("branch_ids", "duplicate", "Branch ids must be unique.")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return name, date, ids, nil
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Event validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required."}
}
