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
	"unicode"
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

// categoryIndexTrimSet is the EXACT cutset migration 0006's unique index trims
// with (`btrim(name, E' \t\r\n')`). The duplicate pre-check below normalizes with
// this, not with strings.TrimSpace/EqualFold, because a pre-check that disagrees
// with the index it fronts is worse than no pre-check: strings.EqualFold applies
// full Unicode case folding (U+017F ſ folds to s, U+00B5 µ to μ, U+212A K to k)
// where SQL lower() does not, so the Go check was STRICTER than the constraint
// and would permanently reject names the database would have accepted.
//
// Stored names are already strings.TrimSpace'd, so the wider Go trim set only
// diverges for rows written outside this service (the od_migrator escape hatch,
// a bulk import, the DB browser). The index remains the guarantee either way.
const categoryIndexTrimSet = " \t\r\n"

// normalizeCategoryName mirrors the unique index's expression exactly.
func normalizeCategoryName(name string) string {
	return strings.ToLower(strings.Trim(name, categoryIndexTrimSet))
}

// hasVisibleCharacter reports whether a name contains anything a human could
// actually see.
//
// `strings.TrimSpace` and the migration's `CHECK (name ~ '\S')` both agree that
// " " is blank, but neither rejects a name made only of INVISIBLE characters:
// U+200B zero-width space, U+FEFF, U+2800 Braille blank and U+3164 Hangul filler
// are not Unicode White_Space and do match `\S`. Without this, a category
// rendering as an empty row is persisted with a real audit row, and every repeat
// is a distinct index key, so an unbounded number can be created.
//
// This also rejects control characters, which is what keeps a NUL out of the
// insert: Postgres cannot store U+0000 in `text` and answers with SQLSTATE 22021,
// which would otherwise surface as a 500 for fully client-controlled input.
func hasVisibleCharacter(name string) bool {
	for _, r := range name {
		if unicode.IsSpace(r) {
			continue
		}
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cs, r) {
			continue
		}
		// Explicitly invisible characters that are not classified as space or
		// format: Braille blank and Hangul filler both render as nothing.
		if r == '⠀' || r == 'ㅤ' {
			continue
		}
		return true
	}
	return false
}

type Category struct {
	ID                       string
	EventID                  string
	Name                     string
	VisibleOnPublicDashboard bool
	CreatedAt                time.Time
}

type CreateCategoryInput struct {
	EventID string
	Name    string
}

type UpdateCategoryVisibilityInput struct {
	EventID                  string
	CategoryID               string
	VisibleOnPublicDashboard bool
}

type categoryLookupReader interface {
	GetCategory(context.Context, uuid.UUID) (Category, error)
}

type categoryVisibilityWriter interface {
	UpdateCategoryVisibility(context.Context, pgx.Tx, uuid.UUID, bool) (Category, error)
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
// The actor is re-checked here for the same reason CreateCategory re-checks it:
// this service is reachable from any future caller, and coordinator-facing
// code is the obvious next reader of this method. Leaving the read to trust its route
// while the write defends itself is an asymmetry that only looks harmless until
// someone mounts the read outside the /admin group.
func (s *Service) ListCategories(ctx context.Context, actor Actor, rawEventID string) ([]Category, error) {
	eventID, err := parseCategoryEventID(rawEventID)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" {
		return nil, forbidden()
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
	if name == "" || !hasVisibleCharacter(name) {
		return Category{}, categoryValidation("name", "required", "Category name is required.")
	}
	// Control characters are rejected wherever they appear, not just when the
	// name is nothing but invisibles. A NUL surrounded by ordinary letters
	// ("a\x00b") passes every other check here and then fails INSIDE the insert
	// with SQLSTATE 22021 — Postgres cannot store U+0000 in `text` — which would
	// reach the caller as a 500 for input they fully control.
	if strings.ContainsFunc(name, func(r rune) bool { return unicode.Is(unicode.Cc, r) }) {
		return Category{}, categoryValidation("name", "invalid_format",
			"Category name must not contain control characters.")
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
	candidate := normalizeCategoryName(name)
	for _, category := range existing {
		if normalizeCategoryName(category.Name) == candidate {
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

func (s *Service) UpdateCategoryVisibility(ctx context.Context, actor Actor, input UpdateCategoryVisibilityInput) (Category, error) {
	eventID, err := parseCategoryEventID(input.EventID)
	if err != nil {
		return Category{}, err
	}
	categoryID, err := uuid.Parse(strings.TrimSpace(input.CategoryID))
	if err != nil {
		return Category{}, categoryValidation("category_id", "invalid_format", "Category id must be a UUID.")
	}
	if actor.Role != "admin" {
		return Category{}, forbidden()
	}
	if _, err := s.eventStatus(ctx, eventID); err != nil {
		return Category{}, err
	}
	categoryReader, ok := s.reader.(categoryLookupReader)
	if !ok {
		return Category{}, errors.New("event: category lookup reader is not configured")
	}
	categoryWriter, ok := s.writer.(categoryVisibilityWriter)
	if !ok {
		return Category{}, errors.New("event: category visibility writer is not configured")
	}
	category, err := categoryReader.GetCategory(ctx, categoryID)
	if errors.Is(err, ErrCategoryNotFound) {
		return Category{}, domain.DomainError{Code: contracts.ErrorCodeCATEGORYNOTFOUND, Message: "Category not found."}
	}
	if err != nil {
		return Category{}, err
	}
	if category.EventID != eventID.String() {
		return Category{}, domain.DomainError{Code: contracts.ErrorCodeCATEGORYNOTFOUND, Message: "Category not found for this event."}
	}
	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "categories"
	targetID := categoryID.String()
	var updated Category
	evt := store.AuditEvent{
		Action:       domain.ActionCategoryVisibilityUpdated,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		PayloadFunc: func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"event_id":                             eventID.String(),
				"category_id":                          categoryID.String(),
				"visible_on_public_dashboard":          input.VisibleOnPublicDashboard,
				"previous_visible_on_public_dashboard": category.VisibleOnPublicDashboard,
			})
		},
	}
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		row, writeErr := categoryWriter.UpdateCategoryVisibility(ctx, tx, categoryID, input.VisibleOnPublicDashboard)
		if writeErr != nil {
			return false, writeErr
		}
		updated = row
		return category.VisibleOnPublicDashboard != input.VisibleOnPublicDashboard, nil
	})
	if err != nil {
		return Category{}, err
	}
	if updated.ID == "" {
		return category, nil
	}
	return updated, nil
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
//   - 55000 raised by the archived-event guard ON categories: the event was
//     archived between the status lookup and the insert. The caller gets the SAME
//     canonical EVENT_ARCHIVED as the pre-check, so the portal has one branch,
//     not two.
//   - 23503 on the owning foreign key: the event was DELETED in that same window.
//     The truthful answer is the 404 the pre-check would have given a moment
//     earlier, not a 500. This is the exact mirror of the 55000 case.
//
// Everything else is returned untouched and becomes a 500, which is correct: an
// unrecognized database fault is a fault, not a user-facing rule.
func mapCategoryWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && pgErr.ConstraintName == categoryUniqueConstraint:
		return duplicateCategoryName()
	// Pinned to the categories table for the same reason the 23505 arm is pinned
	// to its constraint name. 55000 is the GENERIC object_not_in_prerequisite_state,
	// and 0006's guard is shared with event_branches today and with coordinator
	// assignments and camera-credit config next. An unpinned arm would one day tell
	// an admin that a perfectly active event is archived.
	case pgErr.Code == "55000" && strings.Contains(pgErr.Message, "categories"):
		return eventArchived()
	case pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "event_id"):
		return domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
	// 22021 is "invalid byte sequence for encoding" — a NUL inside the name. The
	// service rejects these before the insert (hasVisibleCharacter), so reaching
	// here means a caller bypassed validation; answer with the same field error
	// rather than a 500.
	case pgErr.Code == "22021":
		return categoryValidation("name", "invalid_format", "Category name contains characters that cannot be stored.")
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
