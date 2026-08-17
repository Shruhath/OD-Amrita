package auth

// cached_revocation.go — a deliberately SHORT cache in front of the deny-list
// check that runs on every protected request.
//
// WHY
//
//	RevocationGuard consults the deny-list ahead of every authenticated handler.
//	Production measured IsTokenRevoked at 15,766 calls — the single
//	most-executed query in the database — against 319 actual attendance marks.
//	The deny-list is empty almost always and changes only when an administrator
//	revokes somebody, so nearly every one of those queries returned the same
//	"no" it returned a moment earlier.
//
// WHY THE TTL IS SHORT, AND WHY IT IS NOT ZERO-RISK-FREE
//
//	This IS the forced-logout boundary, unlike the principal cache beside it, so
//	the window is the delay before a revoked session stops being served. Ten
//	seconds is chosen to be shorter than any human notices while still collapsing
//	a screen polling every ten-to-twenty seconds onto roughly one query per poll
//	rather than one per request.
//
//	It never EXTENDS a session's life beyond what the token already grants, and
//	the admin revoke path calls Forget so the machine that took the revocation
//	drops its entry immediately. Other machines expire on their own within the
//	window — the same shape the public read cache already has, and stated in the
//	deployment runbook.
//
//	A cached "revoked = true" is not a risk in the other direction either: the
//	deny-list is append-only for the window in question, so a stale true can only
//	be correct.

import (
	"context"
	"strconv"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
)

// RevocationCacheTTL bounds how long a revocation verdict may be reused.
const RevocationCacheTTL = 10 * time.Second

// CachedRevocationChecker wraps a RevocationChecker with a bounded TTL cache.
type CachedRevocationChecker struct {
	inner RevocationChecker
	cache *cache.TTL[bool]
}

// NewCachedRevocationChecker wraps checker.
func NewCachedRevocationChecker(inner RevocationChecker) *CachedRevocationChecker {
	return &CachedRevocationChecker{inner: inner, cache: cache.New[bool](RevocationCacheTTL)}
}

// IsRevoked returns the cached verdict for this exact token, or loads one.
//
// Errors are never cached (cache.TTL's contract), so a database blip cannot pin
// a failure for the window — which here would mean refusing every request from
// that caller, since the guard fails closed.
func (c *CachedRevocationChecker) IsRevoked(ctx context.Context, id VerifiedIdentity) (bool, error) {
	return c.cache.Do(ctx, revocationKey(id), func(ctx context.Context) (bool, error) {
		return c.inner.IsRevoked(ctx, id)
	})
}

// Clear drops every cached verdict.
//
// There is deliberately no per-subject variant. The key carries the session id
// and issue time, so a subject's entries cannot be enumerated from the subject
// alone, and offering a Forget(subject) that quietly missed some of them would
// be worse than not offering one: it would look like a precise invalidation of
// the forced-logout boundary while leaving stale "not revoked" verdicts behind.
//
// Clearing costs one reload per active session, once, at the moment an
// administrator deliberately forced a logout.
func (c *CachedRevocationChecker) Clear() { c.cache.Clear() }

// revocationKey identifies one token, not one user.
//
// Subject, session and issue time are exactly the three inputs IsTokenRevoked
// matches on: the deny-list has a scope=session branch keyed on session_id and a
// scope=all branch keyed on iat. Keying on anything less would let a verdict
// computed for one token be served for another that the deny-list would judge
// differently.
func revocationKey(id VerifiedIdentity) string {
	return id.Subject + "|" + id.SessionID + "|" + strconv.FormatInt(id.IssuedAt.Unix(), 10)
}
