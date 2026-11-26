package main

// marking_scope_cache_test.go — the composition guard for the cached scope seam.
//
// run() builds TWO policies over one policy body: an exact one for the mark
// WRITE and a cached one for the polled READS. The safety of the whole change
// rests on which one reaches the write gate, and that is a single identifier in
// main.go. Swapping it compiles cleanly, passes every package's own tests, and
// silently lets a mark be admitted on a permission that was revoked a minute ago.
//
// This is the same class of risk newAdminGroup and newPublicGroup already have
// guards for in routes_test.go: nothing below cmd/api can catch it, because every
// other test builds its own graph.

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	svcmarking "github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	"github.com/Shruhath/OD-Amrita/api/internal/service/markingcache"
)

// countingScopeReader is the database, counting how often it is asked.
type countingScopeReader struct {
	event svcmarking.Event
	calls int
}

func (c *countingScopeReader) EventByID(context.Context, string) (svcmarking.Event, error) {
	c.calls++
	return c.event, nil
}

func (c *countingScopeReader) CoordinatorAssignedToEvent(context.Context, string, string) (bool, error) {
	return true, nil
}

func (c *countingScopeReader) CategoryForEvent(context.Context, string, string) error { return nil }

func (c *countingScopeReader) CoordinatorAssignedToEventCategory(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func (c *countingScopeReader) AssignedActiveEvents(context.Context, string) ([]svcmarking.Event, error) {
	return nil, nil
}

func (c *countingScopeReader) ActiveEventsForAdminMarking(context.Context) ([]svcmarking.Event, error) {
	return nil, nil
}

func activeScopeEvent() svcmarking.Event {
	day := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	return svcmarking.Event{
		EventID:   "11111111-1111-4111-8111-111111111111",
		EventDate: day, EndDate: day, Status: "active",
	}
}

// The two policies must behave differently, and in the right direction: the read
// policy collapses repeated questions, the write policy asks every time.
func TestMarkingPolicies_WriteAsksEveryTimeAndReadDoesNot(t *testing.T) {
	const (
		eventID    = "11111111-1111-4111-8111-111111111111"
		categoryID = "22222222-2222-4222-8222-222222222222"
	)
	actor := svcmarking.Actor{UserID: "33333333-3333-4333-8333-333333333333", Role: "coordinator"}

	// Composed exactly as run() composes it.
	plain := &countingScopeReader{event: activeScopeEvent()}
	markingPolicy := svcmarking.NewPolicy(plain)
	readPolicy := svcmarking.NewPolicy(markingcache.NewReader(plain, time.Minute))

	for i := 0; i < 5; i++ {
		if err := markingPolicy.AssertCanMark(context.Background(), actor, eventID, categoryID); err != nil {
			t.Fatalf("write gate, call %d: %v", i, err)
		}
	}
	writeReads := plain.calls
	if writeReads != 5 {
		t.Errorf("the WRITE gate made %d event reads for 5 marks, want 5 — it is being served from cache", writeReads)
	}

	plain.calls = 0
	for i := 0; i < 5; i++ {
		if err := readPolicy.AssertCanMark(context.Background(), actor, eventID, categoryID); err != nil {
			t.Fatalf("read policy, call %d: %v", i, err)
		}
	}
	if plain.calls != 1 {
		t.Errorf("the READ policy made %d event reads for 5 polls, want 1 — the cache is not in the read path", plain.calls)
	}
}

// And the identifiers themselves, because the test above proves the two policies
// are DIFFERENT without proving which one run() hands to the mark guard.
//
// Asserted against the source rather than the running graph because run() dials a
// database and binds a port; there is no seam to inspect it through, and the
// property is worth guarding anyway.
func TestRun_MarkWriteGateHoldsTheUncachedPolicy(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	for _, want := range []struct {
		pattern string
		why     string
	}{
		{
			pattern: `NewRoleAwareMarkGuard\(\s*markingPolicy\b`,
			why: "the mark WRITE must be gated by the uncached policy; handing it readPolicy would admit " +
				"marks on scope that was revoked up to a cache lifetime ago",
		},
		{
			pattern: `NewReadService\(\s*readPolicy\b`,
			why:     "the polled reads must use the cached policy, or the whole change saves nothing",
		},
	} {
		if !regexp.MustCompile(want.pattern).Match(source) {
			t.Errorf("main.go no longer matches %q\n%s", want.pattern, want.why)
		}
	}
}
