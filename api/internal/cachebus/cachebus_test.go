package cachebus

// cachebus_test.go — UNIT tests. No broker: the bus is deliberately split so
// that "which caches get cleared, and whose messages are obeyed" is decided in
// pure Go and provable without one.
//
// Invoked by: api-ci -> build job.

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

type countingCache struct {
	mu    sync.Mutex
	count int
}

func (c *countingCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
}

func (c *countingCache) cleared() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// capturingPublisher records what the bus put on the wire.
type capturingPublisher struct {
	mu        sync.Mutex
	published []events.Envelope
}

func (p *capturingPublisher) Publish(_ context.Context, envelope events.Envelope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, envelope)
}

func (p *capturingPublisher) all() []events.Envelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]events.Envelope, len(p.published))
	copy(out, p.published)
	return out
}

func discardLog(string, ...any) {}

// TestInvalidate_ClearsLocallyAndPublishes — [P0]. The local half must happen
// even when nothing is listening: the machine that took the write is the one
// whose screen the administrator is looking at.
func TestInvalidate_ClearsLocallyAndPublishes(t *testing.T) {
	publisher := &capturingPublisher{}
	bus := New(publisher, "machine-a", discardLog)
	cache := &countingCache{}
	bus.Register(ScopeMarkingScope, cache)

	bus.Invalidate(context.Background(), "event archived", ScopeMarkingScope)

	if cache.cleared() != 1 {
		t.Errorf("[P0] local cache cleared %d times, want 1", cache.cleared())
	}
	published := publisher.all()
	if len(published) != 1 {
		t.Fatalf("[P0] published %d messages, want 1", len(published))
	}
	if published[0].Source != "machine-a" {
		t.Errorf("[P0] Source = %q, want machine-a — without it the sender cannot ignore its own echo", published[0].Source)
	}
}

// TestInvalidate_StillClearsLocallyWithNoPublisher — [P0].
//
// This is the DEFAULT production configuration (no broker). The bus must be a
// faithful stand-in for the plain Clear() call it replaced in cmd/api, or
// turning the stream off would silently stop invalidating anything at all.
func TestInvalidate_StillClearsLocallyWithNoPublisher(t *testing.T) {
	bus := New(nil, "machine-a", discardLog)
	cache := &countingCache{}
	bus.Register(ScopeRevocation, cache)

	bus.Clearing("session revoked", ScopeRevocation).Clear()

	if cache.cleared() != 1 {
		t.Fatalf("[P0] with no broker the bus cleared %d times, want 1 — this is the default deployment", cache.cleared())
	}
}

// TestHandle_IgnoresOurOwnBroadcast — [P1].
//
// Invalidate already cleared locally and synchronously. Clearing again on the
// round trip would throw away entries repopulated in between, for nothing.
func TestHandle_IgnoresOurOwnBroadcast(t *testing.T) {
	publisher := &capturingPublisher{}
	bus := New(publisher, "machine-a", discardLog)
	cache := &countingCache{}
	bus.Register(ScopeMarkingScope, cache)

	bus.Invalidate(context.Background(), "event archived", ScopeMarkingScope)
	echo := publisher.all()[0]
	if err := bus.Handle(context.Background(), echo); err != nil {
		t.Fatalf("[P1] Handle returned %v", err)
	}

	if cache.cleared() != 1 {
		t.Errorf("[P1] cleared %d times, want 1 — the machine re-applied its own broadcast", cache.cleared())
	}
}

// TestHandle_AppliesAnotherMachinesBroadcast — [P0]. THE reason this package
// exists: before it, the peer machine kept serving the pre-change answer for the
// rest of the cache window.
func TestHandle_AppliesAnotherMachinesBroadcast(t *testing.T) {
	bus := New(nil, "machine-b", discardLog)
	cache := &countingCache{}
	bus.Register(ScopeMarkingScope, cache)

	payload, err := json.Marshal(message{Scopes: []Scope{ScopeMarkingScope}, Reason: "event archived"})
	if err != nil {
		t.Fatal(err)
	}
	err = bus.Handle(context.Background(), events.Envelope{
		Type:    eventType,
		Source:  "machine-a",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("[P0] Handle returned %v", err)
	}
	if cache.cleared() != 1 {
		t.Errorf("[P0] peer machine cleared %d times, want 1", cache.cleared())
	}
}

// TestHandle_ClearsOnlyTheNamedScopes — [P1]. Over-clearing is cheap but not
// free: clearing the principal cache on every event edit would put a database
// read back on every authenticated request, which is the query this system
// removed on purpose.
func TestHandle_ClearsOnlyTheNamedScopes(t *testing.T) {
	bus := New(nil, "machine-b", discardLog)
	marking := &countingCache{}
	principal := &countingCache{}
	bus.Register(ScopeMarkingScope, marking)
	bus.Register(ScopePrincipal, principal)

	payload, _ := json.Marshal(message{Scopes: []Scope{ScopeMarkingScope}})
	if err := bus.Handle(context.Background(), events.Envelope{Type: eventType, Source: "machine-a", Payload: payload}); err != nil {
		t.Fatal(err)
	}

	if marking.cleared() != 1 {
		t.Errorf("[P1] named scope cleared %d times, want 1", marking.cleared())
	}
	if principal.cleared() != 0 {
		t.Errorf("[P1] unnamed scope cleared %d times, want 0", principal.cleared())
	}
}

// TestHandle_AnEmptyScopeListMeansEverything — [P1]. Under-clearing serves a
// stale permission; over-clearing costs a reload. Those are not comparable
// prices, so the ambiguous message resolves toward safety.
func TestHandle_AnEmptyScopeListMeansEverything(t *testing.T) {
	bus := New(nil, "machine-b", discardLog)
	caches := map[Scope]*countingCache{}
	for _, scope := range AllScopes {
		cache := &countingCache{}
		caches[scope] = cache
		bus.Register(scope, cache)
	}

	payload, _ := json.Marshal(message{})
	if err := bus.Handle(context.Background(), events.Envelope{Type: eventType, Source: "machine-a", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	for scope, cache := range caches {
		if cache.cleared() != 1 {
			t.Errorf("[P1] scope %q cleared %d times, want 1", scope, cache.cleared())
		}
	}
}

// TestHandle_IgnoresForeignEventTypes — [P2]. The consumer is subscribed to one
// topic today, but a misrouted domain event must not be read as an instruction.
func TestHandle_IgnoresForeignEventTypes(t *testing.T) {
	bus := New(nil, "machine-b", discardLog)
	cache := &countingCache{}
	bus.Register(ScopeMarkingScope, cache)

	err := bus.Handle(context.Background(), events.Envelope{Type: "od.created", Source: "machine-a"})
	if err != nil {
		t.Fatalf("[P2] Handle returned %v", err)
	}
	if cache.cleared() != 0 {
		t.Error("[P2] a domain event must not clear caches")
	}
}

// TestClearing_ReachesTheBus — [P0].
//
// cmd/api hands revokeDroppingCaches and eventEditsDroppingScopeCache an
// `interface{ Clear() }`. This is the adapter that turns those existing,
// unchanged call sites into fleet-wide invalidations, so it has to behave
// exactly like the cache's own Clear did, plus a broadcast.
func TestClearing_ReachesTheBus(t *testing.T) {
	publisher := &capturingPublisher{}
	bus := New(publisher, "machine-a", discardLog)
	revocation := &countingCache{}
	bus.Register(ScopeRevocation, revocation)

	var clearable Clearable = bus.Clearing("session revoked", ScopeRevocation)
	clearable.Clear()

	if revocation.cleared() != 1 {
		t.Errorf("[P0] Clearing() cleared %d times, want 1", revocation.cleared())
	}
	if len(publisher.all()) != 1 {
		t.Errorf("[P0] Clearing() published %d messages, want 1", len(publisher.all()))
	}
}

// TestScopesAreAClosedSet — [P2]. A free-string scope would let a typo produce
// an invalidation nothing listens for — a bug that only shows up as "the cache
// bus works, except for revocations", weeks later.
func TestScopesAreAClosedSet(t *testing.T) {
	for _, scope := range []Scope{ScopeRevocation, ScopePrincipal, ScopeMarkingScope, ScopePublicRead} {
		if !slices.Contains(AllScopes, scope) {
			t.Errorf("[P2] AllScopes is missing %q, so a whole-fleet clear would skip it", scope)
		}
	}
}
