// Package ratelimit is the per-client request limiter guarding the
// UNAUTHENTICATED public surface.
//
// SCOPE — it protects the public group and nothing else. Applying it to
// /api/v1 would throttle a coordinator mid-event, and applying it to /health
// would let a burst of platform probes mark a healthy machine as down. FR15's
// per-coordinator scan limiting is a different mechanism on the authenticated
// write path and is explicitly out of scope here.
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

	// maxKeyLen bounds what one entry can cost. The key is derived from a
	// request header, and Go's default header limit is 1 MB — without this a
	// caller sends 8 KB of junk per key and turns a 10,000-entry cap sized for
	// "a few dozen bytes" into 80 MB. An IPv6 address with a zone is far under
	// this; anything longer is not an address.
	maxKeyLen = 64

	// maxConfiguredRate is the upper bound on the tunable limits. A limiter is
	// only a limiter while it refuses something: PUBLIC_RATE_LIMIT_BURST set to
	// a million is indistinguishable from having no guard, and "raise the cap
	// on event day" is exactly the operation that would do it by accident.
	maxConfiguredRate = 10_000

	// sweepInterval amortises eviction. Sweeping on EVERY request walks the
	// whole map under the global mutex, so a table filled to MaxEntries makes
	// each subsequent request pay a 10,000-entry scan — the defence getting
	// more expensive exactly as the attack gets heavier. Once every few seconds
	// reclaims idle entries just as effectively against a TTL measured in
	// minutes.
	sweepInterval = 5 * time.Second

	// evictionSample bounds the work done when the table is full. Scanning the
	// whole map for the true oldest entry would reintroduce the O(n) cost the
	// amortised sweep just removed; sampling evicts something genuinely stale
	// with a fixed budget.
	evictionSample = 64
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
	// TrustEdgeHeader says whether Fly-Client-IP may be believed — i.e. whether
	// this process actually sits behind the Fly edge, which overwrites that
	// header on every request.
	//
	// It defaults to FALSE, and that default is the security-relevant one.
	// Believing the header off-Fly makes the limiter bypassable by rotating a
	// value the caller supplies, which is precisely what this package refuses to
	// do with X-Forwarded-For. cmd/api sets it from FLY_APP_NAME, a variable
	// only the Fly runtime sets.
	TrustEdgeHeader bool
}

// bucket is one client's token allowance. Tokens are fractional so a refill
// rate below one per second does not truncate to zero on every request.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a bounded map of per-client token buckets.
type Limiter struct {
	perSecond       float64
	burst           float64
	idleTTL         time.Duration
	maxEntries      int
	now             func() time.Time
	trustEdgeHeader bool

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
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
	// Bounded ABOVE as well as below. Both directions are boot-time mistakes
	// that look like configuration: zero takes the dashboard down, and a
	// million quietly removes the guard while leaving the middleware in place.
	if cfg.PerMinute > maxConfiguredRate {
		return nil, fmt.Errorf("ratelimit: PerMinute must be at most %d, got %d", maxConfiguredRate, cfg.PerMinute)
	}
	if cfg.Burst > maxConfiguredRate {
		return nil, fmt.Errorf("ratelimit: Burst must be at most %d, got %d", maxConfiguredRate, cfg.Burst)
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
		perSecond:       perSecond,
		burst:           float64(cfg.Burst),
		idleTTL:         idleTTL,
		maxEntries:      maxEntries,
		now:             now,
		trustEdgeHeader: cfg.TrustEdgeHeader,
		buckets:         make(map[string]*bucket),
		lastSweep:       now(),
		retryAfter:      strconv.Itoa(retryAfter),
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
		if l.allow(l.ClientKey(c)) {
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
		// The map stays bounded, but the bound is enforced by EVICTING rather
		// than by refusing. Refusing at the cap punishes the wrong party: the
		// attacker's keys are already in the table and keep being served, while
		// every student who had not yet searched is turned away — and turned
		// away with a Retry-After that cannot help them, because recovery needs
		// an eviction, not a refill. Making room instead keeps the memory bound
		// (which is the actual invariant) without converting it into a global
		// outage.
		if len(l.buckets) >= l.maxEntries {
			l.evictOneLocked(now)
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
		// Advanced only when time moved FORWARD. Assigning unconditionally lets
		// a clock that stepped backwards move `last` back with it, so the next
		// request credits tokens for the whole skew and the burst is refunded.
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictOneLocked makes room for one new key when the table is full.
//
// It samples rather than scanning: finding the true oldest entry is O(n) on
// every admission once full, which is the cost the amortised sweep exists to
// avoid. A bounded sample reliably evicts something stale, and the entries it
// competes against are all within one IdleTTL of each other anyway.
func (l *Limiter) evictOneLocked(now time.Time) {
	oldestKey := ""
	oldest := now
	seen := 0
	for key, b := range l.buckets {
		if oldestKey == "" || b.last.Before(oldest) {
			oldestKey, oldest = key, b.last
		}
		if seen++; seen >= evictionSample {
			break
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

// sweepLocked drops buckets untouched for longer than IdleTTL.
//
// Done inline rather than from a background goroutine: there is no ticker to
// stop on shutdown, no goroutine to leak in tests, and the map is already locked
// here.
//
// AMORTISED, not per-request. The pass is O(len(buckets)) with the single global
// mutex held, so running it on every request means a table filled to MaxEntries
// makes each subsequent public request walk 10,000 entries — the guard growing
// more expensive in exact proportion to the flood it is supposed to absorb, and
// serialising all public traffic behind one lock while it does. Against a TTL
// measured in minutes, sweeping every few seconds reclaims the same entries.
func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < sweepInterval {
		return
	}
	l.lastSweep = now
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
// Fly-Client-IP gets the same suspicion, and that is the point of the gate. The
// header is only trustworthy because the Fly edge OVERWRITES whatever the caller
// sent — a property of the deployment, not of the header. Off-Fly (the ngrok
// tunnel this repo supports, the local portal harness, any direct-connect host)
// nothing overwrites it, so believing it there would reintroduce the exact
// bypass this function rejects X-Forwarded-For for, under a different name. So
// it is believed only when the process is actually running on Fly.
//
// Behind the edge the header is also NECESSARY: every request shares one TCP
// peer there, so keying on the peer alone would rate-limit the whole internet as
// a single client.
//
// c.RemoteIP() is the fallback and is header-independent — it parses
// Request.RemoteAddr, the actual TCP peer.
func (l *Limiter) ClientKey(c *gin.Context) string {
	if l.trustEdgeHeader {
		if edge := c.GetHeader("Fly-Client-IP"); edge != "" {
			return boundKey("fly:" + edge)
		}
	}
	peer := c.RemoteIP()
	if peer == "" {
		// RemoteIP returns "" when RemoteAddr has no parseable host:port (a Unix
		// socket listener, a hand-set value). Naming that case keeps it a
		// visible shared bucket rather than an unlabelled one.
		peer = "unknown"
	}
	return boundKey("peer:" + peer)
}

// boundKey caps what a single map entry can cost. See maxKeyLen.
func boundKey(key string) string {
	if len(key) > maxKeyLen {
		return key[:maxKeyLen]
	}
	return key
}
