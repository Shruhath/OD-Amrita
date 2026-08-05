// Package ratelimit is the per-client request limiter guarding the
// UNAUTHENTICATED public surface (Story 0.10).
//
// SCOPE — it protects the public group and nothing else. Applying it to
// /api/v1 would throttle a coordinator mid-event, and applying it to /health
// would let a burst of platform probes mark a healthy machine as down. FR15's
// per-coordinator scan limiting is a different mechanism on the authenticated
// write path and is explicitly out of Epic 0.
//
// WHY A TOKEN BUCKET, HAND-ROLLED. The whole algorithm is ~40 lines of stdlib.
// Pulling in golang.org/x/time/rate would add the api module's first new direct
// dependency for something this size, and the injectable clock below is what
// makes refill testable without a test that sleeps.
//
// WHAT IT IS NOT. The limiter is in-process, and api/fly.toml is deployed at
// `flyctl scale count 2`, so each machine keeps its own buckets and the
// effective ceiling per client is roughly double the configured rate. That is
// accepted: this guard exists to make roster enumeration slow, not to be exact.
// A shared limiter would need Redis or a Postgres round trip on every public
// request, which buys precision this threat model does not need.
package ratelimit

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// Defaults are deliberately usable without configuration: the deployed Fly app
// carries no rate-limit secret, and a limiter that refuses to boot without one
// would take the whole API down at the next deploy.
const (
	DefaultPerMinute = 30
	DefaultBurst     = 10

	// defaultIdleTTL reclaims the long tail of one-shot visitors. Ten minutes is
	// far longer than any single visit to a page that answers one question.
	defaultIdleTTL = 10 * time.Minute
	// defaultMaxEntries is the hard ceiling that holds during a burst arriving
	// faster than the sweep can reclaim. ~1,500 students plus staff on campus
	// wifi is well under it, and each entry is a few dozen bytes.
	defaultMaxEntries = 10_000
)

// Config is the limiter's tuning. PerMinute and Burst come from environment
// configuration; the rest have working defaults and are set explicitly only by
// tests.
type Config struct {
	// PerMinute is the sustained refill rate for one client key.
	PerMinute int
	// Burst is the bucket capacity — how many requests a client may make back
	// to back before the refill rate starts to bind.
	Burst int
	// IdleTTL evicts a bucket untouched for this long. Zero uses defaultIdleTTL.
	IdleTTL time.Duration
	// MaxEntries caps tracked client keys. Zero uses defaultMaxEntries.
	MaxEntries int
	// Now is the clock. Nil uses time.Now; injected by tests so refill can be
	// asserted deterministically instead of by sleeping.
	Now func() time.Time
}

// bucket is one client's token allowance. Tokens are fractional so a refill
// rate below one per second does not truncate to zero on every request.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a bounded map of per-client token buckets.
type Limiter struct {
	perSecond  float64
	burst      float64
	idleTTL    time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	// retryAfter is precomputed: it is the same for every refusal, and it is
	// the number of whole seconds needed to earn one token back.
	retryAfter string
}

// New validates the configuration and builds the limiter.
//
// It fails rather than silently correcting: a limiter configured to admit
// nothing would take the public dashboard down for every student, and one
// configured to admit everything would leave the roster open to enumeration.
// Both are boot-time mistakes and both should surface at boot, which is how
// every other config field in this codebase behaves.
func New(cfg Config) (*Limiter, error) {
	if cfg.PerMinute <= 0 {
		return nil, fmt.Errorf("ratelimit: PerMinute must be positive, got %d", cfg.PerMinute)
	}
	if cfg.Burst <= 0 {
		return nil, fmt.Errorf("ratelimit: Burst must be positive, got %d", cfg.Burst)
	}

	idleTTL := cfg.IdleTTL
	if idleTTL <= 0 {
		idleTTL = defaultIdleTTL
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	perSecond := float64(cfg.PerMinute) / 60.0
	// Seconds needed to earn one token back, rounded UP: a client told to retry
	// in zero seconds retries immediately and is refused again.
	retryAfter := int(math.Ceil(1.0 / perSecond))
	if retryAfter < 1 {
		retryAfter = 1
	}

	return &Limiter{
		perSecond:  perSecond,
		burst:      float64(cfg.Burst),
		idleTTL:    idleTTL,
		maxEntries: maxEntries,
		now:        now,
		buckets:    make(map[string]*bucket),
		retryAfter: strconv.Itoa(retryAfter),
	}, nil
}

// Middleware debits one token per request and refuses when the bucket is empty.
//
// The refusal goes through the standard error middleware (`c.Error` + `Abort`)
// rather than writing a body here, so a public 429 is the same typed envelope
// every other refusal in the API produces and the generated client has a real
// branch for it.
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l.allow(ClientKey(c)) {
			c.Next()
			return
		}
		c.Header("Retry-After", l.retryAfter)
		_ = c.Error(domain.DomainError{
			Code:    contracts.ErrorCodeRATELIMITED,
			Message: "Too many requests. Please wait a moment and try again.",
		})
		c.Abort()
	}
}

// allow debits one token for key, refilling first, and reports whether the
// request may proceed.
func (l *Limiter) allow(key string) bool {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	b, known := l.buckets[key]
	if !known {
		// The hard cap is what holds when eviction cannot keep up — a burst of
		// distinct keys arriving faster than they go idle. Refusing the new key
		// rather than growing is the correct failure: an unbounded map keyed by
		// caller-supplied identity IS the memory-exhaustion vector this
		// middleware exists to prevent.
		if len(l.buckets) >= l.maxEntries {
			return false
		}
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.perSecond
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked drops buckets untouched for longer than IdleTTL.
//
// Done inline on each request rather than from a background goroutine: there is
// no ticker to stop on shutdown, no goroutine to leak in tests, and the map is
// already locked here. The cost is one pass over a map bounded by MaxEntries.
func (l *Limiter) sweepLocked(now time.Time) {
	for key, b := range l.buckets {
		if now.Sub(b.last) >= l.idleTTL {
			delete(l.buckets, key)
		}
	}
}

// Entries reports how many client keys are currently tracked.
//
// Exported for the bounded-memory test: "the limiter's memory is bounded" is an
// assertion about internal state, and a test that cannot observe that state can
// only pretend to make it.
func (l *Limiter) Entries() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ClientKey derives the rate-limit identity of a request.
//
// DELIBERATELY NOT c.ClientIP(), and deliberately never X-Forwarded-For or
// X-Real-IP. gin trusts ALL proxies by default and this codebase never calls
// SetTrustedProxies, so under that default c.ClientIP() reads X-Forwarded-For
// straight from the caller. A limiter keyed on a value the caller controls is
// bypassable by rotating one header — which is worse than having no limiter,
// because it looks protected.
//
// Fly-Client-IP is set by the Fly edge, which OVERWRITES any client-supplied
// value, so it is trustworthy in this deployment and is also necessary: behind
// the edge every request shares one TCP peer, so keying on the peer alone would
// rate-limit the entire internet as a single client.
//
// c.RemoteIP() is the fallback and is header-independent — it parses
// Request.RemoteAddr, the actual TCP peer. That is the correct key for the
// local harness and for any direct-connection deployment.
func ClientKey(c *gin.Context) string {
	if edge := c.GetHeader("Fly-Client-IP"); edge != "" {
		return "fly:" + edge
	}
	return "peer:" + c.RemoteIP()
}
