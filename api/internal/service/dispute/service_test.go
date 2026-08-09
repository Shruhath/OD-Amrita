// Red-phase unit tests for Story 0.11's dispute service.
//
// COMPILE-RED. This package holds only the Story 1.1 placeholder doc.go today, so
// the first failure is `undefined: NewService` and friends. The blast radius is
// exactly one package with no other tests in it.
//
// The prescribed surface, written out so the dev is not reverse-engineering it
// from compiler errors:
//
//	type Actor       struct { UserID, Role string }
//	type Student     struct { StudentID, RollNo, DisplayName, Branch, Section, Batch string }
//	type SubmitInput struct { StudentID, SubmittedEmail, EventID, CategoryID string
//	                          ODDate string   // "YYYY-MM-DD"; the day the OD belongs to
//	                          Slot int
//	                          ClaimedCoordinatorName, StudentNote *string }   // nil == absent (T6)
//	type Receipt     struct { DisputeID, Reference, Status string
//	                          SubmittedAt time.Time
//	                          Student Student }
//	type Record      struct { DisputeID, Status, ResolutionNote string; ... }
//	type ListFilter  struct { Page, PageSize int; Status string }
//	type Collection  struct { Data []Record; Total, Page, PageSize int }
//
//	type StudentReader interface { StudentByRollNo(ctx, rollNo string) (Student, error) }
//	type Repository    interface {
//	    Insert(ctx, pgx.Tx, InsertWrite) (Record, error)
//	    OpenForSlot(ctx, studentID, eventID, odDate string, slot int) (Record, error)
//	    ListForAdmin(ctx, ListFilter) ([]Record, int, error)
//	    ListForTeacher(ctx, actorUserID string, f ListFilter) ([]Record, int, error)
//	    ForResolution(ctx, pgx.Tx, actor Actor, disputeID string) (Record, error)
//	    LockedStatus(ctx, pgx.Tx, disputeID string) (string, bool, error)
//	    Resolve(ctx, pgx.Tx, ResolveWrite) (int64, error)
//	}
//	type AuditStore interface {
//	    WithAudit(ctx, store.AuditEvent, func(ctx, pgx.Tx) (bool, error)) error
//	}
//
//	func NewService(students StudentReader, disputes Repository, audited AuditStore, allowedDomain string) *Service
//
// `allowedDomain` is a CONSTRUCTOR parameter, not a package constant, because the
// domain check must be `svcauth.EnforceDomain(email, cfg.Auth.AllowedDomain)` —
// the same one value the auth middleware uses (T2). A second hard-coded copy is
// a second thing to get wrong the day the domain changes.
//
// NOT gated behind an env flag or a build tag. 0.9 shipped an entire suite behind
// ATDD_ACTIVE_09 that nothing ever set.
package dispute

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// The institutional convention, confirmed in the predecessor app rather than
// guessed: lowercase(roll_no)@cb.students.amrita.edu (T1).
const (
	fixtureRoll  = "CB.SC.U4CSE24101"
	fixtureEmail = "cb.sc.u4cse24101@cb.students.amrita.edu"
	fixtureID    = "95000000-0000-4000-8000-000000000101"
	otherID      = "95000000-0000-4000-8000-000000000102"
	fixtureEvent = "93000000-0000-4000-8000-000000000001"
	fixtureCat   = "94000000-0000-4000-8000-000000000001"
	// The day the disputed OD belongs to. Required from creation by the
	// 2026-08-06 multi-day amendment (docs/multi-day-events-plan.md §7): the
	// multi-day change makes an event
	// span a date range, and without this column "I was missed in slot 8" is
	// ambiguous across thirty days. Today an event is one day, so the only valid
	// value is the event's own date — the CONSTRAINT is what changes under 0.12a,
	// not the column.
	fixtureODDate = "2026-08-03"
	// fixtureDisputeID is the id a real InsertDispute RETURNING would produce.
	fixtureDisputeID = "97000000-0000-4000-8000-0000000000aa"
)

// fixtureSubmittedAt is the row's own created_at — the value FR51 requires the
// audit payload to carry, and which is only knowable after the write.
var fixtureSubmittedAt = time.Date(2026, 8, 3, 9, 15, 0, 0, time.UTC)

// fixedNow pins the service clock. Date bounds asserted against the real wall
// clock pass or fail by calendar date rather than by code.
var fixedNow = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// Fakes.
// ---------------------------------------------------------------------------

// fakeStudents records every roll number it was asked for, so a test can assert
// that a refused identity never reached the roster at all.
type fakeStudents struct {
	byRoll  map[string]Student
	queried []string
}

func (f *fakeStudents) StudentByRollNo(_ context.Context, rollNo string) (Student, error) {
	f.queried = append(f.queried, rollNo)
	student, ok := f.byRoll[rollNo]
	if !ok {
		return Student{}, domain.DomainError{Code: contracts.ErrorCodeSTUDENTNOTFOUND, Message: "No such student."}
	}
	return student, nil
}

// fakeRepo counts writes so "refused and wrote nothing" is an assertion rather
// than an assumption.
type fakeRepo struct {
	inserts  int
	resolves int
	rows     int64
	open     *Record
	record   Record
	listErr  error
	// lockedStatus is the status GetDisputeForResolution reads back under the
	// row lock when the guarded UPDATE matched nothing.
	lockedStatus string
	listedAs     []Actor
	// credits records every CreditSlot call, so a test can assert that a
	// REJECTED resolution wrote no attendance record — the direction that
	// silently manufactures attendance if it regresses.
	resolvedAs []ResolveWrite
	credits    []CreditWrite
	creditID   string
	creditMoot bool
	creditErr  error
}

func (r *fakeRepo) Insert(_ context.Context, _ pgx.Tx, _ InsertWrite) (Record, error) {
	r.inserts++
	return r.record, nil
}

func (r *fakeRepo) OpenForSlot(_ context.Context, _, _, _ string, _ int) (Record, error) {
	if r.open == nil {
		return Record{}, domain.DomainError{Code: contracts.ErrorCodeDISPUTENOTFOUND, Message: "No open dispute."}
	}
	return *r.open, nil
}

func (r *fakeRepo) ListForAdmin(_ context.Context, _ ListFilter) ([]Record, int, error) {
	r.listedAs = append(r.listedAs, Actor{Role: "admin"})
	return nil, 0, r.listErr
}

func (r *fakeRepo) ListForTeacher(_ context.Context, actorUserID string, _ ListFilter) ([]Record, int, error) {
	r.listedAs = append(r.listedAs, Actor{UserID: actorUserID, Role: "teacher"})
	return nil, 0, r.listErr
}

func (r *fakeRepo) ForResolution(_ context.Context, _ pgx.Tx, _ Actor, _ string) (Record, error) {
	return r.record, nil
}

// lockedStatus is what the guarded UPDATE's zero-row answer is disambiguated
// with. Defaults to an absent row, so a test that wants the 409 must say so.
func (r *fakeRepo) LockedStatus(_ context.Context, _ pgx.Tx, _ string) (string, bool, error) {
	if r.lockedStatus == "" {
		return "", false, nil
	}
	return r.lockedStatus, true, nil
}

func (r *fakeRepo) Resolve(_ context.Context, _ pgx.Tx, write ResolveWrite) (int64, error) {
	r.resolves++
	r.resolvedAs = append(r.resolvedAs, write)
	return r.rows, nil
}

func (r *fakeRepo) CreditSlot(_ context.Context, _ pgx.Tx, write CreditWrite) (string, bool, error) {
	r.credits = append(r.credits, write)
	if r.creditErr != nil {
		return "", false, r.creditErr
	}
	if r.creditMoot {
		return "", false, nil
	}
	id := r.creditID
	if id == "" {
		id = "11111111-1111-4111-8111-111111111111"
	}
	return id, true, nil
}

// fakeStore runs the write with a nil tx and records the audit event, so the
// service's audit attribution (T13) is assertable without a database.
type fakeStore struct {
	events []store.AuditEvent
	extra  []store.AuditEvent
	calls  int
}

// Audit collects the SECOND audit row an approval writes for the attendance
// record it created. Appending to the same slice keeps "how many audit rows did
// this resolution produce, and which" a single assertion.
func (s *fakeStore) Audit(_ context.Context, _ pgx.Tx, evt store.AuditEvent) error {
	s.extra = append(s.extra, evt)
	return nil
}

func (s *fakeStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	s.calls++
	changed, err := write(ctx, nil)
	if err != nil {
		return err
	}
	if changed {
		// store.WithAudit prefers PayloadFunc and evaluates it AFTER the write,
		// which is what lets the payload carry the inserted row's id and
		// timestamp. A double that skipped it made the payload unassertable.
		if evt.PayloadFunc != nil {
			payload, err := evt.PayloadFunc()
			if err != nil {
				return err
			}
			evt.Payload = payload
		}
		s.events = append(s.events, evt)
	}
	return nil
}

func newTestService(t *testing.T) (*Service, *fakeStudents, *fakeRepo, *fakeStore) {
	t.Helper()
	students := &fakeStudents{byRoll: map[string]Student{
		fixtureRoll: {StudentID: fixtureID, RollNo: fixtureRoll, DisplayName: "Ananya Rao", Branch: "CSE", Section: "A", Batch: "2024"},
	}}
	// A REALISTIC inserted row. This double used to return the zero Record, and
	// the service quietly minted a random uuid when it saw an empty id — so
	// "the receipt carries a reference" passed even though the repository never
	// produced one. The service no longer fabricates, so the double must be
	// honest about what a real insert returns.
	repo := &fakeRepo{rows: 1, record: Record{
		DisputeID:   fixtureDisputeID,
		Reference:   reference(fixtureDisputeID),
		Status:      "open",
		SubmittedAt: fixtureSubmittedAt,
	}}
	audited := &fakeStore{}
	svc := NewService(students, repo, audited, "amrita.edu")
	svc.now = func() time.Time { return fixedNow }
	return svc, students, repo, audited
}

func validInput() SubmitInput {
	return SubmitInput{
		StudentID:      fixtureID,
		SubmittedEmail: fixtureEmail,
		EventID:        fixtureEvent,
		CategoryID:     fixtureCat,
		ODDate:         fixtureODDate,
		Slot:           3,
	}
}

// ---------------------------------------------------------------------------
// Identity.
// ---------------------------------------------------------------------------

// 0.11-SVC-001 — THE test of this story. The three identity-failure paths return
// BYTE-IDENTICAL errors (AC 3, T3/T5).
//
// A bad domain, a well-formed address that resolves to no student, and an address
// that resolves to a DIFFERENT student than the one the form was opened on are
// three different internal facts and must be one external answer. Two different
// answers is a directory oracle: anyone on the internet can then ask "does
// cb.sc.u4cse24099@… exist" and read the answer off the status code.
//
// Compared as whole VALUES with reflect.DeepEqual rather than as three separate
// shape checks, because that is the only form of this assertion that catches a
// details[].field or a trailing full stop drifting apart later.
func TestSubmit_TheThreeIdentityRefusalsAreByteIdentical(t *testing.T) {
	svc, _, repo, audited := newTestService(t)
	ctx := context.Background()

	badDomain := validInput()
	badDomain.SubmittedEmail = "someone@gmail.com"

	unknownStudent := validInput()
	unknownStudent.SubmittedEmail = "cb.sc.u4cse24999@cb.students.amrita.edu"

	mismatch := validInput()
	mismatch.StudentID = otherID // resolves to the fixture student, not this one

	var got []error
	for _, tc := range []struct {
		name  string
		input SubmitInput
	}{
		{"domain outside the amrita family", badDomain},
		{"well-formed address matching no roll number", unknownStudent},
		{"email resolves to a different student than the selection", mismatch},
	} {
		_, err := svc.Submit(ctx, tc.input)
		if err == nil {
			t.Fatalf("[P0] %s was ACCEPTED; every one of these must be refused", tc.name)
		}
		var domainErr domain.DomainError
		if !errors.As(err, &domainErr) {
			t.Fatalf("[P0] %s returned %v, want a typed domain error", tc.name, err)
		}
		if domainErr.Code != contracts.ErrorCodeDISPUTEIDENTITYUNVERIFIED {
			t.Errorf("[P0] %s code = %q, want DISPUTE_IDENTITY_UNVERIFIED. One code for all three (T3): a "+
				"distinct code for \"no such student\" tells the caller the address does not exist, which is "+
				"exactly what the AC forbids disclosing", tc.name, domainErr.Code)
		}
		got = append(got, domainErr)
	}

	if len(got) == 3 {
		if !reflect.DeepEqual(got[0], got[1]) || !reflect.DeepEqual(got[1], got[2]) {
			t.Errorf("[P0] the three identity refusals are not identical values:\n  domain   = %#v\n  unknown  = %#v\n"+
				"  mismatch = %#v\nBuild all three from ONE constructor so they cannot drift — a message, a "+
				"details[].field or even a trailing full stop that differs is a readable oracle", got[0], got[1], got[2])
		}
	}

	// A refused identity writes nothing and audits nothing. This is not a
	// nice-to-have: an audit row per attempt turns the ledger into a log of every
	// address anyone probed with.
	if repo.inserts != 0 {
		t.Errorf("[P0] %d rows were inserted for refused identities, want 0", repo.inserts)
	}
	if audited.calls != 0 {
		t.Errorf("[P0] store.WithAudit was called %d times for refused identities, want 0", audited.calls)
	}
}

// 0.11-SVC-002 — the roll number is derived from the email LOCAL-PART and
// normalised before it is queried (T1).
//
// students.roll_no carries a strict CHECK: it must equal upper(btrim(roll_no))
// and match ^[A-Z0-9][A-Z0-9._/-]*$. A derived roll that fails that pattern can
// never match a row, so a local-part with surrounding whitespace or lowercase
// letters must be normalised rather than queried verbatim — and a local-part that
// cannot be a roll number at all is an unverified identity, never an error.
func TestSubmit_DerivesTheRollNumberFromTheLocalPartAndNormalisesIt(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		email string
		want  string
	}{
		{"lowercase institutional address", "cb.sc.u4cse24101@cb.students.amrita.edu", fixtureRoll},
		{"mixed case", "CB.Sc.U4cse24101@cb.students.amrita.edu", fixtureRoll},
		{"surrounding whitespace", "  cb.sc.u4cse24101@cb.students.amrita.edu  ", fixtureRoll},
		{"bare amrita.edu host still resolves (subdomain is not required)", "cb.sc.u4cse24101@amrita.edu", fixtureRoll},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, students, _, _ := newTestService(t)

			input := validInput()
			input.SubmittedEmail = tc.email

			if _, err := svc.Submit(ctx, input); err != nil {
				t.Fatalf("[P0] %s was refused: %v", tc.name, err)
			}
			if len(students.queried) != 1 {
				t.Fatalf("[P0] ran %d roster lookups, want 1", len(students.queried))
			}
			if students.queried[0] != tc.want {
				t.Errorf("[P0] queried roll_no = %q, want %q. Resolve as upper(trim(local_part)) — students.roll_no "+
					"has a CHECK requiring upper(btrim(roll_no)), so an unnormalised value can never match a row "+
					"and every real student would be told their identity is unverified",
					students.queried[0], tc.want)
			}
		})
	}

	// A local-part that cannot be a roll number is the SAME refusal, and it must
	// not reach the roster at all.
	for _, local := range []string{"first.last", "admin", "a b c", ""} {
		svc, students, _, _ := newTestService(t)
		input := validInput()
		input.SubmittedEmail = local + "@amrita.edu"

		_, err := svc.Submit(ctx, input)
		var domainErr domain.DomainError
		if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeDISPUTEIDENTITYUNVERIFIED {
			t.Errorf("[P0] local-part %q returned %v, want DISPUTE_IDENTITY_UNVERIFIED — a non-conforming "+
				"local-part is an unverified identity, not a 500", local, err)
		}
		if len(students.queried) != 0 {
			t.Errorf("[P1] local-part %q reached the roster as %q; a value that cannot match the roll_no CHECK "+
				"should be rejected before the query", local, students.queried[0])
		}
	}
}

// 0.11-SVC-003 — the domain check is EnforceDomain's, not a second copy (T2).
//
// EnforceDomain accepts the configured host and any subdomain at a label
// boundary. Both halves matter: tightening it to an exact host match rejects
// every real student (@cb.students.amrita.edu), and loosening it to a suffix
// match admits @amrita.edu.evil.com.
func TestSubmit_DomainCheckAcceptsSubdomainsAndRejectsLookalikes(t *testing.T) {
	ctx := context.Background()

	accepted := []string{
		"cb.sc.u4cse24101@amrita.edu",
		"cb.sc.u4cse24101@cb.students.amrita.edu",
		"cb.sc.u4cse24101@students.amrita.edu",
	}
	rejected := []string{
		"cb.sc.u4cse24101@amrita.edu.evil.com",
		"cb.sc.u4cse24101@notamrita.edu",
		"cb.sc.u4cse24101@amrita.edu@evil.com",
		"cb.sc.u4cse24101@",
		"cb.sc.u4cse24101",
	}

	for _, email := range accepted {
		svc, _, _, _ := newTestService(t)
		input := validInput()
		input.SubmittedEmail = email
		if _, err := svc.Submit(ctx, input); err != nil {
			t.Errorf("[P0] %q was refused: %v. EnforceDomain accepts the configured host AND any subdomain at a "+
				"label boundary — tightening it to an exact-host match rejects every real student, whose address "+
				"is @cb.students.amrita.edu (T2)", email, err)
		}
	}

	for _, email := range rejected {
		svc, _, repo, _ := newTestService(t)
		input := validInput()
		input.SubmittedEmail = email
		if _, err := svc.Submit(ctx, input); err == nil {
			t.Errorf("[P0] %q was ACCEPTED", email)
		}
		if repo.inserts != 0 {
			t.Errorf("[P0] %q wrote a row", email)
		}
	}
}

// 0.11-SVC-004 — the receipt carries the SERVER-RESOLVED student and never the
// submitted email (AC 2, AC 10).
//
// FR42's whole claim is that the personal fields came from the database rather
// than from the request body. A receipt built from the input would satisfy every
// shape assertion in the suite and prove nothing.
func TestSubmit_ReceiptCarriesTheResolvedStudentAndNoEmail(t *testing.T) {
	svc, _, _, _ := newTestService(t)

	receipt, err := svc.Submit(context.Background(), validInput())
	if err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}

	if receipt.Student.RollNo != fixtureRoll || receipt.Student.DisplayName != "Ananya Rao" {
		t.Errorf("[P0] receipt student = %+v, want the roster row for %s. FR42's auto-population is only real "+
			"if the display fields came from the DATABASE, not from the request", receipt.Student, fixtureRoll)
	}
	if receipt.Status != "open" {
		t.Errorf("[P0] receipt status = %q, want \"open\"", receipt.Status)
	}
	// Bound to the ROW's id, not merely non-empty. The service used to mint a
	// random uuid when the repository returned an empty record, so "a reference
	// exists" passed even if Insert never produced one — the assertion could not
	// fail for the reason it claimed.
	if receipt.DisputeID != fixtureDisputeID {
		t.Errorf("[P0] receipt dispute id = %q, want the inserted row's id %q", receipt.DisputeID, fixtureDisputeID)
	}
	if want := reference(fixtureDisputeID); receipt.Reference != want {
		t.Errorf("[P0] receipt reference = %q, want %q — the first 8 characters of the dispute uuid (T19). "+
			"NFR50's confirmation copy quotes it, and it is the only handle a student has on a dispute they "+
			"can never look up again", receipt.Reference, want)
	}
	if !receipt.SubmittedAt.Equal(fixtureSubmittedAt) {
		t.Errorf("[P0] receipt submitted_at = %v, want the row's created_at %v", receipt.SubmittedAt, fixtureSubmittedAt)
	}

	// The receipt is a PUBLIC value. Allowlist its exported fields for the same
	// reason 0.10 allowlists PublicStudent: a blocklist of names somebody already
	// thought of can never fail.
	allowed := map[string]bool{"DisputeID": true, "Reference": true, "Status": true, "SubmittedAt": true, "Student": true}
	typ := reflect.TypeOf(receipt)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		if !allowed[field.Name] {
			t.Errorf("[P0] Receipt carries field %q, which is not on the public allowlist. This value is "+
				"serialised to an UNAUTHENTICATED response — SubmittedEmail in particular would turn the receipt "+
				"into a confirmation that the address resolved (AC 10)", field.Name)
		}
	}
}

// 0.11-SVC-005 — the audit row is attributed to the anonymous submitter (T13,
// AC 5).
//
// There is no provisioned principal here, which is exactly why the attribution
// has to be pinned: the tempting alternatives are a null actor (an unattributable
// row) or a synthetic system user (a row that lies about who acted).
func TestSubmit_AuditRowNamesTheSubmitterAndTargetsTheDispute(t *testing.T) {
	svc, _, _, audited := newTestService(t)

	input := validInput()
	input.SubmittedEmail = "CB.SC.U4CSE24101@cb.students.amrita.edu"

	if _, err := svc.Submit(context.Background(), input); err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}
	if len(audited.events) != 1 {
		t.Fatalf("[P0] wrote %d audit events, want exactly 1", len(audited.events))
	}
	evt := audited.events[0]

	if evt.Action != domain.ActionDisputeSubmitted {
		t.Errorf("[P0] audit action = %q, want %q — it is ALREADY registered in domain.AllActions; do not add a "+
			"second name for the same event (T12)", evt.Action, domain.ActionDisputeSubmitted)
	}
	if evt.ActorAccount == nil || *evt.ActorAccount != strings.ToLower(input.SubmittedEmail) {
		t.Errorf("[P0] actor_account = %v, want the lowercased submitted email %q. The ledger is INTERNAL, so "+
			"the address belongs there — it is the only identifier an unauthenticated submitter has (T13)",
			evt.ActorAccount, strings.ToLower(input.SubmittedEmail))
	}
	if evt.ActorRole == nil || *evt.ActorRole != "student" {
		t.Errorf("[P0] actor_role = %v, want \"student\" — an unauthenticated submitter, not a provisioned "+
			"principal and not a synthetic system user", evt.ActorRole)
	}
	if evt.TargetTable == nil || *evt.TargetTable != "disputes" {
		t.Errorf("[P0] target_table = %v, want \"disputes\". It must NOT be od_records: 0.10's CI conservation "+
			"gate counts audit rows joined on target_table = 'od_records' for the public fixture events, and a "+
			"dispute row landing there would break a gate this story is forbidden to relax (T20)", evt.TargetTable)
	}
	// The target id is late-bound through a pointer that the write closure sets,
	// so it can only be right if store.WithAudit dereferences AFTER the write.
	// Nothing asserted it, and an audit-first refactor would have silently
	// written target_id = '' — which the new web-deploy conservation gate joins
	// on, so the CI failure would have been remote from the cause.
	if evt.TargetID == nil || *evt.TargetID != fixtureDisputeID {
		t.Errorf("[P0] target_id = %v, want the inserted dispute's id %q", evt.TargetID, fixtureDisputeID)
	}

	// FR51 fixes the payload's CONTENTS, and nothing read them back. The
	// timestamp in particular was simply absent: submitPayload marshalled the
	// form fields and stopped, while T13 requires "the form contents, the
	// selected event/category/slot and the timestamp".
	if evt.Payload == nil {
		t.Fatalf("[P0] the audit event carries no payload; FR51 requires the form contents, the selected " +
			"event/category/slot and the timestamp")
	}
	var payload map[string]any
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		t.Fatalf("[P0] audit payload is not JSON: %v", err)
	}
	for _, key := range []string{
		"dispute_id", "student_id", "submitted_email", "event_id",
		"category_id", "od_date", "slot", "submitted_at",
	} {
		if _, ok := payload[key]; !ok {
			t.Errorf("[P0] audit payload is missing %q. FR51 fixes these contents; the ledger is the "+
				"authoritative record of a dispute nobody authenticated, so a missing field cannot be "+
				"recovered from anywhere else (T13)", key)
		}
	}
	if got, _ := payload["submitted_at"].(string); got != fixtureSubmittedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("[P0] audit payload submitted_at = %q, want the row's own created_at %q — not a value "+
			"re-derived before the write", got, fixtureSubmittedAt.UTC().Format(time.RFC3339Nano))
	}
	if got, _ := payload["dispute_id"].(string); got != fixtureDisputeID {
		t.Errorf("[P0] audit payload dispute_id = %q, want %q", got, fixtureDisputeID)
	}
}

// ---------------------------------------------------------------------------
// Bounds.
// ---------------------------------------------------------------------------

// 0.11-SVC-006 — every bounded field is refused at the service, and a refusal
// runs no query and writes no row (AC 4).
//
// The handler bounds these too. Both layers are deliberate: the handler catches a
// JSON null before it can nil-deref, and the service is the layer a future caller
// (the portal, a script, a test) reaches without going through the handler.
func TestSubmit_RefusesOutOfBoundsFieldsBeforeAnyQuery(t *testing.T) {
	ctx := context.Background()

	tooLongNote := strings.Repeat("x", 1001)
	tooLongCoordinator := strings.Repeat("y", 201)
	tooLongEmail := strings.Repeat("a", 310) + "@cb.students.amrita.edu"

	for _, tc := range []struct {
		name    string
		mutate  func(*SubmitInput)
		wantErr contracts.ErrorCode
	}{
		{"slot 0", func(i *SubmitInput) { i.Slot = 0 }, contracts.ErrorCodeVALIDATIONFAILED},
		{"slot 13", func(i *SubmitInput) { i.Slot = 13 }, contracts.ErrorCodeVALIDATIONFAILED},
		{"slot -1", func(i *SubmitInput) { i.Slot = -1 }, contracts.ErrorCodeVALIDATIONFAILED},
		{"note over 1000 chars", func(i *SubmitInput) { i.StudentNote = &tooLongNote }, contracts.ErrorCodeVALIDATIONFAILED},
		{"coordinator name over 200 chars", func(i *SubmitInput) { i.ClaimedCoordinatorName = &tooLongCoordinator }, contracts.ErrorCodeVALIDATIONFAILED},
		{"email over 320 chars", func(i *SubmitInput) { i.SubmittedEmail = tooLongEmail }, contracts.ErrorCodeVALIDATIONFAILED},
		{"malformed student uuid", func(i *SubmitInput) { i.StudentID = "not-a-uuid" }, contracts.ErrorCodeVALIDATIONFAILED},
		{"malformed event uuid", func(i *SubmitInput) { i.EventID = "42" }, contracts.ErrorCodeVALIDATIONFAILED},
		{"blank category", func(i *SubmitInput) { i.CategoryID = "" }, contracts.ErrorCodeVALIDATIONFAILED},
		// od_date, added by the 2026-08-06 amendment. An absent or malformed day
		// must be refused rather than defaulted: silently substituting the event
		// date would file a thirty-day event's dispute against the wrong day, and
		// under 0.12a that is the difference between two distinct disputes and a
		// duplicate.
		{"absent od_date", func(i *SubmitInput) { i.ODDate = "" }, contracts.ErrorCodeVALIDATIONFAILED},
		{"malformed od_date", func(i *SubmitInput) { i.ODDate = "03-08-2026" }, contracts.ErrorCodeVALIDATIONFAILED},
		{"od_date as a timestamp", func(i *SubmitInput) { i.ODDate = "2026-08-03T00:00:00Z" }, contracts.ErrorCodeVALIDATIONFAILED},
		// NOT "outside the event range" — the service has no notion of an event
		// range, and must not: T22 makes the range a DATABASE constraint because
		// the day arrives from an anonymous submitter. What the service refuses
		// is a day too far in the future to belong to any event that already
		// happened. Computed relative to the fake clock; the previous hardcoded
		// 2026-09-14 asserted "is in the future" and would have turned red on
		// 2026-09-13 for a reason unrelated to the code.
		{"od_date implausibly far in the future", func(i *SubmitInput) {
			i.ODDate = fixedNow.Add(72 * time.Hour).Format(time.DateOnly)
		}, contracts.ErrorCodeVALIDATIONFAILED},
		// Length caps are counted in RUNES. A byte count refused a 400-character
		// Malayalam note as "at most 1000 characters" while the browser, the
		// contract and the database CHECK all counted characters.
		{"note of 1000 multi-byte runes is accepted", func(i *SubmitInput) {
			note := strings.Repeat("ന", 1000)
			i.StudentNote = &note
		}, ""},
		{"note of 1001 multi-byte runes is refused", func(i *SubmitInput) {
			note := strings.Repeat("ന", 1001)
			i.StudentNote = &note
		}, contracts.ErrorCodeVALIDATIONFAILED},
		// A NUL in free text is rejected by Postgres from INSIDE the insert with
		// SQLSTATE 22021, which would reach an anonymous caller as a 500 for
		// input they fully control.
		{"NUL byte in the note", func(i *SubmitInput) {
			note := "was there\x00really"
			i.StudentNote = &note
		}, contracts.ErrorCodeVALIDATIONFAILED},
		{"control character in the coordinator name", func(i *SubmitInput) {
			name := "Priya\x07Menon"
			i.ClaimedCoordinatorName = &name
		}, contracts.ErrorCodeVALIDATIONFAILED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, students, repo, audited := newTestService(t)
			input := validInput()
			tc.mutate(&input)

			_, err := svc.Submit(ctx, input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("[P0] %s was refused: %v", tc.name, err)
				}
				return
			}
			var domainErr domain.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("[P0] %s returned %v, want a typed domain error", tc.name, err)
			}
			if domainErr.Code != tc.wantErr {
				t.Errorf("[P0] %s code = %q, want %q", tc.name, domainErr.Code, tc.wantErr)
			}
			if len(students.queried) != 0 || repo.inserts != 0 || audited.calls != 0 {
				t.Errorf("[P0] %s reached the database (roster=%d inserts=%d audits=%d); an out-of-bounds value "+
					"on an OPEN endpoint must be refused at the edge, before it can cost a query",
					tc.name, len(students.queried), repo.inserts, audited.calls)
			}
		})
	}

	// The optional fields are genuinely optional (T6). A student disputing a
	// MISSING slot has no record and therefore no coordinator name to read;
	// requiring one blocks exactly the person this form exists for.
	svc, _, _, _ := newTestService(t)
	if _, err := svc.Submit(ctx, validInput()); err != nil {
		t.Errorf("[P0] a submission with no coordinator name and no note was refused: %v. Story 5.7 makes "+
			"coordinator selection optional (\"I don't know\" allowed) — nil means absent, not empty (T6)", err)
	}
}

// ---------------------------------------------------------------------------
// Resolution.
// ---------------------------------------------------------------------------

// 0.11-SVC-007 — only a teacher or an admin may list or resolve, and a
// coordinator/student never reaches a query (AC 11, AC 13).
//
// Enforced in the SERVICE rather than only on the route, because the route is
// markAPI — which carries RequirePrincipal but not a role gate — so the handler
// alone cannot tell a coordinator from a teacher.
func TestListAndResolve_RefuseCoordinatorsAndStudentsBeforeAnyQuery(t *testing.T) {
	ctx := context.Background()

	for _, role := range []string{"coordinator", "student", "", "unknown"} {
		t.Run("role="+role, func(t *testing.T) {
			svc, _, repo, audited := newTestService(t)
			actor := Actor{UserID: "90000000-0000-4000-8000-000000000005", Role: role}

			_, listErr := svc.List(ctx, actor, ListFilter{Page: 1, PageSize: 20})
			var listDomainErr domain.DomainError
			if !errors.As(listErr, &listDomainErr) || listDomainErr.Code != contracts.ErrorCodeRBACFORBIDDEN {
				t.Errorf("[P0] List as %q returned %v, want RBAC_FORBIDDEN — this endpoint returns another "+
					"student's submitted email and note", role, listErr)
			}

			_, resolveErr := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "Corrected on the register.", OutcomeApproved)
			var resolveDomainErr domain.DomainError
			if !errors.As(resolveErr, &resolveDomainErr) || resolveDomainErr.Code != contracts.ErrorCodeRBACFORBIDDEN {
				t.Errorf("[P0] Resolve as %q returned %v, want RBAC_FORBIDDEN", role, resolveErr)
			}

			if len(repo.listedAs) != 0 {
				t.Errorf("[P0] role %q reached a list query; a refused role must never touch the database", role)
			}
			if repo.resolves != 0 || audited.calls != 0 {
				t.Errorf("[P0] role %q reached the resolve write", role)
			}
		})
	}
}

// 0.11-SVC-008 — a teacher is dispatched to the BRANCH-SCOPED query and an admin
// to the unscoped one (AC 11, T16).
//
// Scoping must happen in SQL. Reading everything and filtering in Go moves every
// other branch's disputes — with their submitted emails — through the process
// that is about to serialise a response, and one careless refactor later they are
// on the wire.
func TestList_DispatchesTeachersToTheScopedQueryAndAdminsToTheUnscopedOne(t *testing.T) {
	ctx := context.Background()

	svc, _, repo, _ := newTestService(t)
	teacher := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "teacher"}
	if _, err := svc.List(ctx, teacher, ListFilter{Page: 1, PageSize: 20}); err != nil {
		t.Fatalf("[P0] List as teacher: %v", err)
	}
	if len(repo.listedAs) != 1 || repo.listedAs[0].Role != "teacher" {
		t.Fatalf("[P0] a teacher was not dispatched to ListForTeacher; got %+v. A teacher sees disputes for "+
			"students in their OWN branches only, joined through user_branches IN SQL (T16)", repo.listedAs)
	}
	if repo.listedAs[0].UserID != teacher.UserID {
		t.Errorf("[P0] ListForTeacher was called with user id %q, want the acting teacher's %q — the scope key "+
			"is the ACTOR, and passing anything else scopes the query to the wrong person's branches",
			repo.listedAs[0].UserID, teacher.UserID)
	}

	svc, _, repo, _ = newTestService(t)
	if _, err := svc.List(ctx, Actor{UserID: "90000000-0000-4000-8000-000000000001", Role: "admin"}, ListFilter{Page: 1, PageSize: 20}); err != nil {
		t.Fatalf("[P0] List as admin: %v", err)
	}
	if len(repo.listedAs) != 1 || repo.listedAs[0].Role != "admin" {
		t.Errorf("[P0] an admin was not dispatched to ListForAdmin; got %+v", repo.listedAs)
	}
}

// 0.11-SVC-009 — an empty or whitespace-only resolution note is refused BEFORE
// any write (AC 12).
//
// The note IS the resolution. A dispute closed with an empty note records that
// somebody clicked a button and nothing about why — and because 0.12 owns the
// actual record repair (T15), the note is the only durable trace of the decision.
func TestResolve_RefusesAnEmptyNoteBeforeWriting(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "teacher"}

	for _, note := range []string{"", "   ", "\t\n", " "} {
		svc, _, repo, audited := newTestService(t)

		_, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", note, OutcomeApproved)
		var domainErr domain.DomainError
		if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeVALIDATIONFAILED {
			t.Errorf("[P0] note %q returned %v, want VALIDATION_FAILED", note, err)
		}
		if repo.resolves != 0 || audited.calls != 0 {
			t.Errorf("[P0] note %q reached the write (resolves=%d audits=%d)", note, repo.resolves, audited.calls)
		}
	}
}

// 0.11-SVC-010 — a guarded resolve that affects ZERO rows is re-read to
// distinguish 409 from 404, never guessed (AC 13).
//
// `ResolveDispute` is guarded `WHERE status = 'open'`, so a raced double-resolve
// affects 0 rows rather than overwriting the first resolution. Zero rows means
// EITHER already-resolved OR gone, and answering one of them by assumption is how
// a teacher gets "not found" for a dispute sitting in front of them.
func TestResolve_ZeroRowsIsDisambiguatedByARereadNotAGuess(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "teacher"}

	svc, _, repo, _ := newTestService(t)
	repo.rows = 0
	repo.record = Record{DisputeID: "97000000-0000-4000-8000-000000000001", Status: "resolved"}
	// The answer now comes from GetDisputeForResolution's row-locked read inside
	// the transaction, not from a re-read after it committed.
	repo.lockedStatus = "resolved"

	_, err := svc.Resolve(ctx, actor, repo.record.DisputeID, "Corrected on the register.", OutcomeApproved)
	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("[P0] Resolve on an already-resolved dispute returned %v, want a typed domain error", err)
	}
	if domainErr.Code != contracts.ErrorCodeDISPUTEALREADYRESOLVED {
		t.Errorf("[P0] code = %q, want DISPUTE_ALREADY_RESOLVED. Zero affected rows means already-resolved OR "+
			"gone; re-read to tell them apart rather than picking one — a 404 for a dispute the teacher is "+
			"looking at reads as data loss (AC 13)", domainErr.Code)
	}

	// A successful resolve co-commits exactly one dispute.resolved audit row.
	svc, _, repo, audited := newTestService(t)
	repo.rows = 1
	if _, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "Corrected on the register.", OutcomeApproved); err != nil {
		t.Fatalf("[P0] Resolve: %v", err)
	}
	if len(audited.events) != 1 {
		t.Fatalf("[P0] wrote %d audit events on a successful resolve, want 1", len(audited.events))
	}
	if audited.events[0].Action != domain.ActionDisputeResolved {
		t.Errorf("[P0] audit action = %q, want %q. Add ActionDisputeResolved to BOTH the const block and "+
			"AllActions in api/internal/domain/events.go — events_test.go fails otherwise, and store.WithAudit "+
			"rejects an unregistered action at runtime (T12)", audited.events[0].Action, domain.ActionDisputeResolved)
	}
	if audited.events[0].ActorAccount == nil || *audited.events[0].ActorAccount == "" ||
		audited.events[0].ActorRole == nil || *audited.events[0].ActorRole != "teacher" {
		t.Errorf("[P0] the resolve audit row does not name the acting teacher: %+v", audited.events[0])
	}
	if audited.events[0].Payload == nil && audited.events[0].PayloadFunc == nil {
		t.Errorf("[P0] the resolve audit row carries no payload; FR37/FR50 require the previous status and the " +
			"note to be permanently recorded")
	}
}

// THE APPROVAL WRITES THE RECORD.
//
// The defect this covers: resolving a dispute used to be an annotation and
// nothing else. A teacher agreed the student had been missed, wrote a note, and
// the student still did not appear in od_records or on the public dashboard —
// the credit was a separate trip to another surface that nothing prompted, so in
// practice it never happened.
func TestResolve_ApprovalCreditsTheDisputedSlot(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "teacher"}

	svc, _, repo, audited := newTestService(t)
	repo.rows = 1
	repo.creditID = "22222222-2222-4222-8222-222222222222"

	got, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "Coordinator confirmed it.", OutcomeApproved)
	if err != nil {
		t.Fatalf("[P0] Resolve: %v", err)
	}
	if len(repo.credits) != 1 {
		t.Fatalf("[P0] an approved resolution called CreditSlot %d times, want 1 — approving a dispute must "+
			"write the attendance record, or the student stays missing from the day they disputed", len(repo.credits))
	}
	// The credit is keyed on the DISPUTE, not on caller-supplied facts: the
	// student, event, day and slot are read from the row inside the query, so an
	// approval cannot credit a slot other than the one that was disputed.
	if repo.credits[0].DisputeID != "97000000-0000-4000-8000-000000000001" {
		t.Errorf("[P0] credited dispute %q, want the one being resolved", repo.credits[0].DisputeID)
	}
	if repo.credits[0].Note != "Coordinator confirmed it." {
		t.Errorf("[P0] reason_note = %q, want the teacher's resolution note — od_records_manual_reason_note_ck "+
			"requires one for any non-coordinator actor", repo.credits[0].Note)
	}
	if got.ODRecordID == nil || *got.ODRecordID != repo.creditID {
		t.Errorf("[P0] the response carries ODRecordID %v, want %q — the teacher's screen has to be able to say "+
			"the slot was credited, not merely that the dispute was closed", got.ODRecordID, repo.creditID)
	}

	// TWO auditable facts, not one. An od_records row that appeared with nothing
	// in the ledger behind it is the gap the ledger exists to close.
	if len(audited.extra) != 1 {
		t.Fatalf("[P0] wrote %d extra audit rows, want 1 od.created for the record the approval created", len(audited.extra))
	}
	if audited.extra[0].Action != domain.ActionODCreated {
		t.Errorf("[P0] extra audit action = %q, want %q", audited.extra[0].Action, domain.ActionODCreated)
	}
	if audited.extra[0].TargetTable == nil || *audited.extra[0].TargetTable != "od_records" ||
		audited.extra[0].TargetID == nil || *audited.extra[0].TargetID != repo.creditID {
		t.Errorf("[P0] the od.created row does not point at the created record: %+v", audited.extra[0])
	}
}

// A REJECTION MUST NOT MANUFACTURE ATTENDANCE.
//
// This is the direction that makes the outcome field necessary at all. Wiring
// resolution to the record without it would credit every dispute — including the
// ones a teacher resolves precisely BECAUSE the student was not there.
func TestResolve_RejectionWritesNoRecord(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "admin"}

	svc, _, repo, audited := newTestService(t)
	repo.rows = 1

	got, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "The register shows the student absent.", OutcomeRejected)
	if err != nil {
		t.Fatalf("[P0] Resolve: %v", err)
	}
	if len(repo.credits) != 0 {
		t.Errorf("[P0] a REJECTED resolution credited the slot (%d calls). A rejection is the teacher saying the "+
			"student was not there; crediting it manufactures attendance nobody approved", len(repo.credits))
	}
	if got.ODRecordID != nil {
		t.Errorf("[P0] a rejection reported od_record_id %v, want nil", *got.ODRecordID)
	}
	if len(audited.extra) != 0 {
		t.Errorf("[P0] a rejection wrote %d od.created audit rows, want 0", len(audited.extra))
	}
	// The decision itself is still recorded, and the outcome reaches the row.
	if len(repo.resolvedAs) != 1 || repo.resolvedAs[0].Outcome != OutcomeRejected {
		t.Errorf("[P0] the stored outcome was %+v, want rejected — an inbox that cannot tell an approval from a "+
			"refusal cannot be audited", repo.resolvedAs)
	}
}

// THE OUTCOME IS REQUIRED, and there is no default.
//
// An absent outcome is ambiguous in the one direction that matters: defaulting
// to approved credits slots nobody approved, defaulting to rejected silently
// drops the credit the teacher meant to give.
func TestResolve_RefusesAnAbsentOrUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "admin"}

	for _, outcome := range []string{"", "   ", "approve", "APPROVED", "resolved", "yes", "true"} {
		t.Run("outcome="+outcome, func(t *testing.T) {
			svc, _, repo, _ := newTestService(t)
			repo.rows = 1

			_, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "A note.", outcome)
			var domainErr domain.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("[P0] Resolve with outcome %q returned %v, want a typed validation error", outcome, err)
			}
			if domainErr.Code != contracts.ErrorCodeVALIDATIONFAILED {
				t.Errorf("[P0] code = %q, want VALIDATION_FAILED", domainErr.Code)
			}
			if repo.resolves != 0 {
				t.Errorf("[P0] an unusable outcome still reached the database")
			}
			if len(repo.credits) != 0 {
				t.Errorf("[P0] an unusable outcome still credited a slot")
			}
		})
	}
}

// AN ALREADY-MARKED SLOT IS MOOT, NOT FAILED.
//
// A coordinator catching up, or a teacher fixing the record by hand while the
// dispute sat open, must not turn the teacher's approval into an error: the end
// state they asked for already holds. The dispute still resolves.
func TestResolve_ApprovalOfAnAlreadyMarkedSlotStillResolves(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "admin"}

	svc, _, repo, audited := newTestService(t)
	repo.rows = 1
	repo.creditMoot = true

	got, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "Already marked; closing.", OutcomeApproved)
	if err != nil {
		t.Fatalf("[P0] an approval over an already-marked slot failed with %v; the desired end state already holds", err)
	}
	if got.ODRecordID != nil {
		t.Errorf("[P0] reported od_record_id %v for a record it did not create", *got.ODRecordID)
	}
	if len(audited.extra) != 0 {
		t.Errorf("[P0] wrote %d od.created audit rows for a record it did not create, want 0", len(audited.extra))
	}
}

// A FAILED CREDIT ROLLS THE RESOLUTION BACK.
//
// The teacher must never be shown "resolved" over a slot that was not credited.
// Both facts move together or neither does.
func TestResolve_AFailedCreditFailsTheWholeResolution(t *testing.T) {
	ctx := context.Background()
	actor := Actor{UserID: "90000000-0000-4000-8000-000000000004", Role: "admin"}

	svc, _, repo, audited := newTestService(t)
	repo.rows = 1
	repo.creditErr = errors.New("od_records insert refused")

	if _, err := svc.Resolve(ctx, actor, "97000000-0000-4000-8000-000000000001", "A note.", OutcomeApproved); err == nil {
		t.Fatal("[P0] Resolve succeeded although the credit failed — that reports a dispute as resolved over a " +
			"slot that was never credited, which is the exact defect this change exists to remove")
	}
	if len(audited.events) != 0 {
		t.Errorf("[P0] a failed credit still left %d dispute.resolved audit rows behind", len(audited.events))
	}
}
