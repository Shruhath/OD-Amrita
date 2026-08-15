package marking

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

// EventReaderAdapter serves the policy from service/event's existing reader.
type EventReaderAdapter struct {
	events svcevent.Reader
}

func NewEventReader(events svcevent.Reader) *EventReaderAdapter {
	return &EventReaderAdapter{events: events}
}

func (a *EventReaderAdapter) AssignedActiveEvents(ctx context.Context, userID string) ([]Event, error) {
	parsedUser, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return nil, ErrNotCoordinator
	}
	rows, err := a.events.ListAssignedActiveEvents(ctx, parsedUser)
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

func (a *EventReaderAdapter) ActiveEventsForAdminMarking(ctx context.Context) ([]Event, error) {
	rows, err := a.events.ListActiveEventsForMarking(ctx)
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

func (a *EventReaderAdapter) CoordinatorAssignedToEvent(ctx context.Context, userID, eventID string) (bool, error) {
	parsedUser, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return false, ErrNotCoordinator
	}
	parsedEvent, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return false, ErrEventNotFound
	}
	return a.events.CoordinatorAssignedToEvent(ctx, parsedUser, parsedEvent)
}

type coordinatorCategoryAssigner interface {
	CoordinatorAssignedToEventCategory(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error)
}

func (a *EventReaderAdapter) CoordinatorAssignedToEventCategory(ctx context.Context, userID, eventID, categoryID string) (bool, error) {
	scoped, ok := a.events.(coordinatorCategoryAssigner)
	if !ok {
		return false, nil
	}
	parsedUser, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return false, ErrNotCoordinator
	}
	parsedEvent, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return false, ErrEventNotFound
	}
	parsedCategory, err := uuid.Parse(strings.TrimSpace(categoryID))
	if err != nil {
		return false, ErrCategoryNotFound
	}
	return scoped.CoordinatorAssignedToEventCategory(ctx, parsedUser, parsedEvent, parsedCategory)
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
	endDate, err := time.ParseInLocation("2006-01-02", row.EndDate, time.UTC)
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
		EndDate:    endDate,
		Status:     string(row.Status),
		Branches:   branches,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}, nil
}
