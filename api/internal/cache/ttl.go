// Package cache is a bounded, in-process read cache for the UNAUTHENTICATED
// public surface.
//
// WHY IN-PROCESS AND NOT A SHARED STORE
//
//	The public dashboard's load is a spike, not a volume: the last teaching slot
//	ends at 18:15 IST and the whole campus opens the page over the following
//	hour to ask one question — "was I marked?". Two Fly machines each holding
//	their own copy is the right trade for that shape. A shared store would add a
//	network hop, a second thing that can be down, and a credential, to save a
//	duplicate of a few hundred kilobytes. It is also consistent with how the
//	rate limiter already works.
//
// WHAT ACTUALLY ABSORBS THE SPIKE
//
//	Not the TTL — the SINGLEFLIGHT. A thousand students refreshing at 18:15 is a
//	thousand requests across a handful of hot keys, and without collapsing them
//	each one is its own database round trip. Sharing one in-flight load per key
//	turns the spike into one query per key per TTL, which is the property that
//	keeps Neon out of trouble. The TTL alone would not: a cold cache under a
//	simultaneous spike is exactly the moment every request misses at once.
package cache

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultMaxEntries bounds how many keys a cache may hold.
//
// THIS IS A MEMORY SAFETY BOUND, NOT A TUNING KNOB. One of the cached reads is
// keyed on a student UUID taken straight from the request path of an endpoint
// that anyone on the internet may call, so an unbounded map is a remote
// out-of-memory: enumerate UUIDs and every miss allocates an entry that lives
// for the whole TTL. The roster is on the order of a couple of thousand
// students, so this holds the real working set many times over while still
// refusing to grow without limit.
const DefaultMaxEntries = 20_000

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// TTL is a bounded key/value cache whose misses are collapsed per key.
type TTL[V any] struct {
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]entry[V]

	group singleflight.Group
}

// New returns a cache holding values for ttl.
//
// A non-positive ttl yields a cache that never stores anything and always calls
// through. That is a supported configuration, not a misuse: it is how an
// operator turns caching off on the day of the event, with a secret rather than
// a redeploy, if a stale record is ever more expensive than a database read.
func New[V any](ttl time.Duration) *TTL[V] {
	return &TTL[V]{
		ttl:        ttl,
		maxEntries: DefaultMaxEntries,
		now:        time.Now,
		entries:    make(map[string]entry[V]),
	}
}

// Do returns the cached value for key, or calls load and caches what it returns.
//
// Concurrent calls for the same key while a load is in flight share that one
// load. An error is NEVER cached: pinning a transient database failure for the
// length of the TTL would turn one bad second into a minute of failures for
// every student holding that key.
func (c *TTL[V]) Do(ctx context.Context, key string, load func(context.Context) (V, error)) (V, error) {
	if c == nil || c.ttl <= 0 {
		return load(ctx)
	}
	if value, ok := c.lookup(key); ok {
		return value, nil
	}

	// singleflight.Do carries the FIRST caller's context into load. That is the
	// behaviour we want here — the loads are short reads — but it does mean a
	// cancelled first caller can fail the followers, who then simply retry on
	// their own next request rather than serving anything wrong.
	result, err, _ := c.group.Do(key, func() (any, error) {
		// Re-check under the flight: while this goroutine waited for the group,
		// an earlier flight may have already stored a fresh value.
		if value, ok := c.lookup(key); ok {
			return value, nil
		}
		value, loadErr := load(ctx)
		if loadErr != nil {
			return value, loadErr
		}
		c.store(key, value)
		return value, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	value, _ := result.(V)
	return value, nil
}

func (c *TTL[V]) lookup(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	found, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !c.now().Before(found.expiresAt) {
		delete(c.entries, key)
		var zero V
		return zero, false
	}
	return found.value, true
}

func (c *TTL[V]) store(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, replacing := c.entries[key]; !replacing && len(c.entries) >= c.maxEntries {
		c.purgeExpiredLocked()
		// Still full after purging: decline to cache rather than evict something
		// useful or grow past the bound. Declining degrades to the uncached
		// behaviour, which is correct and merely slower — the failure mode you
		// want when the alternative is unbounded memory on a public endpoint.
		if len(c.entries) >= c.maxEntries {
			return
		}
	}
	c.entries[key] = entry[V]{value: value, expiresAt: c.now().Add(c.ttl)}
}

func (c *TTL[V]) purgeExpiredLocked() {
	now := c.now()
	for key, held := range c.entries {
		if !now.Before(held.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// Len reports how many entries are held, expired ones included. For tests and
// for a future metric; not part of the read path.
func (c *TTL[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
