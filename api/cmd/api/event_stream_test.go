package main

// event_stream_test.go — the composition guard for the domain event stream.
//
// Two facts about run() are asserted here, and neither can be caught anywhere
// else in the codebase, because every other test builds its own object graph.
//
//  1. THE STREAM IS ATTACHED TO THE WRITE BOUNDARY. store.New(pool) without
//     WithPublisher compiles, passes every test in every package, and produces a
//     server that is silently publishing nothing at all. The failure is total and
//     invisible — there is no error, no log line, just an empty topic.
//
//  2. THE CACHES REACH THE BUS, NOT THEIR OWN Clear(). Passing revCache directly
//     to revokeDroppingCaches (as this file did before the bus existed) also
//     compiles and also passes everything — and quietly reverts the fleet-wide
//     invalidation to a single-machine one, which presents as an intermittent,
//     machine-dependent staleness that is very hard to attribute.
//
// Both are one-token slips in main.go, which is why they are checked against the
// source itself. See routes_test.go for the same technique guarding the admin
// group.

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/Shruhath/OD-Amrita/api/internal/cachebus"
	"github.com/Shruhath/OD-Amrita/api/internal/events"
	"github.com/Shruhath/OD-Amrita/api/internal/eventstream"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// newTestStream builds the stream exactly as run() does.
func newTestStream(t *testing.T) (*eventstream.Runtime, error) {
	t.Helper()
	return eventstream.New(context.Background())
}

// TestRun_WriteBoundaryCarriesTheEventPublisher — [P0].
func TestRun_WriteBoundaryCarriesTheEventPublisher(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	attached := regexp.MustCompile(`store\.New\(\s*pool\s*,\s*store\.WithPublisher\(\s*stream\.Publisher\s*\)\s*\)`)
	if !attached.Match(source) {
		t.Error("[P0] the store is not constructed with store.WithPublisher(stream.Publisher) — every domain event would be silently dropped, with no error and no log line")
	}
}

// TestRun_CacheInvalidationGoesThroughTheBus — [P0].
func TestRun_CacheInvalidationGoesThroughTheBus(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, want := range []struct {
		what    string
		pattern *regexp.Regexp
	}{
		{"the revocation cache", regexp.MustCompile(`revocation:\s*stream\.Cache\.Clearing\(`)},
		{"the principal cache", regexp.MustCompile(`principals:\s*stream\.Cache\.Clearing\(`)},
		{"the marking scope cache", regexp.MustCompile(`scope:\s*stream\.Cache\.Clearing\(`)},
	} {
		if !want.pattern.Match(source) {
			t.Errorf("[P0] %s is cleared directly rather than through the bus — the OTHER machine would keep serving the pre-change answer for the rest of the cache window", want.what)
		}
	}
	// Registration is the other half: Clearing() on a scope nothing registered
	// broadcasts correctly and clears nothing locally, which is the worse of the
	// two halves to get wrong.
	registered := regexp.MustCompile(`stream\.Cache\.Register\(cachebus\.Scope(Revocation|Principal|MarkingScope)`)
	if got := len(registered.FindAll(source, -1)); got != 3 {
		t.Errorf("[P0] found %d cache registrations, want 3 — an unregistered scope broadcasts an invalidation that clears nothing locally", got)
	}
}

// TestEventStream_DisabledIsTheDefaultAndIsInert — [P0].
//
// The behavioural half of the guard above. With no broker configured the runtime
// must produce a working publisher and a working bus that touch no network, so
// that everything wired into them in run() is a no-op rather than a nil.
func TestEventStream_DisabledIsTheDefaultAndIsInert(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")

	stream, err := newTestStream(t)
	if err != nil {
		t.Fatalf("[P0] building a disabled stream returned %v", err)
	}
	if stream.Enabled() {
		t.Fatal("[P0] the stream must be disabled with no brokers — this is how production runs")
	}
	if _, ok := stream.Publisher.(events.Nop); !ok {
		t.Errorf("[P0] disabled Publisher = %T, want events.Nop", stream.Publisher)
	}

	// The store accepts it and the write path is untouched.
	_ = store.New(nil, store.WithPublisher(stream.Publisher))

	// And the bus still clears locally, which is exactly what cmd/api relied on
	// before the bus existed.
	cleared := 0
	stream.Cache.Register(cachebus.ScopeMarkingScope, clearFunc(func() { cleared++ }))
	stream.Cache.Clearing("test", cachebus.ScopeMarkingScope).Clear()
	if cleared != 1 {
		t.Errorf("[P0] a disabled stream cleared %d times, want 1 — turning Kafka off must not stop local invalidation", cleared)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream.Close(ctx)
}

type clearFunc func()

func (f clearFunc) Clear() { f() }
