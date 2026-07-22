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

// TestCategoryRejectionMatrixIsInternallyCoherent runs today and guards the
// table itself: a field error must carry a field and a detail code, and a
// non-field error must carry neither. This is cheap, but it is the reason the
// table cannot rot into something that looks specified while contradicting the
// wire format every other layer asserts.
func TestCategoryRejectionMatrixIsInternallyCoherent(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range categoryRejections {
		if seen[tc.name] {
			t.Errorf("duplicate rejection case %q", tc.name)
		}
		seen[tc.name] = true

		isFieldError := tc.wantCode == "VALIDATION_FAILED"
		if isFieldError && (tc.wantField == "" || tc.wantDetail == "") {
			t.Errorf("%s: VALIDATION_FAILED must carry a field and a detail code", tc.name)
		}
		if !isFieldError && (tc.wantField != "" || tc.wantDetail != "") {
			t.Errorf("%s: %s must not carry field details", tc.name, tc.wantCode)
		}
		if isFieldError && tc.wantStatus != 400 {
			t.Errorf("%s: VALIDATION_FAILED must be 400, got %d", tc.name, tc.wantStatus)
		}
	}
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
	writer := &countingCategoryWriter{err: &pgconn.PgError{Code: "55000"}}
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

	listed, err := svc.ListCategories(context.Background(), activeEventID)
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
	_, err := svc.ListCategories(context.Background(), missingEventID)

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) || string(domainErr.Code) != "EVENT_NOT_FOUND" {
		t.Fatalf("error = %v, want EVENT_NOT_FOUND", err)
	}
}

func TestCategoryListRejectsAMalformedEventID(t *testing.T) {
	// A bad path segment is a field error, never a 500 and never a lookup.
	svc := NewService(&fakeCategoryReader{state: "active"}, &fakeAuditStore{}, &countingCategoryWriter{})
	_, err := svc.ListCategories(context.Background(), "not-a-uuid")

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

	listed, err := svc.ListCategories(context.Background(), archivedEventID)
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
