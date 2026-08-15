// Package marking holds the ONE place that answers "may this actor mark here".
//
// The live marking surface has four read paths and one write path, and
// every one of them must agree about scope. NFR11 says no scope may be reachable
// by URL manipulation, so enforcing it in the read handlers alone would be
// worthless: the client picks the event_id and category_id, and could simply POST
// to /api/v1/mark with ids the reads refused. The policy therefore lives here,
// behind exactly two entry points, and is called from every read AND from the
// mark service before any write.
//
// POLICY IN FORCE: coordinators may mark active events they are assigned to,
// narrowed by their category assignments. Admins may mark any active event and
// category. Browser state is never authorization.
package marking

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// ErrNotCoordinator is the refusal every non-live-marker role gets. The name
	// is kept for compatibility with the existing error mapper; the policy now
	// admits coordinators and admins.
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
	roleAdmin       = "admin"
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
	// AssignedActiveEvents returns active coordinator-assigned events only.
	AssignedActiveEvents(ctx context.Context, userID string) ([]Event, error)
	// ActiveEventsForAdminMarking returns active events an admin may live-mark.
	ActiveEventsForAdminMarking(ctx context.Context) ([]Event, error)
	// EventByID returns ErrEventNotFound for an unknown id.
	EventByID(context.Context, string) (Event, error)
	// CoordinatorAssignedToEvent reports whether userID is assigned to eventID.
	CoordinatorAssignedToEvent(ctx context.Context, userID, eventID string) (bool, error)
	// CategoryForEvent returns ErrCategoryNotFound unless the category belongs to
	// the event.
	CategoryForEvent(ctx context.Context, eventID, categoryID string) error
}

type categoryScopedReader interface {
	CoordinatorAssignedToEventCategory(ctx context.Context, userID, eventID, categoryID string) (bool, error)
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
	if err := assertLiveMarker(actor); err != nil {
		return nil, err
	}
	var (
		events []Event
		err    error
	)
	if actor.Role == roleAdmin {
		events, err = p.reader.ActiveEventsForAdminMarking(ctx)
	} else {
		events, err = p.reader.AssignedActiveEvents(ctx, strings.TrimSpace(actor.UserID))
	}
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
	if err := p.reader.CategoryForEvent(ctx, strings.TrimSpace(eventID), strings.TrimSpace(categoryID)); err != nil {
		return err
	}
	if actor.Role != roleAdmin {
		scoped, ok := p.reader.(categoryScopedReader)
		if !ok {
			return nil
		}
		assigned, err := scoped.CoordinatorAssignedToEventCategory(ctx, strings.TrimSpace(actor.UserID), strings.TrimSpace(eventID), strings.TrimSpace(categoryID))
		if err != nil {
			return err
		}
		if !assigned {
			return ErrCategoryNotFound
		}
	}
	return nil
}

// assertEventScope is the role-and-event half of the policy. It is unexported —
// the seam's surface stays exactly two functions — and exists so the category
// read, which has no category id to check yet, asks the SAME policy rather than
// re-deriving half of it somewhere else.
func (p *Policy) assertEventScope(ctx context.Context, actor Actor, eventID string) error {
	if err := assertLiveMarker(actor); err != nil {
		return err
	}
	event, err := p.reader.EventByID(ctx, strings.TrimSpace(eventID))
	if err != nil {
		return err
	}
	switch event.Status {
	case statusActive:
		if actor.Role == roleAdmin {
			return nil
		}
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

// assertLiveMarker is intentionally exact-match: role strings come from the
// users table, and a case-insensitive compare here would silently admit a row
// written by some future path that did not canonicalise.
func assertLiveMarker(actor Actor) error {
	if actor.Role != roleCoordinator && actor.Role != roleAdmin {
		return ErrNotCoordinator
	}
	return nil
}
