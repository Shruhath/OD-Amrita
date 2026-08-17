package markingcache

// What a cache is allowed to change here is NOTHING except how often the
// database is asked. Every test below is either "the cached reader and the plain
// reader give the identical answer" or "the scope policy still refuses what it
// refused before".

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

const (
	eventID    = "11111111-1111-4111-8111-111111111111"
	categoryID = "22222222-2222-4222-8222-222222222222"
	userID     = "33333333-3333-4333-8333-333333333333"
	ttl        = time.Minute
)

// fakeReader counts every call so the collapse can be asserted, and lets each
// answer be set independently so the policy's branches can be reached.
type fakeReader struct {
	event       marking.Event
	eventErr    error
	assigned    bool
	assignedErr error
	categoryErr error
	catAssigned bool
	catErr      error
	list        []marking.Event
	listErr     error

	eventCalls, assignCalls, categoryCalls, catAssignCalls, listCalls, adminCalls int
}

func (f *fakeReader) EventByID(context.Context, string) (marking.Event, error) {
	f.eventCalls++
	return f.event, f.eventErr
}

func (f *fakeReader) CoordinatorAssignedToEvent(context.Context, string, string) (bool, error) {
	f.assignCalls++
	return f.assigned, f.assignedErr
}

func (f *fakeReader) CategoryForEvent(context.Context, string, string) error {
	f.categoryCalls++
	return f.categoryErr
}

func (f *fakeReader) CoordinatorAssignedToEventCategory(context.Context, string, string, string) (bool, error) {
	f.catAssignCalls++
	return f.catAssigned, f.catErr
}

func (f *fakeReader) AssignedActiveEvents(context.Context, string) ([]marking.Event, error) {
	f.listCalls++
	return f.list, f.listErr
}

func (f *fakeReader) ActiveEventsForAdminMarking(context.Context) ([]marking.Event, error) {
	f.adminCalls++
	return f.list, f.listErr
}

func activeEvent() marking.Event {
	day := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	return marking.Event{EventID: eventID, Name: "Onam", EventDate: day, EndDate: day, Status: "active"}
}

func coordinator() marking.Actor {
	return marking.Actor{UserID: userID, Email: "c@amrita.edu", Role: "coordinator"}
}

// ---------------------------------------------------------------------------
// The trap. This is the test that earns the package.
// ---------------------------------------------------------------------------

// marking.Policy finds category-level scope through a type assertion, and ALLOWS
// when the assertion fails. A decorator that dropped
// CoordinatorAssignedToEventCategory would still satisfy marking.Reader, still
// compile, and silently let every coordinator mark in categories they were never
// assigned to.
//
// So this asserts the refusal THROUGH A REAL POLICY rather than asserting the
// method exists: it is the only form of the test that fails if the assertion
// inside scope.go stops finding what it is looking for.
func TestCachedReader_CategoryScopeSurvivesTheTypeAssertion(t *testing.T) {
	inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: false}
	policy := marking.NewPolicy(NewReader(inner, ttl))

	err := policy.AssertCanMark(context.Background(), coordinator(), eventID, categoryID)

	if !errors.Is(err, marking.ErrCategoryNotFound) {
		t.Fatalf("a coordinator unassigned to the category was ADMITTED through the cached reader: err = %v, want %v"+
			"\nthe decorator has stopped satisfying marking's categoryScopedReader, so the policy is failing OPEN",
			err, marking.ErrCategoryNotFound)
	}
	if inner.catAssignCalls == 0 {
		t.Error("the category assignment was never consulted at all")
	}
}

// The same policy, same reader, when the coordinator IS assigned.
func TestCachedReader_AssignedCoordinatorIsStillAdmitted(t *testing.T) {
	inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: true}
	policy := marking.NewPolicy(NewReader(inner, ttl))

	if err := policy.AssertCanMark(context.Background(), coordinator(), eventID, categoryID); err != nil {
		t.Fatalf("AssertCanMark: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Parity: cached and plain must be indistinguishable except by call count.
// ---------------------------------------------------------------------------

func TestCachedReader_PolicyOutcomesMatchTheUncachedReader(t *testing.T) {
	archived := activeEvent()
	archived.Status = "archived"

	cases := []struct {
		name    string
		reader  fakeReader
		actor   marking.Actor
		wantErr error
	}{
		{
			name:   "assigned coordinator, own category",
			reader: fakeReader{event: activeEvent(), assigned: true, catAssigned: true},
			actor:  coordinator(),
		},
		{
			name:    "event missing",
			reader:  fakeReader{eventErr: marking.ErrEventNotFound},
			actor:   coordinator(),
			wantErr: marking.ErrEventNotFound,
		},
		{
			name:    "event archived",
			reader:  fakeReader{event: archived, assigned: true, catAssigned: true},
			actor:   coordinator(),
			wantErr: marking.ErrEventArchived,
		},
		{
			name:    "coordinator not assigned to the event",
			reader:  fakeReader{event: activeEvent(), assigned: false},
			actor:   coordinator(),
			wantErr: marking.ErrEventNotAssigned,
		},
		{
			name:    "category not in the event",
			reader:  fakeReader{event: activeEvent(), assigned: true, categoryErr: marking.ErrCategoryNotFound},
			actor:   coordinator(),
			wantErr: marking.ErrCategoryNotFound,
		},
		{
			name:    "coordinator not assigned to the category",
			reader:  fakeReader{event: activeEvent(), assigned: true, catAssigned: false},
			actor:   coordinator(),
			wantErr: marking.ErrCategoryNotFound,
		},
		{
			name:    "a teacher is not a live marker",
			reader:  fakeReader{event: activeEvent()},
			actor:   marking.Actor{UserID: userID, Role: "teacher"},
			wantErr: marking.ErrNotCoordinator,
		},
		{
			// An admin skips both assignment checks. The cache must not change
			// that: caching a coordinator's refusal under a key an admin could
			// share would be a very quiet way to lock an administrator out.
			name:   "admin needs no assignment",
			reader: fakeReader{event: activeEvent(), assigned: false, catAssigned: false},
			actor:  marking.Actor{UserID: "admin-1", Role: "admin"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plainInner, cachedInner := tc.reader, tc.reader

			plainErr := marking.NewPolicy(&plainInner).
				AssertCanMark(context.Background(), tc.actor, eventID, categoryID)
			cachedErr := marking.NewPolicy(NewReader(&cachedInner, ttl)).
				AssertCanMark(context.Background(), tc.actor, eventID, categoryID)

			if !errors.Is(plainErr, tc.wantErr) {
				t.Errorf("uncached: err = %v, want %v", plainErr, tc.wantErr)
			}
			if !errors.Is(cachedErr, tc.wantErr) {
				t.Errorf("cached: err = %v, want %v", cachedErr, tc.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The collapse — the reason the package exists.
// ---------------------------------------------------------------------------

func TestCachedReader_RepeatedPollsAskTheDatabaseOnce(t *testing.T) {
	inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: true}
	policy := marking.NewPolicy(NewReader(inner, ttl))

	// Six polls is a minute of the marking screen at its ten-second cadence.
	for i := 0; i < 6; i++ {
		if err := policy.AssertCanMark(context.Background(), coordinator(), eventID, categoryID); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
	}

	if inner.eventCalls != 1 || inner.assignCalls != 1 || inner.categoryCalls != 1 || inner.catAssignCalls != 1 {
		t.Errorf("six polls cost %d event, %d assignment, %d category, %d category-assignment queries; want 1 of each",
			inner.eventCalls, inner.assignCalls, inner.categoryCalls, inner.catAssignCalls)
	}
}

// The event row is the same answer for everyone on the event, so a whole marking
// team's polls must share one read of it — while the PER-COORDINATOR assignment
// answers stay separate, because sharing those would be a scope leak.
func TestCachedReader_SharesTheEventButNotTheAssignments(t *testing.T) {
	inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: true}
	policy := marking.NewPolicy(NewReader(inner, ttl))

	for _, id := range []string{"user-a", "user-b", "user-c"} {
		actor := marking.Actor{UserID: id, Role: "coordinator"}
		if err := policy.AssertCanMark(context.Background(), actor, eventID, categoryID); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	if inner.eventCalls != 1 {
		t.Errorf("three coordinators cost %d event reads, want 1 — the shared key is not collapsing", inner.eventCalls)
	}
	if inner.assignCalls != 3 {
		t.Errorf("three coordinators cost %d assignment reads, want 3 — assignments must NOT share a key", inner.assignCalls)
	}
}

// ---------------------------------------------------------------------------
// What may and may not be held.
// ---------------------------------------------------------------------------

// A transient database failure must not be pinned for the length of the ttl:
// that would turn one bad second into a minute of refusals for everyone sharing
// the key. A DECIDED refusal is different — it is the answer, and it is held.
func TestCachedReader_HoldsDecisionsButNeverFailures(t *testing.T) {
	t.Run("a transient failure is retried", func(t *testing.T) {
		boom := errors.New("connection reset by peer")
		inner := &fakeReader{eventErr: boom}
		reader := NewReader(inner, ttl)

		for i := 0; i < 3; i++ {
			if _, err := reader.EventByID(context.Background(), eventID); !errors.Is(err, boom) {
				t.Fatalf("call %d: err = %v, want %v", i, err, boom)
			}
		}
		if inner.eventCalls != 3 {
			t.Errorf("%d calls reached the database, want 3 — a failure was cached", inner.eventCalls)
		}
	})

	t.Run("a decided refusal is held", func(t *testing.T) {
		inner := &fakeReader{eventErr: marking.ErrEventNotFound}
		reader := NewReader(inner, ttl)

		for i := 0; i < 3; i++ {
			if _, err := reader.EventByID(context.Background(), eventID); !errors.Is(err, marking.ErrEventNotFound) {
				t.Fatalf("call %d: err = %v, want %v", i, err, marking.ErrEventNotFound)
			}
		}
		if inner.eventCalls != 1 {
			t.Errorf("%d calls reached the database, want 1 — a settled answer is not being held", inner.eventCalls)
		}
	})
}

// Clear is what an administrator's own edit reaches for, so it must drop every
// kind of entry — not just the one whose name matches what they changed.
func TestCachedReader_ClearDropsEverything(t *testing.T) {
	inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: true, list: []marking.Event{activeEvent()}}
	reader := NewReader(inner, ttl)
	policy := marking.NewPolicy(reader)

	warm := func() {
		if err := policy.AssertCanMark(context.Background(), coordinator(), eventID, categoryID); err != nil {
			t.Fatalf("AssertCanMark: %v", err)
		}
		if _, err := reader.AssignedActiveEvents(context.Background(), userID); err != nil {
			t.Fatalf("AssignedActiveEvents: %v", err)
		}
		if _, err := reader.ActiveEventsForAdminMarking(context.Background()); err != nil {
			t.Fatalf("ActiveEventsForAdminMarking: %v", err)
		}
	}

	warm()
	reader.Clear()
	warm()

	for name, got := range map[string]int{
		"event":               inner.eventCalls,
		"assignment":          inner.assignCalls,
		"category":            inner.categoryCalls,
		"category assignment": inner.catAssignCalls,
		"assigned events":     inner.listCalls,
		"admin events":        inner.adminCalls,
	} {
		if got != 2 {
			t.Errorf("%s: %d reads after Clear, want 2 — that cache survived the clear", name, got)
		}
	}
}

// Zero is the day-of switch: it must call straight through rather than caching
// forever or caching for no time at all.
func TestCachedReader_ZeroTTLDisablesCaching(t *testing.T) {
	inner := &fakeReader{event: activeEvent(), assigned: true, catAssigned: true}
	policy := marking.NewPolicy(NewReader(inner, 0))

	for i := 0; i < 3; i++ {
		if err := policy.AssertCanMark(context.Background(), coordinator(), eventID, categoryID); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
	}

	if inner.eventCalls != 3 {
		t.Errorf("%d event reads with the cache switched off, want 3", inner.eventCalls)
	}
}

// The selector list is the one read where a stale answer is visible as a missing
// event, so it is worth pinning that it caches per coordinator and not globally.
func TestCachedReader_AssignedEventsAreKeyedPerCoordinator(t *testing.T) {
	inner := &fakeReader{list: []marking.Event{activeEvent()}}
	reader := NewReader(inner, ttl)

	for _, id := range []string{"user-a", "user-a", "user-b"} {
		if _, err := reader.AssignedActiveEvents(context.Background(), id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	if inner.listCalls != 2 {
		t.Errorf("%d reads for two distinct coordinators, want 2", inner.listCalls)
	}
}
