package event

// category_test.go — Story 2.2 coverage for AC 2, 3, 4 and 6.
// UNIT test: no build tag, no database.
//
// Invoked by: api-ci -> build job (`go test ./...`).
//
// The scaffold's expectation tables below are the specification: the exact
// rejection order, field names and error codes the service owes, as
// machine-readable Go data rather than prose. They are ranged over, not
// rewritten. The cases the scaffold held commented-out are now live against the
// real service.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const (
	activeEventID      = "20000000-0000-4000-8000-000000000001"
	archivedEventID    = "20000000-0000-4000-8000-000000000002"
	missingEventID     = "20000000-0000-4000-8000-000000000003"
	otherEventID       = "20000000-0000-4000-8000-000000000004"
	adminUserID        = "30000000-0000-4000-8000-000000000001"
	existingCategoryID = "40000000-0000-4000-8000-000000000001"
)

// existingCategories is the "Procession already exists under the active event"
// fixture the duplicate case in the rejection matrix needs.
func existingCategories() map[string][]Category {
	return map[string][]Category{
		activeEventID: {{ID: existingCategoryID, EventID: activeEventID, Name: "Procession"}},
	}
}

func adminActor() Actor {
	return Actor{UserID: adminUserID, Email: "event-admin@amrita.edu", Role: "admin"}
}

// eventIDFor turns a scenario label into the id the caller would send. The
// malformed case deliberately is not a UUID at all.
func eventIDFor(state string) string {
	switch state {
	case "archived":
		return archivedEventID
	case "missing":
		return missingEventID
	case "malformed-id":
		return "not-a-uuid"
	default:
		return activeEventID
	}
}

// fakeCategoryReader answers the two reads the category paths make: the owning
// event's lifecycle state, and the categories already under it.
type fakeCategoryReader struct {
	state      string
	categories map[string][]Category
	err        error
}

func (f *fakeCategoryReader) ListBranches(context.Context) ([]Branch, error) { return nil, f.err }
func (f *fakeCategoryReader) GetEvent(context.Context, uuid.UUID) (Event, error) {
	return Event{}, f.err
}
func (f *fakeCategoryReader) ListEvents(context.Context, ListInput) ([]Event, int64, error) {
	return nil, 0, f.err
}
func (f *fakeCategoryReader) EventStatus(context.Context, uuid.UUID) (Status, error) {
	if f.err != nil {
		return "", f.err
	}
	switch f.state {
	case "missing":
		return "", ErrEventNotFound
	case "archived":
		return StatusArchived, nil
	default:
		return StatusActive, nil
	}
}
func (f *fakeCategoryReader) ListCategories(_ context.Context, eventID uuid.UUID) ([]Category, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.categories[eventID.String()], nil
}
func (f *fakeCategoryReader) ListCoordinatorAssignments(context.Context, uuid.UUID) ([]CoordinatorAssignment, error) {
	return nil, f.err
}
func (f *fakeCategoryReader) GetCoordinatorAssignment(context.Context, uuid.UUID, uuid.UUID) (CoordinatorAssignment, error) {
	return CoordinatorAssignment{}, f.err
}
func (f *fakeCategoryReader) GetCoordinatorCandidateByEmail(context.Context, string) (CoordinatorCandidate, error) {
	return CoordinatorCandidate{}, f.err
}
func (f *fakeCategoryReader) CoordinatorAssignmentExists(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, f.err
}
func (f *fakeCategoryReader) ListAssignedActiveEvents(context.Context, uuid.UUID) ([]Event, error) {
	return nil, f.err
}
func (f *fakeCategoryReader) ListActiveEventsForMarking(context.Context) ([]Event, error) {
	return nil, f.err
}
func (f *fakeCategoryReader) CoordinatorAssignedToEvent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, f.err
}

// countingCategoryWriter records what reached the write layer. `noChange` is the
// explicit opt-in for "the insert affected nothing" — the default is a normal
// successful insert, so a test that says nothing about it gets the happy path.
type countingCategoryWriter struct {
	id          uuid.UUID
	noChange    bool
	err         error
	calls       int
	lastName    string
	lastEventID uuid.UUID
}

func (w *countingCategoryWriter) Create(context.Context, pgx.Tx, CreateWrite) (uuid.UUID, bool, error) {
	return uuid.Nil, false, w.err
}
func (w *countingCategoryWriter) Archive(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error) {
	return false, w.err
}
func (w *countingCategoryWriter) CreateCategory(_ context.Context, _ pgx.Tx, input CreateCategoryWrite) (Category, bool, error) {
	w.calls++
	w.lastName = input.Name
	w.lastEventID = input.EventID
	if w.err != nil {
		return Category{}, false, w.err
	}
	if w.noChange {
		return Category{}, false, nil
	}
	return Category{
		ID:      w.id.String(),
		EventID: input.EventID.String(),
		Name:    input.Name,
	}, true, nil
}
func (w *countingCategoryWriter) CreateCoordinatorAssignment(context.Context, pgx.Tx, CreateCoordinatorAssignmentWrite) (CoordinatorAssignment, bool, error) {
	return CoordinatorAssignment{}, false, w.err
}
func (w *countingCategoryWriter) ReplaceCoordinatorCategoryAccess(context.Context, pgx.Tx, ReplaceCoordinatorCategoryAccessWrite) (bool, error) {
	return false, w.err
}

// categoryRejection is one rejected create and the exact answer the caller is
// owed. Order matters as much as content: every one of these must return BEFORE
// a transaction opens, or a rejected create can still burn a sequence value and
// muddy the audit-conservation property in AC 6.
type categoryRejection struct {
	name       string // scenario label
	input      string // raw name submitted by the caller
	eventState string // "active" | "archived" | "missing" | "malformed-id"
	wantCode   string // canonical contract error code
	wantField  string // ValidationErrorDetail.field, empty when not a field error
	wantDetail string // ValidationErrorDetail.code, empty when not a field error
	wantStatus int
}

// categoryRejections is the complete rejection matrix for Create, in the order
// the service must evaluate it. A reordering that still returns the right codes
// for these cases is acceptable; one that returns a DIFFERENT code for any case
// here is a behavior change, not a refactor.
var categoryRejections = []categoryRejection{
	{
		name:       "blank name is rejected before anything else is consulted",
		input:      "   ",
		eventState: "active",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "name",
		wantDetail: "required",
		wantStatus: 400,
	},
	{
		name:       "name longer than 120 runes is rejected on rune count, not byte length",
		input:      "é" + "123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890",
		eventState: "active",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "name",
		wantDetail: "maximum",
		wantStatus: 400,
	},
	{
		name:       "malformed event id is a field error, never a 500",
		input:      "Procession",
		eventState: "malformed-id",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "event_id",
		wantDetail: "invalid_format",
		wantStatus: 400,
	},
	{
		name:       "unknown event is not found",
		input:      "Procession",
		eventState: "missing",
		wantCode:   "EVENT_NOT_FOUND",
		wantStatus: 404,
	},
	{
		name:       "archived event is a state conflict, not a validation failure",
		input:      "Procession",
		eventState: "archived",
		wantCode:   "EVENT_ARCHIVED",
		wantStatus: 409,
	},
	{
		name:       "duplicate name is a typed field error, not a new error code",
		input:      "  procession  ",
		eventState: "active",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "name",
		wantDetail: "duplicate",
		wantStatus: 400,
	},
}

func TestCategoryCreateRejectsEveryInvalidRequestBeforeOpeningATransaction(t *testing.T) {
	for _, tc := range categoryRejections {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeCategoryReader{state: tc.eventState, categories: existingCategories()}
			writer := &countingCategoryWriter{}
			audit := &fakeAuditStore{}
			svc := NewService(reader, audit, writer)

			_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
				EventID: eventIDFor(tc.eventState),
				Name:    tc.input,
			})

			var domainErr domain.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("error = %v, want a DomainError", err)
			}
			if string(domainErr.Code) != tc.wantCode {
				t.Errorf("code = %s, want %s", domainErr.Code, tc.wantCode)
			}
			if domain.HTTPStatus(domainErr.Code) != tc.wantStatus {
				t.Errorf("status = %d, want %d", domain.HTTPStatus(domainErr.Code), tc.wantStatus)
			}
			if tc.wantField != "" {
				if len(domainErr.Details) != 1 {
					t.Fatalf("details = %#v, want exactly one", domainErr.Details)
				}
				if domainErr.Details[0].Field != tc.wantField || domainErr.Details[0].Code != tc.wantDetail {
					t.Errorf("detail = %#v, want field=%s code=%s", domainErr.Details[0], tc.wantField, tc.wantDetail)
				}
			} else if len(domainErr.Details) != 0 {
				t.Errorf("details = %#v, want none", domainErr.Details)
			}

			// The point of the whole ordering rule: a rejection must never
			// have reached the write boundary. If WithAudit was entered, a
			// rejected create is capable of leaving a row behind, and AC 6's
			// conservation property stops being true by construction.
			if audit.calls != 0 {
				t.Errorf("WithAudit entered %d times for a rejected create", audit.calls)
			}
			if writer.calls != 0 {
				t.Errorf("writer reached %d times for a rejected create", writer.calls)
			}
		})
	}
}

func TestCategoryCreateTrimsTheNameBeforePersistingIt(t *testing.T) {
	reader := &fakeCategoryReader{state: "active"}
	writer := &countingCategoryWriter{id: uuid.New()}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	created, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "  Stage Setup  ",
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// The trimmed form is what the unique index normalizes on, so an
	// untrimmed row would let "Stage Setup " and "Stage Setup" coexist in the
	// same event and quietly break AC 2.
	if writer.lastName != "Stage Setup" {
		t.Errorf("persisted name = %q, want %q", writer.lastName, "Stage Setup")
	}
	if created.Name != "Stage Setup" {
		t.Errorf("returned name = %q, want %q", created.Name, "Stage Setup")
	}
}

func TestCategoryCreateMapsTheUniqueViolationRaceToTheSameTypedDuplicate(t *testing.T) {
	// The pre-check is a nicety; the unique index is the guarantee. Two
	// concurrent creates WILL race past a SELECT-then-INSERT, and the loser
	// must still receive 400-with-detail. Without this mapping the second
	// admin sees an opaque 500 for an input the product considers merely
	// duplicate — the single most likely production defect in this story.
	//
	// The reader deliberately shows NO existing category, so only the mapping of
	// the constraint violation can produce the expected answer.
	reader := &fakeCategoryReader{state: "active"}
	writer := &countingCategoryWriter{
		err: &pgconn.PgError{Code: "23505", ConstraintName: "uq_categories_event_id_name"},
	}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "Procession",
	})

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %v, want a DomainError", err)
	}
	if domainErr.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("code = %s, want VALIDATION_FAILED (never INTERNAL)", domainErr.Code)
	}
	if len(domainErr.Details) != 1 || domainErr.Details[0].Field != "name" || domainErr.Details[0].Code != "duplicate" {
		t.Errorf("detail = %#v, want field=name code=duplicate", domainErr.Details)
	}

	// A unique violation on a DIFFERENT constraint is a real fault and must
	// NOT be laundered into a friendly field error.
	other := &countingCategoryWriter{err: &pgconn.PgError{Code: "23505", ConstraintName: "categories_pkey"}}
	_, err = NewService(reader, &fakeAuditStore{}, other).CreateCategory(
		context.Background(), adminActor(), CreateCategoryInput{EventID: activeEventID, Name: "Procession"})
	if errors.As(err, &domainErr) && domainErr.Code == contracts.ErrorCodeVALIDATIONFAILED {
		t.Error("an unrelated unique violation must not be reported as a duplicate name")
	}
}

func TestCategoryCreateMapsTheArchivedEventTriggerToTheCanonicalCode(t *testing.T) {
	// The 55000 backstop can fire even when the service pre-check passed: the
	// event can be archived between the status lookup and the insert. The
	// caller must get the same canonical EVENT_ARCHIVED either way, so the
	// portal has one branch to handle rather than two.
	reader := &fakeCategoryReader{state: "active"}
	// The message is the one migration 0006's guard actually raises. It matters:
	// 55000 is the GENERIC object_not_in_prerequisite_state and the guard is
	// shared with event_branches (and, next, coordinator assignments and
	// camera-credit config), so the mapping is pinned to this table rather than
	// claiming every 55000 in the system means "this event is archived".
	writer := &countingCategoryWriter{err: &pgconn.PgError{
		Code:    "55000",
		Message: "archived event is a closed aggregate: categories is immutable",
	}}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "Procession",
	})

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %v, want a DomainError", err)
	}
	if string(domainErr.Code) != "EVENT_ARCHIVED" {
		t.Errorf("code = %s, want EVENT_ARCHIVED", domainErr.Code)
	}
}

// The other half of the pinning: a 55000 raised by a DIFFERENT table's guard is
// a real fault, not a friendly "this event is archived" for an event that is
// perfectly active. Without this the shared guard would mislead 2.4 and 2.8.
func TestCategoryCreateDoesNotClaimAnUnrelatedTriggerMeansTheEventIsArchived(t *testing.T) {
	writer := &countingCategoryWriter{err: &pgconn.PgError{
		Code:    "55000",
		Message: "archived event is a closed aggregate: coordinator_assignments is immutable",
	}}
	svc := NewService(&fakeCategoryReader{state: "active"}, &fakeAuditStore{}, writer)

	_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "Procession",
	})

	var domainErr domain.DomainError
	if errors.As(err, &domainErr) {
		t.Errorf("an unrelated table's 55000 must not be reported as %s", domainErr.Code)
	}
}

// A foreign-key violation means the event was DELETED between the status lookup
// and the insert. The truthful answer is the same 404 the pre-check would have
// given a moment earlier — the exact mirror of the 55000 case, and previously a
// 500 for a situation the service already knows how to describe.
func TestCategoryCreateMapsTheOwningForeignKeyViolationToEventNotFound(t *testing.T) {
	writer := &countingCategoryWriter{err: &pgconn.PgError{
		Code:           "23503",
		ConstraintName: "categories_event_id_fkey",
	}}
	svc := NewService(&fakeCategoryReader{state: "active"}, &fakeAuditStore{}, writer)

	_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "Procession",
	})

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %v, want a DomainError", err)
	}
	if string(domainErr.Code) != "EVENT_NOT_FOUND" {
		t.Errorf("code = %s, want EVENT_NOT_FOUND", domainErr.Code)
	}
}

// Names made only of invisible characters are rejected as blank.
//
// U+200B and friends are not Unicode White_Space and DO match the migration's
// `CHECK (name ~ '\S')`, so without this a category that renders as an empty row
// is persisted with a real audit row — and every repeat is a distinct index key,
// so an unbounded number can be created. U+0000 is in the same list for a
// different reason: Postgres cannot store it in `text` and answers 22021, which
// would otherwise reach the caller as a 500 for fully client-controlled input.
func TestCategoryCreateRejectsNamesWithNoVisibleCharacter(t *testing.T) {
	for name, value := range map[string]string{
		"zero-width space":  "\u200b",
		"zero-width joiner": "\u200d\u200d",
		"byte-order mark":   "\ufeff",
		"braille blank":     "\u2800",
		"hangul filler":     "\u3164",
		"nul":               "a\x00b",
	} {
		t.Run(name, func(t *testing.T) {
			writer := &countingCategoryWriter{}
			svc := NewService(&fakeCategoryReader{state: "active"}, &fakeAuditStore{}, writer)

			_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
				EventID: activeEventID,
				Name:    value,
			})

			var domainErr domain.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("error = %v, want a DomainError", err)
			}
			if string(domainErr.Code) != "VALIDATION_FAILED" {
				t.Errorf("code = %s, want VALIDATION_FAILED", domainErr.Code)
			}
			if writer.calls != 0 {
				t.Errorf("writer reached %d times; an invisible name must never reach the write layer", writer.calls)
			}
		})
	}
}

// The duplicate pre-check must not be STRICTER than the unique index it fronts.
// strings.EqualFold applies full Unicode case folding (U+017F ſ folds to s) where
// SQL lower() does not, so the old check permanently rejected names the database
// would have accepted — a rejection no user could act on.
func TestCategoryDuplicatePreCheckMatchesTheIndexNormalization(t *testing.T) {
	reader := &fakeCategoryReader{state: "active", categories: map[string][]Category{
		activeEventID: {{ID: "11111111-1111-4111-8111-111111111111", EventID: activeEventID, Name: "Stage"}},
	}}
	writer := &countingCategoryWriter{id: uuid.MustParse("44444444-4444-4444-8444-444444444444")}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	// lower('ſtage') is 'ſtage', which is NOT 'stage' — the index would accept it.
	if _, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "\u017ftage",
	}); err != nil {
		t.Fatalf("a name the unique index would accept was rejected: %v", err)
	}

	// Plain ASCII case and surrounding whitespace still collapse, because
	// lower(btrim(...)) does collapse them.
	if _, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID,
		Name:    "  STAGE  ",
	}); err == nil {
		t.Error("a case- and whitespace-equivalent name must still be rejected as a duplicate")
	}
}

func TestCategoryCreateWritesExactlyOneAuditRowWithServerOwnedActorContext(t *testing.T) {
	writer := &countingCategoryWriter{id: uuid.MustParse("33333333-3333-4333-8333-333333333333")}
	audit := &fakeAuditStore{}
	svc := NewService(&fakeCategoryReader{state: "active"}, audit, writer)

	_, err := svc.CreateCategory(context.Background(), Actor{
		UserID: adminUserID, Email: "  Event-ADMIN@amrita.edu ", Role: "admin",
	}, CreateCategoryInput{EventID: activeEventID, Name: "Procession"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	if audit.calls != 1 {
		t.Fatalf("WithAudit called %d times, want exactly 1", audit.calls)
	}
	if audit.event.Action != domain.ActionCategoryCreated {
		t.Errorf("action = %s, want category.created", audit.event.Action)
	}
	if *audit.event.TargetTable != "categories" {
		t.Errorf("target table = %s, want categories", *audit.event.TargetTable)
	}
	// The target id is bound INSIDE the WithAudit callback from the insert's
	// RETURNING value, exactly as Service.Create does for events — a target
	// id chosen before the write would name a row that may never exist.
	if *audit.event.TargetID != writer.id.String() {
		t.Errorf("target id = %s, want the id the insert returned (%s)", *audit.event.TargetID, writer.id)
	}
	// Actor identity is normalized from the SERVER-LOADED principal, never
	// from the request body.
	if *audit.event.ActorAccount != "event-admin@amrita.edu" {
		t.Errorf("actor account = %q, want the normalized principal email", *audit.event.ActorAccount)
	}
	if *audit.event.ActorRole != "admin" {
		t.Errorf("actor role = %q, want admin", *audit.event.ActorRole)
	}

	// Payload is stable, non-sensitive context. Asserting the decoded map
	// rather than a JSON string keeps this from failing on key order.
	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["event_id"] != activeEventID || payload["name"] != "Procession" {
		t.Errorf("payload = %#v, want event_id and name", payload)
	}
}

func TestCategoryCreateDerivesChangedFromTheInsertResult(t *testing.T) {
	// An insert that affected nothing is an error, not a silent success. A
	// hard-coded `return true` here would let WithAudit commit an audit row
	// describing a category that does not exist — precisely the divergence
	// the ledger is supposed to make impossible.
	writer := &countingCategoryWriter{noChange: true}
	svc := NewService(&fakeCategoryReader{state: "active"}, &fakeAuditStore{}, writer)

	_, err := svc.CreateCategory(context.Background(), adminActor(), CreateCategoryInput{
		EventID: activeEventID, Name: "Procession",
	})
	if err == nil {
		t.Fatal("a create that inserted nothing must be an error, not a no-op")
	}
}

func TestCategoryListReturnsOnlyTheRequestedEventInDeterministicOrder(t *testing.T) {
	// Ordering is the server's promise (name ASC, id ASC). The service must
	// pass the reader's order through untouched — a re-sort here would let the
	// portal and the API disagree about what "first" means, which is how
	// Story 2.1's cache-update ordering defect happened.
	reader := &fakeCategoryReader{
		state: "active",
		categories: map[string][]Category{
			activeEventID: {
				{ID: existingCategoryID, EventID: activeEventID, Name: "Procession"},
				{ID: otherEventID, EventID: activeEventID, Name: "Stage Setup"},
			},
			otherEventID: {{EventID: otherEventID, Name: "Registration"}},
		},
	}
	svc := NewService(reader, &fakeAuditStore{}, &countingCategoryWriter{})

	listed, err := svc.ListCategories(context.Background(), adminActor(), activeEventID)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(listed) != 2 || listed[0].Name != "Procession" || listed[1].Name != "Stage Setup" {
		t.Fatalf("listed = %#v, want the requested event's rows in server order", listed)
	}
	for _, category := range listed {
		if category.EventID != activeEventID {
			t.Errorf("category %q belongs to %s, not the requested event", category.Name, category.EventID)
		}
	}
}

func TestCategoryListRejectsAnUnknownEvent(t *testing.T) {
	svc := NewService(&fakeCategoryReader{state: "missing"}, &fakeAuditStore{}, &countingCategoryWriter{})
	_, err := svc.ListCategories(context.Background(), adminActor(), missingEventID)

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) || string(domainErr.Code) != "EVENT_NOT_FOUND" {
		t.Fatalf("error = %v, want EVENT_NOT_FOUND", err)
	}
}

func TestCategoryListRejectsAMalformedEventID(t *testing.T) {
	// A bad path segment is a field error, never a 500 and never a lookup.
	svc := NewService(&fakeCategoryReader{state: "active"}, &fakeAuditStore{}, &countingCategoryWriter{})
	_, err := svc.ListCategories(context.Background(), adminActor(), "not-a-uuid")

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("error = %v, want VALIDATION_FAILED", err)
	}
	if len(domainErr.Details) != 1 || domainErr.Details[0].Field != "event_id" {
		t.Errorf("detail = %#v, want field=event_id", domainErr.Details)
	}
}

func TestCategoryListSucceedsForAnArchivedEvent(t *testing.T) {
	// Archived is a CLOSED aggregate, not a hidden one. AC 7 requires the
	// portal to show an archived event's categories read-only, so the read
	// path must keep working after archival — only writes are refused.
	reader := &fakeCategoryReader{
		state:      "archived",
		categories: map[string][]Category{archivedEventID: {{EventID: archivedEventID, Name: "Procession"}}},
	}
	svc := NewService(reader, &fakeAuditStore{}, &countingCategoryWriter{})

	listed, err := svc.ListCategories(context.Background(), adminActor(), archivedEventID)
	if err != nil {
		t.Fatalf("listing an archived event's categories must succeed: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("listed = %#v, want the archived event's categories", listed)
	}
}

func TestCategoryCreateRefusesANonAdminActorWithoutTouchingTheDatabase(t *testing.T) {
	// Defense in depth behind rbac.RequireAdmin: the service is reachable from
	// any future caller, so it re-checks rather than trusting the route.
	writer := &countingCategoryWriter{}
	audit := &fakeAuditStore{}
	svc := NewService(&fakeCategoryReader{state: "active"}, audit, writer)

	_, err := svc.CreateCategory(context.Background(), Actor{
		UserID: adminUserID, Email: "student@amrita.edu", Role: "student",
	}, CreateCategoryInput{EventID: activeEventID, Name: "Procession"})

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("error = %v, want RBAC_FORBIDDEN", err)
	}
	if audit.calls != 0 || writer.calls != 0 {
		t.Errorf("a forbidden create reached the write boundary (audit=%d writer=%d)", audit.calls, writer.calls)
	}
}
