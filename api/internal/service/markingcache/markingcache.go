// Package markingcache decorates the marking scope seam with the bounded
// in-process cache, for the READ path only.
//
// WHY THIS EXISTS
//
//	Every poll of GET /api/v1/mark/status runs the full scope policy before it
//	answers, and the policy asks the database four questions: what is this event,
//	is this coordinator assigned to it, does this category belong to it, and is
//	the coordinator assigned to that category. A coordinator with the screen open
//	asks all four every ten seconds. The monitoring export counted 7,914 GetEvent,
//	7,901 ListCategoriesByEvent and 7,722 CoordinatorAssignedToEvent against 319
//	marks actually written.
//
//	None of those four answers changes on its own. They change when an
//	administrator edits an event or an assignment — a handful of times in the life
//	of an event — so serving them from memory for a minute costs a bounded amount
//	of staleness and removes almost all of the traffic.
//
// A DECORATOR RATHER THAN CACHING INSIDE THE POLICY, for the same reason
// publiccache is a decorator: the policy stays one thing and the caching stays
// another, so "what may this actor mark" is still provable by reading scope.go
// alone, and the integration suite still exercises a policy with no cache in the
// way.
//
// THE READ PATH ONLY — SEE THE NOTE ON NewReader. The mark WRITE keeps an
// uncached policy, and that asymmetry is the whole safety argument.
package markingcache

import (
	"context"
	"errors"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

// ScopeReader is what the decorator needs from the reader beneath it.
//
// It is exactly marking.Reader, spelled out rather than embedded so this package
// states its own requirement instead of inheriting whatever the other one grows.
//
// It used to be marking.Reader PLUS CoordinatorAssignedToEventCategory, because
// marking.Policy reached category-level scope through an OPTIONAL interface
// found with a type assertion and ALLOWED when the assertion failed — so a
// decorator that dropped the method compiled, satisfied marking.Reader, and
// switched category scoping off for every coordinator in the system. Requiring
// it here was the guard against building exactly that.
//
// The method is part of marking.Reader itself now and the fail-open branch is
// gone, so the guard is the compiler's rather than this comment's. The list is
// still written out in full: it is the honest statement of what a scope cache
// must be able to answer.
type ScopeReader interface {
	AssignedActiveEvents(ctx context.Context, userID string) ([]marking.Event, error)
	ActiveEventsForAdminMarking(ctx context.Context) ([]marking.Event, error)
	EventByID(ctx context.Context, eventID string) (marking.Event, error)
	CoordinatorAssignedToEvent(ctx context.Context, userID, eventID string) (bool, error)
	CategoryForEvent(ctx context.Context, eventID, categoryID string) error
	CoordinatorAssignedToEventCategory(ctx context.Context, userID, eventID, categoryID string) (bool, error)
}

// Reader serves the scope policy from memory.
type Reader struct {
	inner ScopeReader

	events         *cache.TTL[answer[marking.Event]]
	categories     *cache.TTL[answer[struct{}]]
	assignments    *cache.TTL[answer[bool]]
	categoryAssign *cache.TTL[answer[bool]]
	assignedEvents *cache.TTL[answer[[]marking.Event]]
	adminEvents    *cache.TTL[answer[[]marking.Event]]
}

// The two assertions that keep the trap described on ScopeReader shut. The
// second is the load-bearing one: it is the same method set marking.Policy looks
// for, so this type can never stop satisfying that lookup without failing here
// first.
var (
	_ marking.Reader = (*Reader)(nil)
	_ ScopeReader    = (*Reader)(nil)
)

// NewReader wraps inner, holding every scope answer for ttl.
//
// USE THIS FOR THE READ POLICY ONLY. The mark write's policy must keep the plain
// reader, so the two can only ever disagree in the safe direction: for up to ttl
// the READS may still offer an event an administrator has just archived, while
// the WRITE refuses it exactly. The reverse — a write admitted on a stale
// permission — cannot happen, because the write never consults this.
//
// A ttl of zero yields caches that store nothing and call straight through,
// which is how caching is switched off with a secret rather than a redeploy.
func NewReader(inner ScopeReader, ttl time.Duration) *Reader {
	return &Reader{
		inner:          inner,
		events:         cache.New[answer[marking.Event]](ttl),
		categories:     cache.New[answer[struct{}]](ttl),
		assignments:    cache.New[answer[bool]](ttl),
		categoryAssign: cache.New[answer[bool]](ttl),
		assignedEvents: cache.New[answer[[]marking.Event]](ttl),
		adminEvents:    cache.New[answer[[]marking.Event]](ttl),
	}
}

// EventByID is the highest-value key here: it is the same answer for every
// coordinator on an event, so a whole marking team's polls collapse onto one
// query per ttl.
func (r *Reader) EventByID(ctx context.Context, eventID string) (marking.Event, error) {
	return cached(ctx, r.events, "event|"+eventID,
		func(ctx context.Context) (marking.Event, error) { return r.inner.EventByID(ctx, eventID) },
		marking.ErrEventNotFound)
}

func (r *Reader) CoordinatorAssignedToEvent(ctx context.Context, userID, eventID string) (bool, error) {
	return cached(ctx, r.assignments, "assign|"+userID+"|"+eventID,
		func(ctx context.Context) (bool, error) {
			return r.inner.CoordinatorAssignedToEvent(ctx, userID, eventID)
		},
		marking.ErrNotCoordinator, marking.ErrEventNotFound)
}

// CategoryForEvent answers only with an error, so the cached value is empty and
// the REFUSAL is what is worth storing — a category that does not belong to an
// event will not start belonging to it inside the ttl.
func (r *Reader) CategoryForEvent(ctx context.Context, eventID, categoryID string) error {
	_, err := cached(ctx, r.categories, "cat|"+eventID+"|"+categoryID,
		func(ctx context.Context) (struct{}, error) {
			return struct{}{}, r.inner.CategoryForEvent(ctx, eventID, categoryID)
		},
		marking.ErrEventNotFound, marking.ErrCategoryNotFound)
	return err
}

// CoordinatorAssignedToEventCategory is the method whose ABSENCE would silently
// disable category scoping. See ScopeReader.
func (r *Reader) CoordinatorAssignedToEventCategory(ctx context.Context, userID, eventID, categoryID string) (bool, error) {
	return cached(ctx, r.categoryAssign, "assigncat|"+userID+"|"+eventID+"|"+categoryID,
		func(ctx context.Context) (bool, error) {
			return r.inner.CoordinatorAssignedToEventCategory(ctx, userID, eventID, categoryID)
		},
		marking.ErrNotCoordinator, marking.ErrEventNotFound, marking.ErrCategoryNotFound)
}

func (r *Reader) AssignedActiveEvents(ctx context.Context, userID string) ([]marking.Event, error) {
	return cached(ctx, r.assignedEvents, "assigned|"+userID,
		func(ctx context.Context) ([]marking.Event, error) {
			return r.inner.AssignedActiveEvents(ctx, userID)
		},
		marking.ErrNotCoordinator)
}

// ActiveEventsForAdminMarking takes no parameters, so every administrator wants
// the same answer and there is nothing to key on.
func (r *Reader) ActiveEventsForAdminMarking(ctx context.Context) ([]marking.Event, error) {
	return cached(ctx, r.adminEvents, "admin-active",
		func(ctx context.Context) ([]marking.Event, error) {
			return r.inner.ActiveEventsForAdminMarking(ctx)
		})
}

// Clear drops every cached scope answer on THIS machine.
//
// For an administrator who has just archived an event or changed an assignment:
// without it they would watch their own change fail to appear for the length of
// the ttl. Peer machines expire on their own — the same shape, and the same
// reasoning, as the revocation cache after a forced logout.
func (r *Reader) Clear() {
	r.events.Clear()
	r.categories.Clear()
	r.assignments.Clear()
	r.categoryAssign.Clear()
	r.assignedEvents.Clear()
	r.adminEvents.Clear()
}

// answer pairs a value with a DECIDED refusal so that both can be cached.
//
// The distinction this type exists to draw: "this category does not belong to
// this event" is an ANSWER and holding it for a minute is correct, whereas "the
// database refused the connection" is a FAILURE and holding it would turn one bad
// second into a minute of refusals for everyone sharing the key.
type answer[V any] struct {
	value V
	err   error
}

// cached serves fn through c, storing decided refusals alongside successes and
// letting every other error through without being stored.
//
// sentinels lists the errors that count as decided. An error matching none of
// them is returned from the load, which is exactly how cache.TTL is told not to
// store anything — see its Do documentation.
func cached[V any](
	ctx context.Context,
	c *cache.TTL[answer[V]],
	key string,
	fn func(context.Context) (V, error),
	sentinels ...error,
) (V, error) {
	got, err := c.Do(ctx, key, func(ctx context.Context) (answer[V], error) {
		value, loadErr := fn(ctx)
		if loadErr == nil {
			return answer[V]{value: value}, nil
		}
		for _, sentinel := range sentinels {
			if errors.Is(loadErr, sentinel) {
				return answer[V]{err: loadErr}, nil
			}
		}
		return answer[V]{}, loadErr
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return got.value, got.err
}
