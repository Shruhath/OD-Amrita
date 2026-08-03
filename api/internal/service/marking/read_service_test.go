// Tests that the READ façade cannot serve a scope the mark write would refuse.
//
// The mark-path half of this guarantee is covered in service/od_record
// (TestMark_CallsScopeGuardBeforeAnyWrite). This file is the read half, and it
// did not exist: scope_test.go tests Policy in isolation, while the handler and
// route tests both inject a fake read service and so bypass ReadService
// entirely. Deleting the policy call from ReadService.Categories or
// ReadService.MarkStatus therefore failed no Go test at all — the seam could be
// removed from the reads and every suite stayed green.
//
// The property each test asserts is ORDER, not just outcome: the policy must be
// consulted BEFORE the downstream dependency is touched. A service that reads
// first and authorizes afterwards has already done the work an unauthorized
// caller asked for, and leaks its existence through timing and through errors.
package marking

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	odrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// recordingEvents is the event repository behind Categories. It records whether
// it was reached, which is the whole point: it must NOT be, for a refused actor.
type recordingEvents struct {
	listCategoriesCalls int
	categories          []svcevent.Category
}

func (r *recordingEvents) ListCategories(_ context.Context, eventID uuid.UUID) ([]svcevent.Category, error) {
	r.listCategoriesCalls++
	return r.categories, nil
}

// The rest of svcevent.Reader is unused by ReadService. Reaching any of these
// from the marking surface is itself the bug, so they fail loudly.
func (r *recordingEvents) ListBranches(context.Context) ([]svcevent.Branch, error) {
	panic("marking read service must not list branches")
}

func (r *recordingEvents) GetEvent(context.Context, uuid.UUID) (svcevent.Event, error) {
	panic("marking read service must not read events directly — that is the policy's job")
}

func (r *recordingEvents) ListEvents(context.Context, svcevent.ListInput) ([]svcevent.Event, int64, error) {
	panic("marking read service must not list events directly — VisibleEvents goes through the policy")
}

func (r *recordingEvents) EventStatus(context.Context, uuid.UUID) (svcevent.Status, error) {
	panic("marking read service must not read event status directly")
}

func (r *recordingEvents) ListCoordinatorAssignments(context.Context, uuid.UUID) ([]svcevent.CoordinatorAssignment, error) {
	panic("marking read service must not list coordinator assignments directly")
}

func (r *recordingEvents) GetCoordinatorCandidateByEmail(context.Context, string) (svcevent.CoordinatorCandidate, error) {
	panic("marking read service must not read coordinator candidates")
}

func (r *recordingEvents) CoordinatorAssignmentExists(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	panic("marking read service must not check coordinator assignments directly")
}

func (r *recordingEvents) ListAssignedActiveEvents(context.Context, uuid.UUID) ([]svcevent.Event, error) {
	panic("marking read service must not list assigned events directly")
}

func (r *recordingEvents) CoordinatorAssignedToEvent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	panic("marking read service must not check event assignment directly")
}

type recordingSearcher struct{ calls int }

func (r *recordingSearcher) Search(context.Context, svcstudent.Actor, svcstudent.SearchInput) ([]svcstudent.SearchResult, error) {
	r.calls++
	return nil, nil
}

type recordingStatus struct{ calls int }

func (r *recordingStatus) MarkStatus(context.Context, odrecord.Actor, string, string) (odrecord.Status, error) {
	r.calls++
	return odrecord.Status{ServerTime: time.Now()}, nil
}

func newReadFixture(t *testing.T) (*ReadService, *fakeReader, *recordingEvents, *recordingSearcher, *recordingStatus) {
	t.Helper()
	reader := &fakeReader{
		activeEvents: []Event{{EventID: activeEventID, Status: "active"}},
		events:       map[string]Event{activeEventID: {EventID: activeEventID, Status: "active"}},
		categories:   map[string]string{categoryID: activeEventID},
		assignments:  map[string]bool{"u-coord|" + activeEventID: true},
	}
	events := &recordingEvents{categories: []svcevent.Category{{
		ID: categoryID, EventID: activeEventID, Name: "Procession", CreatedAt: time.Now(),
	}}}
	searcher := &recordingSearcher{}
	status := &recordingStatus{}
	return NewReadService(NewPolicy(reader), events, searcher, status), reader, events, searcher, status
}

// 0.6-READSEAM-001a — a non-coordinator is refused by every scoped read, and no
// downstream dependency is touched.
func TestReadService_RefusesNonCoordinatorsBeforeReadingAnything(t *testing.T) {
	for _, role := range []string{"admin", "teacher", "student", "", "COORDINATOR"} {
		t.Run(role, func(t *testing.T) {
			svc, reader, events, searcher, status := newReadFixture(t)
			actor := Actor{UserID: "u", Role: role}

			if _, err := svc.VisibleEvents(context.Background(), actor); err == nil {
				t.Errorf("[P0] VisibleEvents admitted role %q — the marking surface is coordinator-only (C2)", role)
			}
			if _, err := svc.Categories(context.Background(), actor, activeEventID); err == nil {
				t.Errorf("[P0] Categories admitted role %q", role)
			}
			if _, err := svc.SearchStudents(context.Background(), actor, "CB.SC", 10); err == nil {
				t.Errorf("[P0] SearchStudents admitted role %q — the roster is 1,534 real people", role)
			}
			if _, err := svc.MarkStatus(context.Background(), actor, activeEventID, categoryID); err == nil {
				t.Errorf("[P0] MarkStatus admitted role %q", role)
			}

			if events.listCategoriesCalls != 0 {
				t.Errorf("[P0] the category repository was reached %d times for a refused actor — "+
					"authorization must precede the read, not follow it", events.listCategoriesCalls)
			}
			if searcher.calls != 0 {
				t.Errorf("[P0] the roster search ran %d times for a refused actor", searcher.calls)
			}
			if status.calls != 0 {
				t.Errorf("[P0] the counters query ran %d times for a refused actor", status.calls)
			}
			if reader.activeEventsCalls != 0 {
				t.Errorf("[P0] the event reader was reached %d times for a refused actor", reader.activeEventsCalls)
			}
		})
	}
}

// 0.6-READSEAM-001b — Categories consults the policy before the repository.
//
// This is the test that fails if someone deletes the assertEventScope call to
// "simplify" the method. It pins the ORDER, so returning the right error after
// doing the read is still a failure.
func TestReadService_CategoriesConsultsThePolicyBeforeTheRepository(t *testing.T) {
	svc, reader, events, _, _ := newReadFixture(t)

	if _, err := svc.Categories(context.Background(), coordinator(), activeEventID); err != nil {
		t.Fatalf("Categories = %v, want nil for an active event", err)
	}
	if len(reader.eventByIDCalls) == 0 {
		t.Fatal("[P0] Categories never asked the policy about the event — the scope seam is bypassed (C4/NFR11)")
	}
	if events.listCategoriesCalls != 1 {
		t.Fatalf("[P0] ListCategories called %d times, want 1", events.listCategoriesCalls)
	}

	// An unknown event must be refused by the policy, and the repository must
	// never be asked about it.
	events.listCategoriesCalls = 0
	if _, err := svc.Categories(context.Background(), coordinator(), uuid.NewString()); err == nil {
		t.Error("[P0] Categories served an unknown event")
	}
	if events.listCategoriesCalls != 0 {
		t.Error("[P0] the repository was read for an event the policy refused")
	}
}

// 0.6-READSEAM-001c — MarkStatus runs the FULL policy, category ownership
// included, before counting anything.
//
// Without the category half, a coordinator could read another category's live
// counters by editing the query string — NFR11's "no scope reachable by URL
// manipulation", on the one read that exposes running event totals.
func TestReadService_MarkStatusAssertsCategoryOwnershipBeforeCounting(t *testing.T) {
	svc, reader, _, _, status := newReadFixture(t)

	foreignCategory := uuid.NewString() // exists, but belongs to no event here
	if _, err := svc.MarkStatus(context.Background(), coordinator(), activeEventID, foreignCategory); err == nil {
		t.Error("[P0] MarkStatus served a category that does not belong to the event")
	}
	if status.calls != 0 {
		t.Errorf("[P0] the counters query ran %d times for a category the policy refused", status.calls)
	}
	if len(reader.categoryCalls) == 0 {
		t.Fatal("[P0] MarkStatus never checked category ownership — AssertCanMark's second half is bypassed")
	}

	if _, err := svc.MarkStatus(context.Background(), coordinator(), activeEventID, categoryID); err != nil {
		t.Fatalf("MarkStatus = %v, want nil for an owned category", err)
	}
	if status.calls != 1 {
		t.Errorf("[P0] counters ran %d times for an admitted actor, want 1", status.calls)
	}
}

// 0.6-READSEAM-001d — an archived event is refused on the read side too.
//
// The write path already refuses it. If a read still served it, the selector
// would offer a scope whose every tap the server rejects.
func TestReadService_RefusesArchivedEventsOnEveryScopedRead(t *testing.T) {
	svc, reader, events, _, status := newReadFixture(t)
	archived := uuid.NewString()
	reader.events[archived] = Event{EventID: archived, Status: "archived"}
	reader.categories[categoryID] = archived

	if _, err := svc.Categories(context.Background(), coordinator(), archived); err == nil {
		t.Error("[P0] Categories served an archived event")
	}
	if _, err := svc.MarkStatus(context.Background(), coordinator(), archived, categoryID); err == nil {
		t.Error("[P0] MarkStatus served an archived event")
	}
	if events.listCategoriesCalls != 0 || status.calls != 0 {
		t.Error("[P0] an archived event's data was read before the policy refused it")
	}
}
