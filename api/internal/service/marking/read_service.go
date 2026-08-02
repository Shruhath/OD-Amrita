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

// ReadService is the coordinator marking surface's read façade.
//
// It exists so the four read handlers depend on ONE thing, and so every one of
// them passes through the scope seam on the way in. A handler that talked to the
// event reader or the student service directly could serve a scope the mark path
// would refuse, which is precisely the disagreement this story exists to prevent.
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
	ServerTime time.Time
	Slot       *SlotWindow
	Counters   Counters
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

func (s *ReadService) SearchStudents(ctx context.Context, actor Actor, query string, limit int) ([]Student, error) {
	if err := assertCoordinator(actor); err != nil {
		return nil, AsDomainError(err)
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
	}
	if raw.Slot != nil {
		status.Slot = &SlotWindow{Slot: raw.Slot.Slot, StartedAt: raw.Slot.Start, EndsAt: raw.Slot.End}
	}
	return status, nil
}
