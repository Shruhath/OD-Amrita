// Package cachebus broadcasts cache invalidations across every api machine.
//
// THE PROBLEM IT SOLVES, WHICH IS ALREADY WRITTEN DOWN IN THE CODE IT FIXES
//
//	cache.TTL.Clear's doc comment says it plainly: "with several machines
//	serving, only the one that took the write knows anything the others do not…
//	the others expire on their own." That was the honest trade when there was no
//	way to tell them. It means an administrator who archives an event, or forces
//	a user out, watches their own screen update immediately while the OTHER Fly
//	machine keeps serving the pre-change answer for the rest of the cache window
//	— and which answer a given request gets depends on which machine it lands on,
//	so the symptom is intermittent and looks like a bug in the cache rather than
//	a property of it.
//
//	A broadcast closes that: the machine taking the write says so, and every
//	machine drops the same entries within milliseconds.
//
// WHY THIS IS A BROADCAST AND NOT A CONSUMER GROUP
//
//	A consumer group would deliver each invalidation to exactly ONE member, which
//	is the precise opposite of what is wanted and would leave the fleet in a worse
//	state than before — half the machines stale, non-deterministically. So every
//	process consumes with NO group id and reads every partition itself. See
//	kafka.ConsumerOptions.Group.
//
// IT IS STILL ONLY AN OPTIMISATION. If the broker is down, or the stream is off,
// every cache falls back to expiring on its TTL exactly as it does today. Nothing
// here is a correctness dependency, which is why nothing here returns an error to
// a request path.
package cachebus

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// Scope names a family of cached answers. It is a small closed set rather than a
// free string so that a typo in a publisher cannot produce an invalidation no
// consumer is listening for — the failure that would present as "the cache bus
// works except for revocations", found weeks later.
type Scope string

const (
	// ScopeRevocation covers the forced-logout deny-list verdicts.
	ScopeRevocation Scope = "revocation"
	// ScopePrincipal covers the per-request role/principal lookups.
	ScopePrincipal Scope = "principal"
	// ScopeMarkingScope covers the marking surface's event/category/assignment
	// answers.
	ScopeMarkingScope Scope = "marking_scope"
	// ScopePublicRead covers the unauthenticated dashboard's cached reads.
	ScopePublicRead Scope = "public_read"
)

// AllScopes is every scope, for the "clear everything" path and for tests that
// assert a publisher and a consumer agree on the set.
var AllScopes = []Scope{ScopeRevocation, ScopePrincipal, ScopeMarkingScope, ScopePublicRead}

// The event type this package puts on the wire. It is NOT a domain.Action: an
// invalidation is not a business fact and has no place in the audit ledger's
// closed registry. It travels on its own topic for the same reason.
//
// The constant is DEFINED in package events, beside the routing table that sends
// it to the cache topic, and only aliased here. Declaring it locally is what
// caused the original bug: the router had never heard of the type, so every
// invalidation was produced to the audit mirror and reached no consumer at all.
const eventType = events.TypeCacheInvalidated

// message is the envelope payload.
type message struct {
	Scopes []Scope `json:"scopes"`
	// Reason is free text for the operator reading the log, never branched on.
	Reason string `json:"reason,omitempty"`
}

// Clearable is the one thing this package needs of a cache. cache.TTL satisfies
// it, and so does any decorator that forwards Clear — which is how the marking
// scope cache and the public read caches join in without knowing this package
// exists.
type Clearable interface{ Clear() }

// Bus publishes and applies invalidations.
type Bus struct {
	publisher events.Publisher
	// source is this process's identity. A message carrying it is IGNORED on
	// receipt, because the machine that published it has already cleared its own
	// caches synchronously — re-clearing on the round trip would throw away
	// entries repopulated in the interim for no benefit.
	source string
	logf   func(string, ...any)

	mu       sync.RWMutex
	registry map[Scope][]Clearable
}

// New returns a bus. A nil publisher yields one that only APPLIES invalidations
// — which is a legitimate configuration and not a degraded one: a consumer-only
// process still wants its caches dropped when someone else changes something.
func New(publisher events.Publisher, source string, logf func(string, ...any)) *Bus {
	if publisher == nil {
		publisher = events.Nop{}
	}
	if logf == nil {
		logf = log.Printf
	}
	return &Bus{
		publisher: publisher,
		source:    source,
		logf:      logf,
		registry:  make(map[Scope][]Clearable),
	}
}

// Register attaches a local cache to a scope. Several caches may share one
// scope: the revocation and principal caches are cleared together by a single
// revoke today, and the bus keeps that grouping rather than inventing a finer
// one that no call site asks for.
func (b *Bus) Register(scope Scope, cache Clearable) {
	if cache == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.registry[scope] = append(b.registry[scope], cache)
}

// Invalidate clears the named scopes LOCALLY and tells every other machine to do
// the same.
//
// Local first, deliberately. The local clear is synchronous and certain; the
// broadcast is neither. Doing the certain thing first means the machine that
// took the write is never the one serving a stale answer, which is the case an
// administrator actually observes — they are looking at the screen served by the
// machine they just wrote through.
func (b *Bus) Invalidate(ctx context.Context, reason string, scopes ...Scope) {
	if len(scopes) == 0 {
		scopes = AllScopes
	}
	b.applyLocal(scopes)

	payload, err := json.Marshal(message{Scopes: scopes, Reason: reason})
	if err != nil {
		// Unreachable with a closed struct of strings, and still not worth
		// propagating: the local clear above already happened, which is the half
		// that has to work.
		b.logf("cachebus: could not encode invalidation (%v); local caches were still cleared", err)
		return
	}
	b.publisher.Publish(ctx, events.Envelope{
		ID:            uuid.NewString(),
		Type:          eventType,
		SchemaVersion: events.SchemaVersion,
		OccurredAt:    time.Now().UTC(),
		Source:        b.source,
		Payload:       payload,
	})
}

// Handle applies an invalidation that arrived from another machine. It is the
// kafka.Handler for the cache topic.
func (b *Bus) Handle(_ context.Context, envelope events.Envelope) error {
	if envelope.Type != eventType {
		return nil
	}
	// Our own broadcast, come back to us. Already applied — see Bus.source.
	if envelope.Source != "" && envelope.Source == b.source {
		return nil
	}
	var msg message
	if err := json.Unmarshal(envelope.Payload, &msg); err != nil {
		return err
	}
	scopes := msg.Scopes
	if len(scopes) == 0 {
		// An invalidation that names no scope is treated as "everything" rather
		// than as "nothing". Over-clearing costs a reload; under-clearing serves
		// a stale permission, and those are not comparable prices.
		scopes = AllScopes
	}
	b.applyLocal(scopes)
	b.logf("cachebus: cleared %v (from %s, %q)", scopes, envelope.Source, msg.Reason)
	return nil
}

func (b *Bus) applyLocal(scopes []Scope) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, scope := range scopes {
		for _, cache := range b.registry[scope] {
			cache.Clear()
		}
	}
}

// Clearing returns a Clearable that clears the given scopes through the bus.
//
// This is the ADAPTER that lets the existing wiring join the bus without being
// rewritten. cmd/api already hands `interface{ Clear() }` values to
// revokeDroppingCaches and eventEditsDroppingScopeCache; handing them this
// instead changes a local clear into a fleet-wide one, and those two types never
// learn that a broker exists.
func (b *Bus) Clearing(reason string, scopes ...Scope) Clearable {
	return busClearer{bus: b, reason: reason, scopes: scopes}
}

type busClearer struct {
	bus    *Bus
	reason string
	scopes []Scope
}

func (c busClearer) Clear() {
	c.bus.Invalidate(context.Background(), c.reason, c.scopes...)
}
