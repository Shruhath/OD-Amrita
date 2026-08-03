// Marking tests in the mark service.
//
// Three separate concerns live here because they all need package-internal
// access to `odrecord`:
//
//	the narrow widening of the identity/capture rule (student_id + camera);
//	the requirement that the mark write go through the scope seam;
//	the server-computed counters and slot window (FR13/FR14).
package odrecord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// ---------------------------------------------------------------------------
// 0.6-PAIR-001 — identity <-> capture_method pairing
//
// Story 0.4 pairs `card` with nfc|camera, and `student_id` with search ONLY.
// But nothing in Epic 0 writes a sealed token to a QR/barcode — 0.5 writes the
// token to the NFC TAG. What the camera actually reads is the barcode already
// printed on the university ID card, which carries a ROLL NUMBER. So the camera
// path necessarily resolves to a student_id, and without this widening there is
// no legal request shape for a camera mark at all.
//
// This is a required, narrow, documented change to a `done` story's rule. It is
// NOT a licence to loosen the card branch, which these tests pin byte-for-byte.
// ---------------------------------------------------------------------------

func studentIDPtr(v string) *string { return &v }

func TestValidateIdentityCapturePair_AcceptsStudentIDForCameraAndSearch(t *testing.T) {
	for _, method := range []CaptureMethod{CaptureMethodCamera, CaptureMethodSearch} {
		t.Run(string(method), func(t *testing.T) {
			input := MarkInput{
				EventID:       "11111111-1111-4111-8111-111111111111",
				CategoryID:    "33333333-3333-4333-8333-333333333333",
				CaptureMethod: method,
				StudentID:     studentIDPtr("55555555-5555-4555-8555-555555555555"),
			}
			if err := validateIdentityCapturePair(input); err != nil {
				t.Fatalf("[P0] student_id + %s rejected (%v) — the camera reads a printed ROLL NUMBER, not a sealed token", method, err)
			}
		})
	}
}

func TestValidateIdentityCapturePair_StillRejectsStudentIDForNFC(t *testing.T) {
	input := MarkInput{
		EventID:       "11111111-1111-4111-8111-111111111111",
		CategoryID:    "33333333-3333-4333-8333-333333333333",
		CaptureMethod: CaptureMethodNFC,
		StudentID:     studentIDPtr("55555555-5555-4555-8555-555555555555"),
	}
	if err := validateIdentityCapturePair(input); err == nil {
		t.Fatal("[P0] student_id + nfc was accepted — an NFC tap always carries a sealed card, never a bare id")
	}
}

// The card branch must come out of Task 3 unchanged. A dev who "simplifies" the
// function into accept-everything passes the two tests above and fails this one.
func TestValidateIdentityCapturePair_CardBranchIsUnchanged(t *testing.T) {
	card := &CardInput{Version: "v1", Token: "not-a-real-token"}

	for _, method := range []CaptureMethod{CaptureMethodNFC, CaptureMethodCamera} {
		input := MarkInput{CaptureMethod: method, Card: card}
		if err := validateIdentityCapturePair(input); err != nil {
			t.Errorf("[P0] card + %s rejected (%v) — 0.4's card branch must not narrow", method, err)
		}
	}

	input := MarkInput{CaptureMethod: CaptureMethodSearch, Card: card}
	if err := validateIdentityCapturePair(input); err == nil {
		t.Error("[P0] card + search was accepted — 0.4's card branch must not widen either")
	}
}

// ---------------------------------------------------------------------------
// 0.6-SCOPE-002 — the mark write goes through the scope seam
//
// NFR11: no scope may be reachable by URL manipulation. If scope were enforced
// only in the read handlers, a coordinator could still POST /api/v1/mark with
// any event_id they liked. The guard therefore belongs in the SERVICE, before
// any write, so the reads and the write can never disagree.
// ---------------------------------------------------------------------------

// recordingGuard is the fake for the Task 2 seam. It records the arguments and
// the moment it was called relative to the write.
type recordingGuard struct {
	calls  [][3]string // actorRole, eventID, categoryID
	err    error
	called bool
}

func (g *recordingGuard) AssertCanMark(_ context.Context, actor Actor, eventID, categoryID string) error {
	g.called = true
	g.calls = append(g.calls, [3]string{actor.Role, eventID, categoryID})
	return g.err
}

var errScopeRefused = errors.New("scope refused")

func TestMark_CallsScopeGuardBeforeAnyWrite(t *testing.T) {
	guard := &recordingGuard{err: errScopeRefused}
	writer := &countingWriter{}

	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, guard, stubCreditLedger())

	_, err := svc.Mark(context.Background(), Actor{UserID: "u", Role: "coordinator"}, MarkInput{
		EventID:       "11111111-1111-4111-8111-111111111111",
		CategoryID:    "33333333-3333-4333-8333-333333333333",
		CaptureMethod: CaptureMethodSearch,
		StudentID:     studentIDPtr("55555555-5555-4555-8555-555555555555"),
	})

	if !errors.Is(err, errScopeRefused) {
		t.Fatalf("[P0] Mark = %v, want the guard's refusal to propagate", err)
	}
	if !guard.called {
		t.Fatal("[P0] Mark never consulted AssertCanMark — scope enforced in the handler only is reachable by URL manipulation (NFR11)")
	}
	if writer.inserts != 0 {
		t.Fatalf("[P0] Mark wrote %d rows despite a refused scope — the guard must run BEFORE any write", writer.inserts)
	}
}

func TestMark_PassesTheRequestedScopeToTheGuardVerbatim(t *testing.T) {
	guard := &recordingGuard{err: errScopeRefused}
	svc := NewService(stubReader(), stubCardResolver(), &countingWriter{}, stubAuditStore(), scaffoldClock{}, guard, stubCreditLedger())

	const wantEvent = "11111111-1111-4111-8111-111111111111"
	const wantCategory = "33333333-3333-4333-8333-333333333333"

	_, _ = svc.Mark(context.Background(), Actor{UserID: "u", Role: "coordinator"}, MarkInput{
		EventID:       wantEvent,
		CategoryID:    wantCategory,
		CaptureMethod: CaptureMethodSearch,
		StudentID:     studentIDPtr("55555555-5555-4555-8555-555555555555"),
	})

	if len(guard.calls) != 1 {
		t.Fatalf("[P0] guard called %d times, want exactly 1", len(guard.calls))
	}
	got := guard.calls[0]
	if got[1] != wantEvent || got[2] != wantCategory {
		t.Errorf("[P0] guard received event=%q category=%q, want the ids from the request (%q/%q)", got[1], got[2], wantEvent, wantCategory)
	}
}

// ---------------------------------------------------------------------------
// 0.6-STATUS-001 — server-computed counters and slot window
//
// FR13 wants two counters, FR14 the time left in the active slot. Both are
// SERVER-derived: the client never computes a slot boundary and never counts a
// mark itself. `server_time` must come from the SAME clock read as the slot
// decision, or the coordinator's countdown and the server's FR12 answer can
// disagree by a poll interval — which shows as "the timer said 12 seconds left
// but it rejected my tap".
// ---------------------------------------------------------------------------

type scaffoldClock struct{ at time.Time }

func (c scaffoldClock) Now() time.Time {
	if c.at.IsZero() {
		return time.Date(2026, 9, 4, 9, 0, 0, 0, campusTZ)
	}
	return c.at
}

// stubCounts is the fake for the new counter reader. Real counting is proven
// against Postgres in api/test/odrecord — here we only prove the service asks
// the right question and reports both figures separately.
type stubCounts struct {
	mine          int
	categoryTotal int
	scopes        [][3]string // actorUserID, eventID, categoryID
	err           error
}

func (s *stubCounts) CountMarks(_ context.Context, actorUserID, eventID, categoryID string) (int, int, error) {
	s.scopes = append(s.scopes, [3]string{actorUserID, eventID, categoryID})
	return s.mine, s.categoryTotal, s.err
}

func TestMarkStatus_ReportsBothCountersSeparately(t *testing.T) {
	counts := &stubCounts{mine: 7, categoryTotal: 23}
	svc := NewStatusService(stubReader(), counts, stubCreditLedger(), scaffoldClock{})

	status, err := svc.MarkStatus(context.Background(),
		Actor{UserID: "coord-1", Role: "coordinator"},
		"11111111-1111-4111-8111-111111111111",
		"33333333-3333-4333-8333-333333333333",
	)
	if err != nil {
		t.Fatalf("MarkStatus = %v, want nil", err)
	}
	if status.Counters.Mine != 7 {
		t.Errorf("[P0] Counters.Mine = %d, want 7", status.Counters.Mine)
	}
	if status.Counters.CategoryTotal != 23 {
		t.Errorf("[P0] Counters.CategoryTotal = %d, want 23 (all coordinators combined, FR13)", status.Counters.CategoryTotal)
	}
	if len(counts.scopes) != 1 || counts.scopes[0][0] != "coord-1" {
		t.Errorf("[P0] counter query scope = %+v, want it filtered by the calling coordinator", counts.scopes)
	}
}

// The slot window is read from the SAME clock read that produced server_time.
func TestMarkStatus_SlotWindowAndServerTimeShareOneClockRead(t *testing.T) {
	// 09:00 IST on the event date sits inside slot 3 (09:40 is its end... slot 2
	// runs 08:50-09:40), so this asserts a live slot rather than a boundary.
	at := time.Date(2026, 9, 4, 9, 0, 0, 0, campusTZ)
	svc := NewStatusService(stubReaderOn(at), &stubCounts{}, stubCreditLedger(), scaffoldClock{at: at})

	status, err := svc.MarkStatus(context.Background(),
		Actor{UserID: "coord-1", Role: "coordinator"},
		"11111111-1111-4111-8111-111111111111",
		"33333333-3333-4333-8333-333333333333",
	)
	if err != nil {
		t.Fatalf("MarkStatus = %v, want nil", err)
	}
	if !status.ServerTime.Equal(at) {
		t.Errorf("[P0] ServerTime = %v, want the injected clock reading %v (never time.Now())", status.ServerTime, at)
	}
	if status.Slot == nil {
		t.Fatal("[P0] Slot = nil at 09:00 on the event date — slot 2 (08:50-09:40) is open")
	}
	if status.Slot.Slot != 2 {
		t.Errorf("[P0] Slot.Slot = %d, want 2 — reuse odrecord.ActiveSlot, never a second slot table", status.Slot.Slot)
	}
	if !status.ServerTime.Before(status.Slot.End) {
		t.Error("[P0] ServerTime is not before Slot.End — the client's countdown would start negative")
	}
	if status.ServerTime.Before(status.Slot.Start) {
		t.Error("[P0] ServerTime precedes Slot.Start — the window and the clock read disagree")
	}
}

// AC 7: "when no slot is active the screen says so plainly". The server must
// express that as an explicit absence, never as slot 0 with a zero window.
func TestMarkStatus_ReturnsNilSlotWhenNoSlotIsOpen(t *testing.T) {
	// 03:00 IST is before slot 1 (08:00) and after slot 12 ended the day before.
	at := time.Date(2026, 9, 4, 3, 0, 0, 0, campusTZ)
	svc := NewStatusService(stubReaderOn(at), &stubCounts{}, stubCreditLedger(), scaffoldClock{at: at})

	status, err := svc.MarkStatus(context.Background(),
		Actor{UserID: "coord-1", Role: "coordinator"},
		"11111111-1111-4111-8111-111111111111",
		"33333333-3333-4333-8333-333333333333",
	)
	if err != nil {
		t.Fatalf("MarkStatus = %v, want nil error — no open slot is a valid state, not a failure", err)
	}
	if status.Slot != nil {
		t.Fatalf("[P0] Slot = %+v at 03:00, want nil — `slot: null` is the no-slot state, never slot 0", status.Slot)
	}
	if !status.ServerTime.Equal(at) {
		t.Errorf("[P0] ServerTime = %v, want %v even with no slot open", status.ServerTime, at)
	}
}

// A day that is not the event date has no slots at all — ActiveSlot compares
// against events.event_date, which is why any live-slot test must seed an event
// dated TODAY.
func TestMarkStatus_ReturnsNilSlotOnADayThatIsNotTheEventDate(t *testing.T) {
	eventDate := time.Date(2026, 9, 4, 0, 0, 0, 0, campusTZ)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, campusTZ) // one day later, mid-slot-2 time-of-day

	svc := NewStatusService(stubReaderOn(eventDate), &stubCounts{}, stubCreditLedger(), scaffoldClock{at: at})

	status, err := svc.MarkStatus(context.Background(),
		Actor{UserID: "coord-1", Role: "coordinator"},
		"11111111-1111-4111-8111-111111111111",
		"33333333-3333-4333-8333-333333333333",
	)
	if err != nil {
		t.Fatalf("MarkStatus = %v, want nil", err)
	}
	if status.Slot != nil {
		t.Errorf("[P0] Slot = %+v on a non-event day, want nil", status.Slot)
	}
}

// ---------------------------------------------------------------------------
// Shared stubs. These are deliberately minimal: the real behaviour they stand in
// for is proven against Postgres in api/test/odrecord.
// ---------------------------------------------------------------------------

const (
	scaffoldEventID    = "11111111-1111-4111-8111-111111111111"
	scaffoldCategoryID = "33333333-3333-4333-8333-333333333333"
	scaffoldStudentID  = "55555555-5555-4555-8555-555555555555"
	scaffoldActorID    = "77777777-7777-4777-8777-777777777777"
)

// countingWriter records whether a write was attempted. The scope tests above
// rely on `inserts` staying at 0 when the guard refuses.
type countingWriter struct {
	inserts int
	err     error
}

func (w *countingWriter) InsertPresent(_ context.Context, _ pgx.Tx, input RecordInput) (MarkResult, error) {
	w.inserts++
	if w.err != nil {
		return MarkResult{}, w.err
	}
	return MarkResult{
		RecordID:         "66666666-6666-4666-8666-666666666666",
		Student:          input.Student,
		EventID:          input.EventID,
		CategoryID:       input.CategoryID,
		Slot:             input.Slot,
		CaptureMethod:    input.CaptureMethod,
		ServerReceivedAt: input.ServerReceivedAt,
	}, nil
}

// scaffoldReader is a minimal in-memory Reader: one active event and one
// category that belongs to it.
type scaffoldReader struct{ eventDate time.Time }

func (r scaffoldReader) EventForMark(_ context.Context, eventID string) (Event, error) {
	if eventID != scaffoldEventID {
		return Event{}, ErrEventNotFound
	}
	return Event{EventID: scaffoldEventID, EventDate: r.eventDate, Status: "active"}, nil
}

func (r scaffoldReader) CategoryForEvent(_ context.Context, eventID, categoryID string) (Category, error) {
	if eventID != scaffoldEventID || categoryID != scaffoldCategoryID {
		return Category{}, ErrCategoryNotFound
	}
	return Category{CategoryID: scaffoldCategoryID, EventID: scaffoldEventID}, nil
}

func (r scaffoldReader) StudentForMark(_ context.Context, studentID string) (Student, error) {
	if studentID != scaffoldStudentID {
		return Student{}, ErrStudentNotFound
	}
	return Student{
		StudentID:   scaffoldStudentID,
		RollNo:      "CB.SC.U4CSE24001",
		DisplayName: "Scaffold Student",
		Branch:      "CSE",
		Section:     "A",
		Batch:       "2024",
	}, nil
}

func stubReader() Reader { return stubReaderOn(time.Date(2026, 9, 4, 0, 0, 0, 0, campusTZ)) }

// stubReaderOn returns a Reader whose single event is dated `eventDate`.
func stubReaderOn(eventDate time.Time) Reader { return scaffoldReader{eventDate: eventDate} }

// refusingCardResolver is never reached by these scaffolds — every scenario
// here uses the student_id identity path. If it IS reached, that is the defect.
type refusingCardResolver struct{}

func (refusingCardResolver) Resolve(context.Context, Actor, CardInput) (Student, error) {
	return Student{}, ErrCardNotRecognized
}

func stubCardResolver() CardResolver { return refusingCardResolver{} }

// passthroughAudit runs the callback without a real transaction. The scope
// tests must never get this far; the audit-row conservation property is proven
// against real Postgres in api/test/odrecord instead.
type passthroughAudit struct{}

func (passthroughAudit) WithAudit(ctx context.Context, _ store.AuditEvent, fn func(context.Context, pgx.Tx) (bool, error)) error {
	_, err := fn(ctx, nil)
	return err
}

func stubAuditStore() AuditStore { return passthroughAudit{} }

type recordingAuditStore struct {
	actions  []domain.Action
	payloads [][]byte
}

func (s *recordingAuditStore) WithAudit(ctx context.Context, evt store.AuditEvent, fn func(context.Context, pgx.Tx) (bool, error)) error {
	changed, err := fn(ctx, nil)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	s.actions = append(s.actions, evt.Action)
	if evt.PayloadFunc != nil {
		payload, err := evt.PayloadFunc()
		if err != nil {
			return err
		}
		s.payloads = append(s.payloads, payload)
		return nil
	}
	s.payloads = append(s.payloads, evt.Payload)
	return nil
}

func (s *recordingAuditStore) payloadContains(value string) bool {
	for _, payload := range s.payloads {
		if strings.Contains(string(payload), value) {
			return true
		}
	}
	return false
}

type fixedCreditLedger struct {
	state  FallbackCreditState
	debits int
	err    error
}

func (l *fixedCreditLedger) Debit(context.Context, pgx.Tx, string, string) (FallbackCreditState, error) {
	l.debits++
	if l.err != nil {
		return FallbackCreditState{}, l.err
	}
	return l.state, nil
}

func (l *fixedCreditLedger) State(context.Context, string, string) (FallbackCreditState, error) {
	if l.err != nil {
		return FallbackCreditState{}, l.err
	}
	return l.state, nil
}

func stubCreditLedger() *fixedCreditLedger {
	return &fixedCreditLedger{state: FallbackCreditState{Limit: 5, Consumed: 1, Remaining: 4, Exhausted: false, WarningThreshold: 1}}
}

// 0.6-SCOPE-002 — a missing scope guard is refused at COMPOSITION time.
//
// NewService used to map a nil guard onto an unexported allow-everything policy,
// under a comment claiming that made a nil guard "fail loudly". It did the
// opposite: a composition root that forgot the argument got a mark endpoint with
// no scope enforcement at all, silently, and no test noticed. This pins the
// corrected behaviour so the fallback cannot quietly come back.
func TestNewService_RefusesANilScopeGuard(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("[P0] NewService accepted a nil ScopeGuard — a mark path with no scope enforcement " +
				"must never be constructible by omission (C4/NFR11)")
		}
		message, ok := recovered.(string)
		if !ok || !strings.Contains(message, "ScopeGuard") {
			t.Errorf("[P0] panic = %v, want a message naming the missing ScopeGuard so the fix is obvious", recovered)
		}
	}()

	NewService(stubReader(), stubCardResolver(), &countingWriter{}, stubAuditStore(), scaffoldClock{}, nil, stubCreditLedger())
}

// 0.6-SCOPE-002b — the explicit opt-out still works, so tests that are genuinely
// not about scope have a supported way to say so IN SOURCE.
func TestNewService_AcceptsTheExplicitAllowAnyScopeOptOut(t *testing.T) {
	svc := NewService(stubReader(), stubCardResolver(), &countingWriter{}, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, stubCreditLedger())
	if svc == nil {
		t.Fatal("[P0] NewService returned nil for an explicit AllowAnyScope")
	}
}
