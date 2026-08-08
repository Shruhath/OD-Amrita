// Package marking holds the ONE place that answers "may this actor mark here".
//
// The coordinator marking surface has four read paths and one write path, and
// every one of them must agree about scope. NFR11 says no scope may be reachable
// by URL manipulation, so enforcing it in the read handlers alone would be
// worthless: the client picks the event_id and category_id, and could simply POST
// to /api/v1/mark with ids the reads refused. The policy therefore lives here,
// behind exactly two entry points, and is called from every read AND from the
// mark service before any write.
//
// POLICY IN FORCE: the actor's role is `coordinator`, the event exists and is
// `active`, an event-level assignment exists for actor.UserID, and the category
// belongs to that event. The assignment check is server-side; browser state is
// never authorization.
package marking

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// ErrNotCoordinator is the refusal every non-coordinator role gets. Teacher
	// and admin marking is Story 0.9: it needs the explicit slot picker that
	// Story 0.4 requires for those roles and that this screen deliberately does
	// not have. Admitting an admin "because admins can do everything" would put
	// them on a screen whose every mark the mark service then rejects.
	ErrNotCoordinator = errors.New("marking: actor is not a coordinator")

	ErrEventNotFound    = errors.New("marking: event not found")
	ErrEventArchived    = errors.New("marking: event is archived")
	ErrEventNotAssigned = errors.New("marking: event not assigned")
	ErrCategoryNotFound = errors.New("marking: category not found")
)

const (
	statusActive    = "active"
	statusArchived  = "archived"
	roleCoordinator = "coordinator"
)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type Branch struct {
	ID   string
	Code string
	Name string
}

// Event is the marking surface's view of an event: enough to render the selector
// and enough to decide scope, and nothing that only the admin surface needs.
type Event struct {
	EventID string
	Name    string
	// EventDate is the FIRST day of the range and EndDate the last; they are
	// equal for a one-day event.
	EventDate  time.Time
	EndDate    time.Time
	Status     string
	Branches   []Branch
	CreatedAt  time.Time
	ArchivedAt *time.Time
}

// Reader is the data the policy needs. It is deliberately three narrow questions
// rather than a general event repository, so a future policy change cannot widen
// what the seam can see without saying so here.
type Reader interface {
	// AssignedActiveEvents returns active assigned events only.
	AssignedActiveEvents(ctx context.Context, userID string) ([]Event, error)
	// EventByID returns ErrEventNotFound for an unknown id.
	EventByID(context.Context, string) (Event, error)
	// CoordinatorAssignedToEvent reports whether userID is assigned to eventID.
	CoordinatorAssignedToEvent(ctx context.Context, userID, eventID string) (bool, error)
	// CategoryForEvent returns ErrCategoryNotFound unless the category belongs to
	// the event.
	CategoryForEvent(ctx context.Context, eventID, categoryID string) error
}

type Policy struct {
	reader Reader
}

func NewPolicy(reader Reader) *Policy {
	return &Policy{reader: reader}
}

// VisibleEvents lists the events the actor may mark for.
//
// It returns ACTIVE events only. The marking screen builds its selector straight
// from this list, so offering an event that AssertCanMark would refuse is a
// screen that invites a guaranteed error on the coordinator's first tap.
func (p *Policy) VisibleEvents(ctx context.Context, actor Actor) ([]Event, error) {
	if err := assertCoordinator(actor); err != nil {
		return nil, err
	}
	events, err := p.reader.AssignedActiveEvents(ctx, strings.TrimSpace(actor.UserID))
	if err != nil {
		return nil, err
	}
	// Filtered again here rather than trusted from the reader: this list is the
	// selector, and the two functions must not be able to disagree.
	visible := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Status == statusActive {
			visible = append(visible, event)
		}
	}
	return visible, nil
}

// AssertCanMark is the gate in front of every scoped read and, critically, in
// front of the mark write itself.
func (p *Policy) AssertCanMark(ctx context.Context, actor Actor, eventID, categoryID string) error {
	if err := p.assertEventScope(ctx, actor, eventID); err != nil {
		return err
	}
	return p.reader.CategoryForEvent(ctx, strings.TrimSpace(eventID), strings.TrimSpace(categoryID))
}

// assertEventScope is the role-and-event half of the policy. It is unexported —
// the seam's surface stays exactly two functions — and exists so the category
// read, which has no category id to check yet, asks the SAME policy rather than
// re-deriving half of it somewhere else.
func (p *Policy) assertEventScope(ctx context.Context, actor Actor, eventID string) error {
	if err := assertCoordinator(actor); err != nil {
		return err
	}
	event, err := p.reader.EventByID(ctx, strings.TrimSpace(eventID))
	if err != nil {
		return err
	}
	switch event.Status {
	case statusActive:
		assigned, err := p.reader.CoordinatorAssignedToEvent(ctx, strings.TrimSpace(actor.UserID), strings.TrimSpace(eventID))
		if err != nil {
			return err
		}
		if !assigned {
			return ErrEventNotAssigned
		}
		return nil
	case statusArchived:
		return ErrEventArchived
	default:
		// An event in no recognised lifecycle state is not markable, and saying
		// "archived" about it would be a lie.
		return ErrEventNotFound
	}
}

// assertCoordinator is intentionally exact-match: role strings come from the
// users table, and a case-insensitive compare here would silently admit a row
// written by some future path that did not canonicalise.
func assertCoordinator(actor Actor) error {
	if actor.Role != roleCoordinator {
		return ErrNotCoordinator
	}
	return nil
}
