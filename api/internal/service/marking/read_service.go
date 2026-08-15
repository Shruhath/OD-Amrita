package marking

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	odrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// ReadService is the live marking surface's read façade.
//
// It exists so the four read handlers depend on ONE thing, and so no handler can
// reach the event reader or the student service directly and serve a scope the
// mark path would refuse.
//
// THE FOUR READS DO NOT ALL USE THE SAME HALF OF THE SEAM, and the difference is
// deliberate rather than an oversight:
//
//   - VisibleEvents, Categories and MarkStatus go through the FULL policy —
//     role, assignment/event, and (for MarkStatus) category ownership. They describe a
//     scope, so they must agree exactly with what the write would accept.
//   - SearchStudents goes through the ROLE half only (assertCoordinator, which
//     lives in scope.go beside the policy — not a second copy of it). The roster
//     is not scoped to an event: a coordinator must be able to find a student
//     before knowing which category they belong in, and the camera path resolves
//     a roll number to an id before any scope is asserted. The mark itself is
//     still gated by AssertCanMark, so a search result confers no ability to
//     mark. Roster READ access is separately bounded by student.assertMaySearch.
//
// The assignment lookup narrows the scoped event/category/status reads. It does
// NOT narrow this one. An unassigned coordinator may still search the roster,
// because a search result alone grants no ability to mark.
type ReadService struct {
	policy   *Policy
	events   svcevent.Reader
	students StudentSearcher
	status   StatusReader
}

// StudentSearcher is the roster search, narrowed to the one method this surface
// may call. The admin import and list paths are deliberately out of reach.
type StudentSearcher interface {
	Search(context.Context, svcstudent.Actor, svcstudent.SearchInput) ([]svcstudent.SearchResult, error)
}

// StatusReader is the counters-and-slot read.
type StatusReader interface {
	MarkStatus(ctx context.Context, actor odrecord.Actor, eventID, categoryID string) (odrecord.Status, error)
}

func NewReadService(policy *Policy, events svcevent.Reader, students StudentSearcher, status StatusReader) *ReadService {
	return &ReadService{policy: policy, events: events, students: students, status: status}
}

// Category is the marking surface's view of a category.
type Category struct {
	CategoryID string
	EventID    string
	Name       string
	CreatedAt  time.Time
}

// Student is a search hit. Display fields only — see student.SearchResult.
type Student struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

// SlotWindow is the active slot's bounds, in server time.
type SlotWindow struct {
	Slot      int
	StartedAt time.Time
	EndsAt    time.Time
}

type Counters struct {
	Mine          int
	CategoryTotal int
}

// Status is nil-Slot when no slot is open. That absence is the state.
type Status struct {
	ServerTime     time.Time
	Slot           *SlotWindow
	Counters       Counters
	FallbackCredit FallbackCreditState
}

type FallbackCreditState struct {
	Limit            int
	Consumed         int
	Remaining        int
	Exhausted        bool
	WarningThreshold int
}

func (s *ReadService) VisibleEvents(ctx context.Context, actor Actor) ([]Event, error) {
	events, err := s.policy.VisibleEvents(ctx, actor)
	if err != nil {
		return nil, AsDomainError(err)
	}
	return events, nil
}

// Categories lists an event's categories, and only for an actor the seam admits
// for that event.
func (s *ReadService) Categories(ctx context.Context, actor Actor, eventID string) ([]Category, error) {
	// No category has been chosen yet, so this asks the policy's role-and-event
	// half rather than re-deriving it here.
	if err := s.policy.assertEventScope(ctx, actor, eventID); err != nil {
		return nil, AsDomainError(err)
	}
	parsed, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return nil, AsDomainError(ErrEventNotFound)
	}
	rows, err := s.events.ListCategories(ctx, parsed)
	if err != nil {
		return nil, err
	}
	categories := make([]Category, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, Category{
			CategoryID: row.ID,
			EventID:    row.EventID,
			Name:       row.Name,
			CreatedAt:  row.CreatedAt,
		})
	}
	return categories, nil
}

// SearchStudents is the ROLE-gated read. See the type doc above for why it is
// not event-scoped, and what the assignment story has to decide about it.
//
// Deliberately NOT assertLiveMarker: Story 0.9's teacher/admin search-mark
// screen calls this exact endpoint before it can select a student to mark, so
// live-marker-only here would 403 the entire teacher/admin surface at the
// first step.
func (s *ReadService) SearchStudents(ctx context.Context, actor Actor, query string, limit int) ([]Student, error) {
	if err := assertMaySearchRoster(actor); err != nil {
		return nil, err
	}
	rows, err := s.students.Search(ctx, svcstudent.Actor{
		UserID: actor.UserID,
		Email:  actor.Email,
		Role:   actor.Role,
	}, svcstudent.SearchInput{Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	students := make([]Student, 0, len(rows))
	for _, row := range rows {
		students = append(students, Student(row))
	}
	return students, nil
}

func (s *ReadService) MarkStatus(ctx context.Context, actor Actor, eventID, categoryID string) (Status, error) {
	if err := s.policy.AssertCanMark(ctx, actor, eventID, categoryID); err != nil {
		return Status{}, AsDomainError(err)
	}
	raw, err := s.status.MarkStatus(ctx, odrecord.Actor{
		UserID: actor.UserID,
		Email:  actor.Email,
		Role:   actor.Role,
	}, eventID, categoryID)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		ServerTime: raw.ServerTime,
		Counters:   Counters{Mine: raw.Counters.Mine, CategoryTotal: raw.Counters.CategoryTotal},
		FallbackCredit: FallbackCreditState{
			Limit:            raw.FallbackCredit.Limit,
			Consumed:         raw.FallbackCredit.Consumed,
			Remaining:        raw.FallbackCredit.Remaining,
			Exhausted:        raw.FallbackCredit.Exhausted,
			WarningThreshold: raw.FallbackCredit.WarningThreshold,
		},
	}
	if raw.Slot != nil {
		status.Slot = &SlotWindow{Slot: raw.Slot.Slot, StartedAt: raw.Slot.Start, EndsAt: raw.Slot.End}
	}
	return status, nil
}

// assertMaySearchRoster is intentionally exact-match, mirroring assertCoordinator's
// own reasoning: role strings come from the users table, and a case-insensitive
// compare here would silently admit a row written by some future path that did
// not canonicalise.
func assertMaySearchRoster(actor Actor) error {
	switch actor.Role {
	case roleCoordinator, "teacher", roleAdmin:
		return nil
	default:
		return forbiddenPortal("Coordinator, teacher, or administrator access is required to search students.")
	}
}
