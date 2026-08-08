//go:build atdd

package odrecord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestCoordinatorCardMarkCreatesODRecordAndAuditInSameTransaction(t *testing.T) {
	ctx := context.Background()
	fixture := newServiceFixture(t, time.Date(2026, 8, 3, 8, 25, 0, 0, campusLocation(t)))
	svc := NewService(fixture.reader, fixture.cardResolver, fixture.writer, fixture.auditStore, fixture.clock, AllowAnyScope{}, fixture.creditLedger)

	result, err := svc.Mark(ctx, coordinatorActor(), MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: CaptureMethodNFC,
		Card:          &CardInput{Version: "v1", Token: "QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"},
	})
	if err != nil {
		t.Fatalf("Mark coordinator card: %v", err)
	}

	if result.Student.DisplayName != "Ananya Rao" || result.Student.StudentID != fixture.studentID {
		t.Fatalf("marked student = %+v", result.Student)
	}
	if result.Slot != 1 {
		t.Fatalf("slot = %d, want server-derived slot 1", result.Slot)
	}
	if fixture.writer.lastRecord.StudentID != fixture.studentID || fixture.writer.lastRecord.EventID != fixture.eventID || fixture.writer.lastRecord.CategoryID != fixture.categoryID {
		t.Fatalf("record = %+v, want resolved student/event/category", fixture.writer.lastRecord)
	}
	if fixture.auditStore.changedWrites != 1 || fixture.auditStore.action != domain.ActionODCreated {
		t.Fatalf("audit writes/action = %d/%s, want one od.created", fixture.auditStore.changedWrites, fixture.auditStore.action)
	}
	if bytes.Contains(fixture.auditStore.payload, []byte("QlJS")) {
		t.Fatalf("audit payload leaked raw card token: %s", fixture.auditStore.payload)
	}
}

func TestDuplicateUniqueIndexViolationMapsToScanDuplicate(t *testing.T) {
	fixture := newServiceFixture(t, time.Date(2026, 8, 3, 8, 25, 0, 0, campusLocation(t)))
	fixture.writer.err = ErrDuplicatePresentRecord
	svc := NewService(fixture.reader, fixture.cardResolver, fixture.writer, fixture.auditStore, fixture.clock, AllowAnyScope{}, fixture.creditLedger)

	_, err := svc.Mark(context.Background(), coordinatorActor(), MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: CaptureMethodNFC,
		Card:          &CardInput{Version: "v1", Token: "QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"},
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeSCANDUPLICATE {
		t.Fatalf("error = %v, want SCAN_DUPLICATE", err)
	}
}

func TestUnknownCardIsRejectedBeforeSlotClosedAudit(t *testing.T) {
	fixture := newServiceFixture(t, time.Date(2026, 8, 3, 10, 35, 0, 0, campusLocation(t)))
	fixture.cardResolver.err = ErrCardNotRecognized
	svc := NewService(fixture.reader, fixture.cardResolver, fixture.writer, fixture.auditStore, fixture.clock, AllowAnyScope{}, fixture.creditLedger)

	_, err := svc.Mark(context.Background(), coordinatorActor(), MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: CaptureMethodNFC,
		Card:          &CardInput{Version: "v1", Token: "unknown-card-token"},
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeCARDNOTRECOGNIZED {
		t.Fatalf("error = %v, want CARD_NOT_RECOGNIZED before slot checks", err)
	}
	if fixture.auditStore.changedWrites != 0 {
		t.Fatalf("blocked audit writes = %d, want none for unknown card", fixture.auditStore.changedWrites)
	}
}

func TestCoordinatorOutsideSlotWritesBlockedAuditOnly(t *testing.T) {
	fixture := newServiceFixture(t, time.Date(2026, 8, 3, 10, 35, 0, 0, campusLocation(t)))
	svc := NewService(fixture.reader, fixture.cardResolver, fixture.writer, fixture.auditStore, fixture.clock, AllowAnyScope{}, fixture.creditLedger)

	_, err := svc.Mark(context.Background(), coordinatorActor(), MarkInput{
		EventID:       fixture.eventID,
		CategoryID:    fixture.categoryID,
		CaptureMethod: CaptureMethodNFC,
		Card:          &CardInput{Version: "v1", Token: "QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"},
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeSLOTCLOSED {
		t.Fatalf("error = %v, want SLOT_CLOSED", err)
	}
	if fixture.writer.calls != 0 {
		t.Fatalf("OD record writer was called %d times for a closed slot", fixture.writer.calls)
	}
	if fixture.auditStore.changedWrites != 1 || fixture.auditStore.action != domain.ActionScanBlocked {
		t.Fatalf("blocked audit writes/action = %d/%s, want scan.blocked", fixture.auditStore.changedWrites, fixture.auditStore.action)
	}
}

func TestTeacherAndAdminUseExplicitSearchSlotOutsideLiveWindow(t *testing.T) {
	for _, actor := range []Actor{
		{UserID: uuid.NewString(), Email: "teacher@amrita.edu", Role: "teacher"},
		{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"},
	} {
		t.Run(actor.Role, func(t *testing.T) {
			fixture := newServiceFixture(t, time.Date(2026, 8, 3, 19, 0, 0, 0, campusLocation(t)))
			svc := NewService(fixture.reader, fixture.cardResolver, fixture.writer, fixture.auditStore, fixture.clock, AllowAnyScope{}, fixture.creditLedger)
			result, err := svc.Mark(context.Background(), actor, MarkInput{
				EventID:       fixture.eventID,
				CategoryID:    fixture.categoryID,
				CaptureMethod: CaptureMethodSearch,
				StudentID:     &fixture.studentID,
				Slot:          intPtr(12),
			})
			if err != nil {
				t.Fatalf("Mark search slot: %v", err)
			}
			if result.Slot != 12 || fixture.writer.lastRecord.CaptureMethod != CaptureMethodSearch {
				t.Fatalf("result/record = %+v/%+v, want explicit search slot 12", result, fixture.writer.lastRecord)
			}
		})
	}
}

func TestValidationRejectsAmbiguousIdentityUnsupportedRoleAndRawTokenPersistence(t *testing.T) {
	fixture := newServiceFixture(t, time.Date(2026, 8, 3, 8, 25, 0, 0, campusLocation(t)))
	svc := NewService(fixture.reader, fixture.cardResolver, fixture.writer, fixture.auditStore, fixture.clock, AllowAnyScope{}, fixture.creditLedger)
	for _, tc := range []struct {
		name  string
		actor Actor
		input MarkInput
		code  contracts.ErrorCode
	}{
		{
			name:  "student caller",
			actor: Actor{UserID: uuid.NewString(), Email: "student@amrita.edu", Role: "student"},
			input: MarkInput{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: CaptureMethodSearch, StudentID: &fixture.studentID, Slot: intPtr(1)},
			code:  contracts.ErrorCodeRBACFORBIDDEN,
		},
		{
			name:  "both identity sources",
			actor: coordinatorActor(),
			input: MarkInput{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: CaptureMethodNFC, StudentID: &fixture.studentID, Card: &CardInput{Version: "v1", Token: "token"}},
			code:  contracts.ErrorCodeVALIDATIONFAILED,
		},
		{
			name:  "teacher nfc",
			actor: Actor{UserID: uuid.NewString(), Email: "teacher@amrita.edu", Role: "teacher"},
			input: MarkInput{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: CaptureMethodNFC, Card: &CardInput{Version: "v1", Token: "token"}, Slot: intPtr(1)},
			code:  contracts.ErrorCodeRBACFORBIDDEN,
		},
		{
			name:  "nfc with direct student id",
			actor: coordinatorActor(),
			input: MarkInput{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: CaptureMethodNFC, StudentID: &fixture.studentID},
			code:  contracts.ErrorCodeVALIDATIONFAILED,
		},
		{
			name:  "search with card identity",
			actor: coordinatorActor(),
			input: MarkInput{EventID: fixture.eventID, CategoryID: fixture.categoryID, CaptureMethod: CaptureMethodSearch, Card: &CardInput{Version: "v1", Token: "token"}},
			code:  contracts.ErrorCodeVALIDATIONFAILED,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Mark(context.Background(), tc.actor, tc.input)
			var de domain.DomainError
			if !errors.As(err, &de) || de.Code != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestActiveSlotCampusScheduleBoundaries(t *testing.T) {
	loc := campusLocation(t)
	eventDate := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	for _, tc := range []struct {
		name string
		now  time.Time
		slot int
		ok   bool
	}{
		{name: "slot 1 inclusive start", now: time.Date(2026, 8, 3, 8, 0, 0, 0, loc), slot: 1, ok: true},
		{name: "slot 1 exclusive end enters slot 2", now: time.Date(2026, 8, 3, 8, 50, 0, 0, loc), slot: 2, ok: true},
		{name: "break rejected", now: time.Date(2026, 8, 3, 10, 35, 0, 0, loc), ok: false},
		{name: "slot 12 inclusive start", now: time.Date(2026, 8, 3, 17, 25, 0, 0, loc), slot: 12, ok: true},
		{name: "after slot 12 rejected", now: time.Date(2026, 8, 3, 18, 15, 0, 0, loc), ok: false},
		{name: "wrong event date rejected", now: time.Date(2026, 8, 4, 8, 25, 0, 0, loc), ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A one-day event is start == end, which is what this suite has always
			// described: the "wrong event date rejected" case below is now rejected
			// by range containment rather than by date equality, and must stay red
			// for a day outside the range either way.
			slot, _, ok := ActiveSlot(tc.now, eventDate, eventDate)
			if ok != tc.ok || slot != tc.slot {
				t.Fatalf("ActiveSlot() = %d, %v; want %d, %v", slot, ok, tc.slot, tc.ok)
			}
		})
	}
}

type serviceFixture struct {
	eventID      string
	categoryID   string
	studentID    string
	reader       *fakeReader
	cardResolver *fakeCardResolver
	writer       *fakeWriter
	auditStore   *fakeAuditStore
	clock        fixedClock
	creditLedger *atddCreditLedger
}

func newServiceFixture(t *testing.T, now time.Time) serviceFixture {
	t.Helper()
	studentID := uuid.NewString()
	eventID := uuid.NewString()
	categoryID := uuid.NewString()
	student := Student{StudentID: studentID, RollNo: "CB.SC.U4CSE24001", DisplayName: "Ananya Rao", Branch: "CSE", Section: "A", Batch: "2024"}
	return serviceFixture{
		eventID:    eventID,
		categoryID: categoryID,
		studentID:  studentID,
		reader: &fakeReader{
			event:    Event{EventID: eventID, EventDate: now, EndDate: now, Status: "active"},
			category: Category{CategoryID: categoryID, EventID: eventID},
			student:  student,
		},
		cardResolver: &fakeCardResolver{student: student},
		writer:       &fakeWriter{},
		auditStore:   &fakeAuditStore{},
		clock:        fixedClock{now: now},
		creditLedger: &atddCreditLedger{state: FallbackCreditState{Limit: 5, Consumed: 0, Remaining: 5, WarningThreshold: 1}},
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeReader struct {
	event    Event
	category Category
	student  Student
}

func (r *fakeReader) EventForMark(context.Context, string) (Event, error) { return r.event, nil }
func (r *fakeReader) CategoryForEvent(context.Context, string, string) (Category, error) {
	return r.category, nil
}
func (r *fakeReader) StudentForMark(context.Context, string) (Student, error) { return r.student, nil }

type fakeCardResolver struct {
	student Student
	err     error
}

func (r *fakeCardResolver) Resolve(ctx context.Context, actor Actor, input CardInput) (Student, error) {
	if r.err != nil {
		return Student{}, r.err
	}
	return r.student, nil
}

type fakeWriter struct {
	calls      int
	lastRecord RecordInput
	err        error
}

func (w *fakeWriter) InsertPresent(ctx context.Context, tx pgx.Tx, input RecordInput) (MarkResult, error) {
	w.calls++
	w.lastRecord = input
	if w.err != nil {
		return MarkResult{}, w.err
	}
	return MarkResult{
		RecordID:         uuid.NewString(),
		Student:          input.Student,
		EventID:          input.EventID,
		CategoryID:       input.CategoryID,
		Slot:             input.Slot,
		CaptureMethod:    input.CaptureMethod,
		ServerReceivedAt: input.ServerReceivedAt,
	}, nil
}

type fakeAuditStore struct {
	changedWrites int
	action        domain.Action
	payload       []byte
}

func (s *fakeAuditStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	changed, err := write(ctx, nil)
	if changed {
		s.changedWrites++
		s.action = evt.Action
		if evt.PayloadFunc != nil {
			payload, err := evt.PayloadFunc()
			if err != nil {
				return err
			}
			s.payload = payload
		} else {
			s.payload = evt.Payload
		}
		if len(s.payload) > 0 && !json.Valid(s.payload) {
			return errors.New("invalid audit payload")
		}
	}
	return err
}

type atddCreditLedger struct {
	state  FallbackCreditState
	debits int
	err    error
}

func (l *atddCreditLedger) Debit(context.Context, pgx.Tx, string, string, time.Time) (FallbackCreditState, error) {
	l.debits++
	if l.err != nil {
		return FallbackCreditState{}, l.err
	}
	return l.state, nil
}

func (l *atddCreditLedger) State(context.Context, string, string, time.Time) (FallbackCreditState, error) {
	if l.err != nil {
		return FallbackCreditState{}, l.err
	}
	return l.state, nil
}

func coordinatorActor() Actor {
	return Actor{UserID: uuid.NewString(), Email: "coordinator@amrita.edu", Role: "coordinator"}
}

func campusLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load campus location: %v", err)
	}
	return loc
}

func intPtr(v int) *int { return &v }
