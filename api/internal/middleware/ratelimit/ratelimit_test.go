// Red-phase unit tests for Story 0.10's public rate limiter (AC 7, AC 8).
//
// COMPILE-RED BY DESIGN. Nothing in this package exists yet beyond doc.go's
// placeholder, so the first failure is `undefined: New` and friends. That is the
// point: these tests PRESCRIBE the surface, and the blast radius is exactly one
// package that today has no other tests to take down with it.
//
// The prescribed surface, so the dev is not reverse-engineering it from
// compiler errors:
//
//	type Config struct {
//	    PerMinute  int            // sustained refill rate; 0 -> invalid
//	    Burst      int            // bucket capacity; 0 -> invalid
//	    IdleTTL    time.Duration  // evict a bucket untouched for this long
//	    MaxEntries int            // hard cap on tracked client keys
//	    Now        func() time.Time // nil -> time.Now; injected here so refill is testable
//	}
//	func New(Config) (*Limiter, error)
//	func (*Limiter) Middleware() gin.HandlerFunc
//	func (*Limiter) Entries() int      // tracked client keys — the bounded-memory oracle
//	func (*Limiter) ClientKey(*gin.Context) string
//
// ClientKey hangs off the Limiter rather than being a free function because
// whether Fly-Client-IP may be believed is a property of the DEPLOYMENT
// (Config.TrustEdgeHeader), not of the request.
//
// `Now` is injectable because AC 7 requires proving the bucket REFILLS, and the
// only alternative is a test that sleeps — which is both slow and flaky. `Entries`
// is exported for the same reason: "the limiter's memory is bounded" is an
// assertion about internal state, and a test that cannot see that state can only
// pretend to make it.
//
// DELIBERATELY NOT behind an env gate or a build tag. 0.9 shipped a suite behind
// ATDD_ACTIVE_09 that nothing ever set; these run in plain `go test ./...`.
package ratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
)

// testClock is the injected time source. Refill is a function of elapsed time,
// and a test that waits for real seconds to pass is a test that will one day
// fail on a loaded CI runner for reasons that have nothing to do with the code.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// newTestClock starts at a fixed instant so failure output is reproducible.
func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)}
}

// limitedRouter composes the limiter the way api/cmd/api/main.go must: the error
// middleware at the root renders the envelope, the limiter guards the public
// group, and the handler counts the requests that got through.
func limitedRouter(t *testing.T, cfg Config) (*gin.Engine, *Limiter, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	limiter, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}

	served := 0
	router := gin.New()
	router.Use(errormw.Handler())
	public := router.Group("/api/v1/public")
	public.Use(limiter.Middleware())
	public.GET("/students/search", func(c *gin.Context) {
		served++
		c.JSON(http.StatusOK, gin.H{"data": []any{}})
	})
	return router, limiter, &served
}

// get issues one request, optionally with headers and a specific TCP peer.
func get(router *gin.Engine, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/students/search?q=CB.", nil)
	if remoteAddr != "" {
		request.RemoteAddr = remoteAddr
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// 0.10-RL-001 — the burst is spendable, the next request is refused, and the
// refusal is the EXISTING typed envelope rather than a hand-written body.
//
// RATE_LIMITED already maps to 429 in domain/errors.go. Inventing a new code
// would mean editing openapi.yaml, errors.go and errors_test.go for no gain.
func TestLimiter_AllowsTheBurstThenRefusesWithTypedEnvelopeAndRetryAfter(t *testing.T) {
	clock := newTestClock()
	router, _, served := limitedRouter(t, Config{PerMinute: 30, Burst: 3, IdleTTL: 10 * time.Minute, MaxEntries: 1000, Now: clock.Now})

	for i := 1; i <= 3; i++ {
		if code := get(router, "203.0.113.7:5000", nil).Code; code != http.StatusOK {
			t.Fatalf("[P0] request %d of the burst was refused with %d; a burst of 3 must admit 3", i, code)
		}
	}
	if *served != 3 {
		t.Fatalf("[P0] handler served %d requests, want 3", *served)
	}

	refused := get(router, "203.0.113.7:5000", nil)
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("[P0] request 4 answered %d, want 429 — an unauthenticated endpoint with no limiter is a "+
			"roster enumeration tool", refused.Code)
	}
	if *served != 3 {
		t.Errorf("[P0] the refused request still reached the handler (served=%d); the limiter must abort, "+
			"not merely annotate", *served)
	}

	var body contracts.Error
	if err := json.Unmarshal(refused.Body.Bytes(), &body); err != nil {
		t.Fatalf("[P0] 429 body is not a typed error envelope: %v (body=%q)", err, refused.Body.String())
	}
	if body.Code != contracts.ErrorCodeRATELIMITED {
		t.Errorf("[P0] 429 code = %q, want RATE_LIMITED — reuse the existing code, do not add a new one", body.Code)
	}

	retryAfter := refused.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatalf("[P0] 429 carries no Retry-After header; without it a client cannot know it may recover, " +
			"and the UI has nothing truthful to tell the student (AC 7, AC 12)")
	}
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil || seconds <= 0 {
		t.Errorf("[P0] Retry-After = %q, want a positive integer number of seconds", retryAfter)
	}
}

// 0.10-RL-002 — the bucket refills, so a legitimate visitor recovers on their own.
//
// A limiter that refuses permanently after one burst is a denial of service the
// project inflicted on itself. At 60/minute one token is worth one second.
func TestLimiter_RefillsOverTimeSoALegitimateVisitorRecovers(t *testing.T) {
	clock := newTestClock()
	router, _, _ := limitedRouter(t, Config{PerMinute: 60, Burst: 2, IdleTTL: 10 * time.Minute, MaxEntries: 1000, Now: clock.Now})

	get(router, "203.0.113.8:5000", nil)
	get(router, "203.0.113.8:5000", nil)
	if code := get(router, "203.0.113.8:5000", nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("[P0] the bucket did not empty after its burst: got %d", code)
	}

	clock.advance(1100 * time.Millisecond)

	if code := get(router, "203.0.113.8:5000", nil).Code; code != http.StatusOK {
		t.Errorf("[P0] still refused (%d) after 1.1s at 60/min; the bucket must refill or a student who "+
			"searched too fast once is locked out forever", code)
	}
}

// 0.10-RL-003 — one visitor's burst does not refuse a different visitor.
//
// The failure this prevents is the worst-case shape of the feature: a single
// impatient student on campus wifi taking the dashboard down for everyone.
func TestLimiter_BucketsAreIsolatedPerClientKey(t *testing.T) {
	clock := newTestClock()
	router, _, _ := limitedRouter(t, Config{PerMinute: 30, Burst: 2, IdleTTL: 10 * time.Minute, MaxEntries: 1000, Now: clock.Now})

	get(router, "203.0.113.9:5000", nil)
	get(router, "203.0.113.9:5000", nil)
	if code := get(router, "203.0.113.9:5000", nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("[P0] the first client's bucket did not empty: got %d", code)
	}

	if code := get(router, "198.51.100.4:5000", nil).Code; code != http.StatusOK {
		t.Errorf("[P0] a DIFFERENT client was refused with %d; the limiter is global rather than per-key, so "+
			"one visitor can lock out the whole campus", code)
	}
}

// 0.10-RL-004 — the client key ignores every attacker-controlled header (T9, AC 8).
//
// gin trusts ALL proxies by default and this codebase never calls
// SetTrustedProxies, so c.ClientIP() reads X-Forwarded-For straight from the
// caller. A limiter keyed on that is bypassable by rotating one header value,
// which is worse than having no limiter — it looks protected. Fly's edge sets
// Fly-Client-IP and OVERWRITES any client-supplied value, so it is trustworthy
// in production; c.RemoteIP() (the TCP peer) is the fallback.
func TestClientKey_IgnoresForgedForwardingHeaders(t *testing.T) {
	clock := newTestClock()
	router, _, _ := limitedRouter(t, Config{PerMinute: 30, Burst: 2, IdleTTL: 10 * time.Minute, MaxEntries: 1000, Now: clock.Now})

	const peer = "203.0.113.10:5000"
	get(router, peer, nil)
	get(router, peer, nil)

	// Same machine, a fresh forged X-Forwarded-For on every request. If the key
	// were header-derived, each of these would mint a brand-new full bucket.
	for i := 0; i < 5; i++ {
		forged := map[string]string{
			"X-Forwarded-For": fmt.Sprintf("10.0.0.%d", i),
			"X-Real-IP":       fmt.Sprintf("10.0.1.%d", i),
		}
		if code := get(router, peer, forged).Code; code != http.StatusTooManyRequests {
			t.Fatalf("[P0] a forged X-Forwarded-For bought request %d a fresh bucket (got %d). The limiter is "+
				"bypassable with one header — do not use c.ClientIP() or X-Forwarded-For (T9)", i, code)
		}
	}

	// Fly-Client-IP gets NO special treatment off-Fly. This limiter was built
	// with TrustEdgeHeader unset, which is what the local harness, the ngrok
	// tunnel and any direct-connect deployment run with — and there nothing
	// overwrites the header, so believing it would be the same one-header bypass
	// the assertions above exist to prevent, under a different name.
	for i := 0; i < 3; i++ {
		forged := map[string]string{"Fly-Client-IP": fmt.Sprintf("198.51.100.%d", i)}
		if code := get(router, peer, forged).Code; code != http.StatusTooManyRequests {
			t.Fatalf("[P0] a forged Fly-Client-IP bought request %d a fresh bucket (got %d) on a limiter that is "+
				"NOT behind the Fly edge. AC 8 requires one machine to share one bucket regardless of headers", i, code)
		}
	}
}

// 0.10-RL-004b — behind the Fly edge, the edge-set header IS the key.
//
// The mirror of RL-004, and the reason the trust is a flag rather than a
// deletion: behind the edge every request shares ONE TCP peer, so keying on the
// peer there would rate-limit the entire internet as a single client and take
// the dashboard down for everyone at 30 requests a minute.
func TestClientKey_TrustsTheEdgeHeaderOnlyWhenBehindTheEdge(t *testing.T) {
	clock := newTestClock()
	router, _, _ := limitedRouter(t, Config{
		PerMinute: 30, Burst: 2, IdleTTL: 10 * time.Minute, MaxEntries: 1000,
		Now: clock.Now, TrustEdgeHeader: true,
	})

	// One shared edge socket, two genuinely different visitors.
	const edgePeer = "203.0.113.10:5000"
	first := map[string]string{"Fly-Client-IP": "198.51.100.1"}
	get(router, edgePeer, first)
	get(router, edgePeer, first)
	if code := get(router, edgePeer, first).Code; code != http.StatusTooManyRequests {
		t.Fatalf("[P0] the third request from one edge client answered %d, want 429", code)
	}

	second := map[string]string{"Fly-Client-IP": "198.51.100.2"}
	if code := get(router, edgePeer, second).Code; code != http.StatusOK {
		t.Errorf("[P0] a second visitor behind the same edge socket was refused (%d); keying on the shared TCP "+
			"peer rate-limits the entire internet as one client", code)
	}
}

// 0.10-RL-005 — the limiter's own memory is bounded.
//
// An unbounded map keyed by attacker-supplied identity IS the denial of service.
// Both guards are required: TTL eviction reclaims the long tail of one-shot
// visitors, and the hard cap is what holds during a burst faster than the sweep.
func TestLimiter_BoundsItsOwnMemory(t *testing.T) {
	clock := newTestClock()
	router, limiter, _ := limitedRouter(t, Config{PerMinute: 30, Burst: 2, IdleTTL: 10 * time.Minute, MaxEntries: 50, Now: clock.Now})

	// 500 requests across 256 distinct client keys — the shape of a trivial
	// enumeration script, not of a campus. The key is the ADDRESS, not the
	// ip:port pair: RemoteIP() strips the port, so varying the port alone would
	// reuse one bucket and the loop below would prove nothing about the cap.
	for i := 0; i < 500; i++ {
		get(router, fmt.Sprintf("198.51.100.%d:%d", i%256, 6000+i), nil)
	}

	if entries := limiter.Entries(); entries > 50 {
		t.Errorf("[P0] limiter tracks %d client keys with MaxEntries=50; an unbounded map keyed by caller "+
			"identity is itself the memory-exhaustion vector this middleware exists to prevent", entries)
	}

	// Everything above is now well past IdleTTL. One fresh request is what gives
	// the limiter an opportunity to sweep; after it, only that request's own
	// bucket has any reason to still exist.
	clock.advance(11 * time.Minute)
	get(router, "203.0.113.200:5000", nil)

	if entries := limiter.Entries(); entries > 1 {
		t.Errorf("[P0] %d client keys survive an 11-minute idle period with IdleTTL=10m; idle buckets must be "+
			"swept or the map only ever grows", entries)
	}
}

// 0.10-RL-005b — a full table still admits a visitor who has never been seen.
//
// The memory bound must not be paid for with a lockout. When the map is at
// MaxEntries the attacker's keys are the ones already IN it — refusing every
// unseen key would keep serving them while turning away each new student, and
// with a Retry-After that cannot help, because recovery would need an eviction
// rather than a refill. Bounded memory is the invariant; refusing new clients is
// not.
func TestLimiter_AdmitsANewClientWhenTheTableIsFull(t *testing.T) {
	clock := newTestClock()
	router, limiter, _ := limitedRouter(t, Config{PerMinute: 30, Burst: 2, IdleTTL: 10 * time.Minute, MaxEntries: 20, Now: clock.Now})

	// Fill the table well past the cap, WITHOUT letting anything go idle, so
	// the sweep cannot be what makes room.
	for i := 0; i < 200; i++ {
		get(router, fmt.Sprintf("198.51.100.%d:6000", i%256), nil)
	}
	if entries := limiter.Entries(); entries > 20 {
		t.Fatalf("[P0] limiter tracks %d client keys with MaxEntries=20", entries)
	}

	// A student arriving now has never been seen before.
	if code := get(router, "203.0.113.77:5000", nil).Code; code != http.StatusOK {
		t.Errorf("[P0] a first-time visitor was refused (%d) because the table was full of somebody else's keys. "+
			"The limiter has become the denial of service it exists to prevent", code)
	}
	if entries := limiter.Entries(); entries > 20 {
		t.Errorf("[P0] admitting the new client pushed the table to %d entries, past MaxEntries=20; making room "+
			"must mean evicting, not growing", entries)
	}
}

// 0.10-RL-006 — an unusable configuration fails at boot, not at the first
// request, matching how every other config field in this codebase behaves.
func TestNew_RejectsUnusableConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"zero rate", Config{PerMinute: 0, Burst: 10}},
		{"negative rate", Config{PerMinute: -1, Burst: 10}},
		{"zero burst", Config{PerMinute: 30, Burst: 0}},
		{"negative burst", Config{PerMinute: 30, Burst: -5}},
		// Bounded above too. "Raise the cap for event day" is a real operator
		// instinct, and a burst of a million leaves the middleware in place
		// while removing the guard entirely — a failure that looks like
		// configuration rather than like a hole.
		{"absurd rate", Config{PerMinute: 10_000_000, Burst: 10}},
		{"absurd burst", Config{PerMinute: 30, Burst: 10_000_000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Errorf("[P1] New(%+v) succeeded; a limiter that admits nothing (or everything) must fail "+
					"fast at boot rather than silently at the first student's request", tc.cfg)
			}
		})
	}
}
