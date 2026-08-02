package marking

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

// maxMarkableEvents bounds what the coordinator selector will ever offer.
//
// The seam's VisibleEvents answers a policy question, not a pagination question,
// so it takes no page arguments — but an unbounded read is still an unbounded
// read. One event runs at a time in practice (the MVP exists for a single day),
// so a hundred active events is already far past anything real. The bound is
// stated here rather than left implicit in a query.
const maxMarkableEvents = 100

// EventReaderAdapter serves the policy from service/event's existing reader.
//
// No new SQL: ListEvents already filters by lifecycle and orders deterministically,
// GetEvent already reports a missing event, and ListCategoriesByEvent already
// answers ownership. Forking a second event reader would create exactly the kind
// of second source of truth this story is trying to avoid.
type EventReaderAdapter struct {
	events svcevent.Reader
}

func NewEventReader(events svcevent.Reader) *EventReaderAdapter {
	return &EventReaderAdapter{events: events}
}

func (a *EventReaderAdapter) ActiveEvents(ctx context.Context) ([]Event, error) {
	rows, _, err := a.events.ListEvents(ctx, svcevent.ListInput{
		Status:   svcevent.StatusActive,
		Page:     1,
		PageSize: maxMarkableEvents,
	})
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, convErr := fromServiceEvent(row)
		if convErr != nil {
			return nil, convErr
		}
		events = append(events, event)
	}
	return events, nil
}

func (a *EventReaderAdapter) EventByID(ctx context.Context, eventID string) (Event, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		// A malformed id is indistinguishable from an id that does not exist, and
		// the caller must not be able to tell the two apart.
		return Event{}, ErrEventNotFound
	}
	row, err := a.events.GetEvent(ctx, parsed)
	if errors.Is(err, svcevent.ErrEventNotFound) {
		return Event{}, ErrEventNotFound
	}
	if err != nil {
		return Event{}, err
	}
	return fromServiceEvent(row)
}

func (a *EventReaderAdapter) CategoryForEvent(ctx context.Context, eventID, categoryID string) error {
	parsedEvent, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return ErrEventNotFound
	}
	parsedCategory, err := uuid.Parse(strings.TrimSpace(categoryID))
	if err != nil {
		return ErrCategoryNotFound
	}
	categories, err := a.events.ListCategories(ctx, parsedEvent)
	if err != nil {
		return err
	}
	for _, category := range categories {
		if strings.EqualFold(category.ID, parsedCategory.String()) {
			return nil
		}
	}
	return ErrCategoryNotFound
}

func fromServiceEvent(row svcevent.Event) (Event, error) {
	// service/event carries event_date as the wire's `date` string; the marking
	// surface needs a time so ActiveSlot can compare calendar days.
	eventDate, err := time.ParseInLocation("2006-01-02", row.EventDate, time.UTC)
	if err != nil {
		return Event{}, err
	}
	branches := make([]Branch, 0, len(row.Branches))
	for _, branch := range row.Branches {
		branches = append(branches, Branch{ID: branch.ID, Code: branch.Code, Name: branch.Name})
	}
	return Event{
		EventID:    row.ID,
		Name:       row.Name,
		EventDate:  eventDate,
		Status:     string(row.Status),
		Branches:   branches,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}, nil
}
