// Tests for the scope seam — the one place that answers "may this actor mark here".
//
// WHY A SEAM AT ALL. The epic wants "the coordinator only sees the event they
// are assigned to", but the assignment table is Story 0.7. 0.6 therefore builds
// the ONE function that answers "may this actor mark here", implements it with a
// role+active-event policy, and calls it from every read AND the mark write.
// 0.7 then replaces only the policy body. Because the policy lives in one place,
// 0.7 is a small diff — which is the entire point of the split.
//
// INTERIM EXPOSURE THIS PINS: until 0.7 lands, ANY user with role=coordinator
// can mark for ANY active event. That is a known, accepted gap. What must never
// be true is that a NON-coordinator can, or that an ARCHIVED event is markable.
package marking

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// fakeReader stands in for the SQLC-backed reader. It records what the policy
// asked for, so a policy that "allows" without actually checking the event is
// caught rather than passing by luck.
type fakeReader struct {
	activeEvents []Event
	events       map[string]Event
	categories   map[string]string // categoryID -> owning eventID
	assignments  map[string]bool

	activeEventsCalls int
	eventByIDCalls    []string
	assignmentCalls   [][2]string
	categoryCalls     [][2]string
	err               error
}

func (f *fakeReader) AssignedActiveEvents(_ context.Context, userID string) ([]Event, error) {
	f.activeEventsCalls++
	if f.err != nil {
		return nil, f.err
	}
	if userID == "" {
		return nil, ErrNotCoordinator
	}
	return f.activeEvents, nil
}

func (f *fakeReader) EventByID(_ context.Context, eventID string) (Event, error) {
	f.eventByIDCalls = append(f.eventByIDCalls, eventID)
	if f.err != nil {
		return Event{}, f.err
	}
	event, ok := f.events[eventID]
	if !ok {
		return Event{}, ErrEventNotFound
	}
	return event, nil
}

func (f *fakeReader) CoordinatorAssignedToEvent(_ context.Context, userID, eventID string) (bool, error) {
	f.assignmentCalls = append(f.assignmentCalls, [2]string{userID, eventID})
	if f.err != nil {
		return false, f.err
	}
	return f.assignments[userID+"|"+eventID], nil
}

func (f *fakeReader) CategoryForEvent(_ context.Context, eventID, categoryID string) error {
	f.categoryCalls = append(f.categoryCalls, [2]string{eventID, categoryID})
	if f.err != nil {
		return f.err
	}
	owner, ok := f.categories[categoryID]
	if !ok || owner != eventID {
		return ErrCategoryNotFound
	}
	return nil
}

const (
	activeEventID   = "11111111-1111-4111-8111-111111111111"
	archivedEventID = "22222222-2222-4222-8222-222222222222"
	categoryID      = "33333333-3333-4333-8333-333333333333"
	foreignCatID    = "44444444-4444-4444-8444-444444444444"
)

func newFixture() *fakeReader {
	return &fakeReader{
		activeEvents: []Event{{EventID: activeEventID, Status: "active", EventDate: time.Now()}},
		events: map[string]Event{
			activeEventID:   {EventID: activeEventID, Status: "active", EventDate: time.Now()},
			archivedEventID: {EventID: archivedEventID, Status: "archived", EventDate: time.Now()},
		},
		categories: map[string]string{
			categoryID:   activeEventID,
			foreignCatID: archivedEventID,
		},
		assignments: map[string]bool{"u-coord|" + activeEventID: true},
	}
}

func coordinator() Actor { return Actor{UserID: "u-coord", Role: "coordinator"} }

// 0.6-SCOPE-001a — the policy admits a coordinator for an active event whose
// category belongs to it, and proves it actually consulted the event and the
// category rather than short-circuiting on the role.
func TestAssertCanMark_AllowsCoordinatorForActiveEventAndOwnedCategory(t *testing.T) {
	reader := newFixture()
	policy := NewPolicy(reader)

	if err := policy.AssertCanMark(context.Background(), coordinator(), activeEventID, categoryID); err != nil {
		t.Fatalf("AssertCanMark = %v, want nil for a coordinator on an active event", err)
	}
	if len(reader.eventByIDCalls) == 0 {
		t.Error("[P0] policy allowed the mark without loading the event — role alone is not the policy")
	}
	if len(reader.categoryCalls) == 0 {
		t.Error("[P0] policy allowed the mark without checking the category belongs to the event")
	}
}

// 0.6-SCOPE-001b — every non-coordinator role is refused. Teacher and admin
// marking is Story 0.9 and needs an explicit slot picker this screen does not
// have, so admitting an admin "because admins can do everything" is a defect.
func TestAssertCanMark_RefusesEveryNonCoordinatorRole(t *testing.T) {
	for _, role := range []string{"student", "teacher", "admin", "", "COORDINATOR"} {
		t.Run(role, func(t *testing.T) {
			reader := newFixture()
			policy := NewPolicy(reader)

			err := policy.AssertCanMark(context.Background(), Actor{UserID: "u", Role: role}, activeEventID, categoryID)
			if err == nil {
				t.Fatalf("[P0] AssertCanMark admitted role %q — 0.6 is coordinator-only (C2)", role)
			}
			if !errors.Is(err, ErrNotCoordinator) {
				t.Errorf("[P0] error = %v, want ErrNotCoordinator so the handler can map it to RBAC_FORBIDDEN", err)
			}
		})
	}
}

// 0.6-SCOPE-001c — an archived event is closed to marking even for a
// coordinator, and a category belonging to a different event is refused. Both
// are URL-manipulation paths (NFR11): the client picks these ids.
func TestAssertCanMark_RefusesArchivedEventAndForeignCategory(t *testing.T) {
	t.Run("archived event", func(t *testing.T) {
		policy := NewPolicy(newFixture())
		err := policy.AssertCanMark(context.Background(), coordinator(), archivedEventID, foreignCatID)
		if !errors.Is(err, ErrEventArchived) {
			t.Fatalf("[P0] error = %v, want ErrEventArchived", err)
		}
	})

	t.Run("category owned by another event", func(t *testing.T) {
		policy := NewPolicy(newFixture())
		err := policy.AssertCanMark(context.Background(), coordinator(), activeEventID, foreignCatID)
		if !errors.Is(err, ErrCategoryNotFound) {
			t.Fatalf("[P0] error = %v, want ErrCategoryNotFound — a category from another event must not be markable", err)
		}
	})

	t.Run("unknown event", func(t *testing.T) {
		policy := NewPolicy(newFixture())
		err := policy.AssertCanMark(context.Background(), coordinator(), "99999999-9999-4999-8999-999999999999", categoryID)
		if !errors.Is(err, ErrEventNotFound) {
			t.Fatalf("[P0] error = %v, want ErrEventNotFound", err)
		}
	})
}

// 0.6-SCOPE-001d — VisibleEvents returns active events for a coordinator and
// NOTHING for anyone else. It must not return archived events: the selector on
// the marking screen is built straight from this list, and an event the reader
// offers but AssertCanMark refuses is a screen that invites a guaranteed error.
func TestVisibleEvents_ActiveOnlyForCoordinatorsAndEmptyForOthers(t *testing.T) {
	t.Run("coordinator sees active events", func(t *testing.T) {
		reader := newFixture()
		policy := NewPolicy(reader)

		events, err := policy.VisibleEvents(context.Background(), coordinator())
		if err != nil {
			t.Fatalf("VisibleEvents = %v, want nil error", err)
		}
		if len(events) != 1 || events[0].EventID != activeEventID {
			t.Fatalf("[P0] VisibleEvents = %+v, want exactly the one active event", events)
		}
		for _, event := range events {
			if event.Status != "active" {
				t.Errorf("[P0] VisibleEvents returned a %q event — the selector must never offer an unmarkable event", event.Status)
			}
		}
	})

	t.Run("non-coordinator sees nothing", func(t *testing.T) {
		for _, role := range []string{"student", "teacher", "admin"} {
			reader := newFixture()
			policy := NewPolicy(reader)

			events, err := policy.VisibleEvents(context.Background(), Actor{UserID: "u", Role: role})
			if err == nil && len(events) != 0 {
				t.Errorf("[P0] role %q received %d visible events — 0.6 is coordinator-only", role, len(events))
			}
			if err != nil && !errors.Is(err, ErrNotCoordinator) {
				t.Errorf("[P0] role %q error = %v, want ErrNotCoordinator or an empty list", role, err)
			}
		}
	})
}

// 0.6-SCOPE-001e — the seam's surface stays exactly two functions. This is the
// test that fails if a later story starts scattering authorization: it names
// Story 0.7 as the ONLY story allowed to change the policy body, and it does so
// by pinning the call signatures the rest of the system depends on.
func TestScopeSeam_ExportsExactlyTheTwoAgreedEntryPoints(t *testing.T) {
	// Signatures first: these fail to COMPILE if either entry point changes shape.
	var (
		_ func(context.Context, Actor) ([]Event, error)      = (*Policy)(nil).VisibleEvents
		_ func(context.Context, Actor, string, string) error = (*Policy)(nil).AssertCanMark
	)

	// Then the count, which is the half the earlier version was missing. It had
	// only the two declarations above and no assertion at all, so a THIRD
	// exported entry point — the one thing the test's name promises to catch —
	// would not have failed it.
	//
	// The assignment story replaces the BODY of these two methods with a lookup
	// against the coordinator-assignment table. It must not add a third entry
	// point, and it must not move any part of the policy into a handler. If this
	// test fails, that is the decision being made — make it deliberately.
	want := map[string]bool{"VisibleEvents": true, "AssertCanMark": true}

	shape := reflect.TypeOf(&Policy{})
	for i := range shape.NumMethod() {
		name := shape.Method(i).Name
		if !want[name] {
			t.Errorf("[P0] Policy exports a third entry point %q — the seam is exactly VisibleEvents and AssertCanMark. "+
				"Scattering authorization across more entry points is the failure this seam exists to prevent (C4/NFR11).", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("[P0] Policy no longer exports %q — every marking read and the mark write depend on it", name)
	}
}
