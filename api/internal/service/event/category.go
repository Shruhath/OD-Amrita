package event

// category.go — categories are part of the EVENT aggregate, not a parallel
// domain: a category has no identity without its event, so the service, the
// reader/writer seams and the routes all live beside the event ones rather than
// in a `service/category` package of their own.

import (
	"context"
	"encoding/json"
	"errors"
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

// maxCategoryNameRunes mirrors CreateCategoryRequest.name maxLength in the
// contract. Counted in RUNES, not bytes, so a name of accented characters is not
// rejected for being "too long" in a way no user could predict.
const maxCategoryNameRunes = 120

// categoryUniqueConstraint is the unique index migration 0006 creates. The
// constraint NAME is part of the contract between the migration and this
// service: only a violation of THIS index means "duplicate category name", and
// any other unique violation is a real fault that must not be laundered into a
// friendly field error.
const categoryUniqueConstraint = "uq_categories_event_id_name"

type Category struct {
	ID        string
	EventID   string
	Name      string
	CreatedAt time.Time
}

type CreateCategoryInput struct {
	EventID string
	Name    string
}

// CreateCategoryWrite is the already-validated, already-normalized shape handed
// to the writer inside the audited transaction.
type CreateCategoryWrite struct {
	EventID uuid.UUID
	Name    string
}

// ListCategories returns one event's categories in the server's order (name
// ASC, id ASC), which the caller renders and never re-sorts.
//
// An ARCHIVED event still lists: archived is a closed aggregate, not a hidden
// one. Only writes are refused, so the portal can show an archived event's
// categories read-only.
func (s *Service) ListCategories(ctx context.Context, rawEventID string) ([]Category, error) {
	eventID, err := parseCategoryEventID(rawEventID)
	if err != nil {
		return nil, err
	}
	if _, err := s.eventStatus(ctx, eventID); err != nil {
		return nil, err
	}
	return s.reader.ListCategories(ctx, eventID)
}

// CreateCategory adds exactly one category to an event, co-committing its audit
// row through the shared write boundary.
//
// Every rejection returns BEFORE a transaction opens. That ordering is the
// reason "one persisted category ⇔ one category.created audit row" is true by
// construction rather than by luck: a rejected create cannot leave a partial
// write or a stray ledger entry behind.
func (s *Service) CreateCategory(ctx context.Context, actor Actor, input CreateCategoryInput) (Category, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Category{}, categoryValidation("name", "required", "Category name is required.")
	}
	if utf8.RuneCountInString(name) > maxCategoryNameRunes {
		return Category{}, categoryValidation("name", "maximum", "Category name must not exceed 120 characters.")
	}
	eventID, err := parseCategoryEventID(input.EventID)
	if err != nil {
		return Category{}, err
	}
	if actor.Role != "admin" {
		return Category{}, forbidden()
	}

	status, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return Category{}, err
	}
	if status == StatusArchived {
		return Category{}, eventArchived()
	}

	// A courtesy pre-check so the common duplicate arrives as a field error
	// without burning a transaction. It is NOT the guarantee — two concurrent
	// creates race past it, which is why the unique-index violation is mapped to
	// the same answer below.
	existing, err := s.reader.ListCategories(ctx, eventID)
	if err != nil {
		return Category{}, err
	}
	for _, category := range existing {
		if strings.EqualFold(strings.TrimSpace(category.Name), name) {
			return Category{}, duplicateCategoryName()
		}
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "categories"
	var targetID string
	payload, _ := json.Marshal(map[string]any{"event_id": eventID.String(), "name": name})
	evt := store.AuditEvent{
		Action:       domain.ActionCategoryCreated,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	var created Category
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		row, changed, writeErr := s.writer.CreateCategory(ctx, tx, CreateCategoryWrite{EventID: eventID, Name: name})
		if writeErr != nil {
			return false, writeErr
		}
		// Derived from the insert's RETURNING result, never an unconditional
		// true: an insert that affected nothing must fail the whole call rather
		// than let the ledger describe a row that does not exist.
		if !changed {
			return false, errors.New("event: category create returned an unexpected no-op")
		}
		created = row
		// Bound INSIDE the callback from the value the insert returned — a
		// target id chosen beforehand would name a row that may never exist.
		targetID = row.ID
		return true, nil
	})
	if err != nil {
		return Category{}, mapCategoryWriteError(err)
	}
	return created, nil
}

// eventStatus resolves the owning event's lifecycle state, translating the
// reader's sentinel into the canonical 404 the wire promises.
func (s *Service) eventStatus(ctx context.Context, eventID uuid.UUID) (Status, error) {
	status, err := s.reader.EventStatus(ctx, eventID)
	if errors.Is(err, ErrEventNotFound) {
		return "", domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
	}
	if err != nil {
		return "", err
	}
	return status, nil
}

func parseCategoryEventID(raw string) (uuid.UUID, error) {
	eventID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, categoryValidation("event_id", "invalid_format", "Event id must be a UUID.")
	}
	return eventID, nil
}

// mapCategoryWriteError translates the two database rejections the caller is
// entitled to understand into canonical codes, and leaves everything else alone.
//
//   - 23505 on the per-event unique index: the concurrent duplicate. Without
//     this the losing writer of a race gets an opaque 500 for input the product
//     considers merely duplicate.
//   - 55000 from the archived-event guard: the event was archived between the
//     status lookup and the insert. The caller gets the SAME canonical
//     EVENT_ARCHIVED as the pre-check, so the portal has one branch, not two.
func mapCategoryWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && pgErr.ConstraintName == categoryUniqueConstraint:
		return duplicateCategoryName()
	case pgErr.Code == "55000":
		return eventArchived()
	default:
		return err
	}
}

func categoryValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Category validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func duplicateCategoryName() domain.DomainError {
	return categoryValidation("name", "duplicate", "A category with this name already exists.")
}

func eventArchived() domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeEVENTARCHIVED,
		Message: "This event is archived and can no longer be changed.",
	}
}
