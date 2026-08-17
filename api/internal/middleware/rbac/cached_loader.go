package rbac

// cached_loader.go — a short-lived cache in front of the per-request principal
// read.
//
// WHY
//
//	RequirePrincipal and RequireAdmin run on every authenticated request, and
//	each ran GetUserBySubject against the database. Production measured 13,571
//	of those calls in one window — against 319 actual attendance marks. Together
//	with the revocation check beside it that is 92 authorization queries per mark
//	written, before a single handler does any work.
//
//	The answer barely changes. A users row's role is edited by an administrator
//	perhaps a handful of times in the life of an event, while the read happens
//	several times a second per open portal tab.
//
// WHAT THIS COSTS
//
//	A role change takes up to PrincipalCacheTTL to be seen. That is the whole
//	trade, and it is bounded on purpose at half a minute: long enough to collapse
//	a polling screen's requests onto one query, short enough that an
//	administrator who demotes someone does not have to explain a delay.
//
//	It is NOT the security boundary. Forced logout is the revocation deny-list,
//	which is checked separately and cached far more briefly; this only decides
//	which role a still-valid session is acting under.

import (
	"context"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
)

// PrincipalCacheTTL bounds how stale a loaded principal may be.
const PrincipalCacheTTL = 30 * time.Second

// CachedPrincipalLoader wraps a PrincipalLoader with a bounded TTL cache whose
// concurrent misses collapse onto one load.
type CachedPrincipalLoader struct {
	inner PrincipalLoader
	cache *cache.TTL[Principal]
}

// NewCachedPrincipalLoader wraps loader. It reuses internal/cache, so the miss
// collapsing that already absorbs the public dashboard's spike applies here too:
// a portal screen polling on a timer produces one query per TTL, not one per
// poll.
func NewCachedPrincipalLoader(inner PrincipalLoader) *CachedPrincipalLoader {
	return &CachedPrincipalLoader{inner: inner, cache: cache.New[Principal](PrincipalCacheTTL)}
}

// Load returns the cached principal for subject, or loads and caches one.
//
// Errors are never cached (cache.TTL's contract), which matters more here than
// it looks: ErrPrincipalNotFound is how an unprovisioned caller is refused, and
// pinning that for the TTL would keep refusing someone for half a minute after
// their first login had already provisioned them.
func (l *CachedPrincipalLoader) Load(ctx context.Context, subject string) (Principal, error) {
	return l.cache.Do(ctx, subject, func(ctx context.Context) (Principal, error) {
		return l.inner.Load(ctx, subject)
	})
}

// Forget drops one subject's cached principal so the next request re-reads it.
//
// For a role that was just CHANGED by this process, where waiting out the TTL
// would leave an administrator looking at a stale answer immediately after
// editing it.
func (l *CachedPrincipalLoader) Forget(subject string) { l.cache.Forget(subject) }

// Clear drops every cached principal.
//
// For a change whose affected subjects the caller cannot name — a forced logout
// is the case that needs it. Rare by nature, so reloading is the right price.
func (l *CachedPrincipalLoader) Clear() { l.cache.Clear() }
