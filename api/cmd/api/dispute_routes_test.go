// Route-composition guard for Story 0.11's dispute surface.
//
// This story registers routes on BOTH sides of the authentication boundary in
// the same change, which is the arrangement most likely to put one on the wrong
// side. gin attaches middleware to the GROUP VALUE, not to the path prefix, so
// each of these compiles cleanly and looks right in a diff:
//
//	public.POST("/disputes", …)      // open to the internet   -> correct
//	markAPI.POST("/public/disputes") // 401 for every student   -> form is dead
//	public.GET("/disputes", …)       // every dispute, to anyone -> catastrophic
//	admin.GET("/disputes", …)        // teachers get 403        -> feature is dead for its main user
//
// The last two are the reason this file asserts the route table structurally
// rather than only checking status codes. A status check alone can be satisfied
// for the wrong reason; the route table is ground truth about where a route hangs.
//
// COMPILE-RED BY DESIGN until handler/dispute and service/dispute exist. That
// temporarily takes the other composition tests in this package with it, which is
// why the activation order puts composition early — it is what brings the package
// back.
//
// Reuses refusingLoader, reachCounter and concreteURL from routes_test.go, and
// composePublicRouter's shape from public_routes_test.go (same package, untagged).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	handlerdispute "github.com/Shruhath/OD-Amrita/api/internal/handler/dispute"
	handlerpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/handler/publicattendance"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

const (
	// gin's registered form of the two paths this story adds on each side.
	publicDisputeRoute   = "/api/v1/public/disputes"
	publicEventsRoute    = "/api/v1/public/events"
	disputeListRoute     = "/api/v1/disputes"
	disputeResolveRoute  = "/api/v1/disputes/:dispute_id/resolution"
	anonymousDisputeBody = `{"student_id":"00000000-0000-4000-8000-000000000000",` +
		`"submitted_email":"cb.sc.u4cse24101@cb.students.amrita.edu",` +
		`"event_id":"00000000-0000-4000-8000-000000000001",` +
		`"category_id":"00000000-0000-4000-8000-000000000002","slot":3}`
)

// reachingDisputeService stands in for the dispute service on both sides, so a
// route that slips into (or past) the wrong chain fails as a clear assertion
// rather than a nil-pointer panic.
type reachingDisputeService struct{ reached *reachCounter }

func (s reachingDisputeService) Submit(_ context.Context, _ dispute.SubmitInput) (dispute.Receipt, error) {
	s.reached.hits++
	return dispute.Receipt{}, nil
}

func (s reachingDisputeService) List(_ context.Context, _ dispute.Actor, _ dispute.ListFilter) (dispute.Collection, error) {
	s.reached.hits++
	return dispute.Collection{}, nil
}

func (s reachingDisputeService) Resolve(_ context.Context, _ dispute.Actor, _ string, _ string) (dispute.Record, error) {
	s.reached.hits++
	return dispute.Record{}, nil
}

func (s reachingDisputeService) PublicEvents(_ context.Context) (publicattendance.EventCollection, error) {
	s.reached.hits++
	return publicattendance.EventCollection{}, nil
}

// composeDisputeRouter builds the chain EXACTLY the way run() must, including the
// SECOND limiter as ROUTE-level middleware on the public POST only (T14).
//
// writePerMinute/writeBurst are parameters so one test can compose a permissive
// pair (to prove reachability) and another a burst of one (to prove the write
// bucket binds while the read bucket does not).
func composeDisputeRouter(t *testing.T, reached *reachCounter, loader rbac.PrincipalLoader, groupBurst, writeBurst int) *gin.Engine {
	t.Helper()

	router := gin.New()
	router.Use(errormw.Handler())

	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(svcauth.RejectingVerifier{}, "amrita.edu", nil))
	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(loader))
	admin := newAdminGroup(api, loader)
	admin.GET("/events", reached.handler)

	groupLimiter, err := ratelimit.New(ratelimit.Config{PerMinute: groupBurst, Burst: groupBurst, IdleTTL: time.Minute, MaxEntries: 1000})
	if err != nil {
		t.Fatalf("build group limiter: %v", err)
	}
	writeLimiter, err := ratelimit.New(ratelimit.Config{PerMinute: writeBurst, Burst: writeBurst, IdleTTL: time.Minute, MaxEntries: 1000})
	if err != nil {
		t.Fatalf("build dispute write limiter: %v", err)
	}

	public := newPublicGroup(router, groupLimiter.Middleware())
	svc := reachingDisputeService{reached: reached}
	handlerpublicattendance.RegisterPublicRoutes(public, reachingPublicService{reached: reached}, reachingPublicService{reached: reached})
	// The public dispute surface: the event read carries only the group limiter,
	// the WRITE carries the group limiter plus its own stricter one.
	handlerdispute.RegisterPublicRoutes(public, svc, writeLimiter.Middleware())
	// The authenticated inbox goes on markAPI — NOT on admin. Teachers are the
	// primary user of FR36/FR37 and cannot reach the admin group at all.
	handlerdispute.RegisterRoutes(markAPI, svc)

	return router
}

// postDispute sends an anonymous submission from a fixed client address.
func postDispute(router *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, publicDisputeRoute, strings.NewReader(anonymousDisputeBody))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = remoteAddr
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// 0.11-ROUTE-001 — the load-bearing one. The public dispute POST answers a
// request carrying NO Authorization header, no cookie and no session, and so
// does the public event read.
//
// 0.9's flagship defect was a guard written but never wired; 0.10's analogue was
// the public group's mount point. This story's is a POST registered on the group
// that carries auth middleware, which produces the identical URL and a 401 for
// every student who tries to report a missed slot.
func TestPublicDisputeWriteIsReachableWithNoCredentialsAtAll(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}
	router := composeDisputeRouter(t, reached, loader, 600, 600)

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[strings.ToUpper(route.Method)+" "+route.Path] = true
	}
	for _, want := range []string{
		http.MethodPost + " " + publicDisputeRoute,
		http.MethodGet + " " + publicEventsRoute,
	} {
		if !registered[want] {
			t.Errorf("[P0] %s is not mounted — a student cannot file a dispute without it", want)
		}
	}

	before := reached.hits
	recorder := postDispute(router, "203.0.113.10:5000")
	if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
		t.Errorf("[P0] POST %s answered %d with no credentials. The route was almost certainly registered on "+
			"markAPI or on a group built with api.Group(\"/public\") — same URL, inherited auth middleware, and "+
			"a dead form for every student on campus", publicDisputeRoute, recorder.Code)
	}
	if reached.hits == before {
		t.Errorf("[P0] POST %s never reached its handler (status %d); an anonymous submitter must be served, "+
			"not refused", publicDisputeRoute, recorder.Code)
	}

	before = reached.hits
	eventsRequest := httptest.NewRequest(http.MethodGet, publicEventsRoute, nil)
	eventsRequest.RemoteAddr = "203.0.113.11:5000"
	eventsRecorder := httptest.NewRecorder()
	router.ServeHTTP(eventsRecorder, eventsRequest)
	if eventsRecorder.Code == http.StatusUnauthorized || eventsRecorder.Code == http.StatusForbidden {
		t.Errorf("[P0] GET %s answered %d with no credentials", publicEventsRoute, eventsRecorder.Code)
	}
	if reached.hits == before {
		t.Errorf("[P0] GET %s never reached its handler (status %d) — the student who was NEVER MARKED is the "+
			"person this endpoint exists for, and they have no records to derive an event from (T7)",
			publicEventsRoute, eventsRecorder.Code)
	}

	if loader.calls != 0 {
		t.Errorf("[P0] the RBAC principal loader was consulted %d times for anonymous public requests; the "+
			"public group must carry no principal-loading middleware at all", loader.calls)
	}
}

// 0.11-ROUTE-002 — the authenticated dispute routes still refuse a bare request,
// and they are reachable by a TEACHER rather than sitting behind RequireAdmin.
//
// Registering them on `admin` compiles, looks right, and 403s every teacher —
// the exact user FR36 and FR37 are written for. The admin group's guard runs
// BEFORE the handler, so this is a composition property, not a service one.
func TestAuthenticatedDisputeRoutesRefuseBareRequestsAndAreNotAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	router := composeDisputeRouter(t, reached, &refusingLoader{}, 600, 600)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, disputeListRoute},
		{http.MethodPost, disputeResolveRoute},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			before := reached.hits
			request := httptest.NewRequest(tc.method, concreteURL(tc.path), strings.NewReader(`{"resolution_note":"noted"}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusOK || recorder.Code == http.StatusCreated {
				t.Errorf("[P0] %s %s answered %d with no credentials — another student's submitted email, note "+
					"and dispute history are behind this route", tc.method, tc.path, recorder.Code)
			}
			if reached.hits != before {
				t.Errorf("[P0] %s %s reached its handler with no credentials", tc.method, tc.path)
			}
		})
	}

	// Structural, not by status: prove they are NOT under /admin. A teacher who
	// gets a 403 from their own inbox reads it as a permissions bug in the portal
	// and nobody looks at the route table for a week.
	inspected := 0
	for _, route := range router.Routes() {
		if !strings.Contains(route.Path, "/disputes") || strings.Contains(route.Path, "/public/") {
			continue
		}
		inspected++
		if strings.HasPrefix(route.Path, "/api/v1/admin") {
			t.Errorf("[P0] %s %s is registered under /admin. newAdminGroup attaches RequireAdmin, so every "+
				"TEACHER — the primary user of FR36/FR37 — would get a 403 from their own dispute inbox (T16)",
				route.Method, route.Path)
		}
	}
	if inspected != 2 {
		t.Errorf("[P0] inspected %d authenticated dispute routes, want 2 (list + resolution). A test that "+
			"asserts properties of routes it never found passes most loudly when the feature is missing", inspected)
	}
}

// 0.11-ROUTE-003 — the public group holds exactly ONE write, and it is the
// dispute submission.
//
// 0.10's equivalent asserted that the public group contained no POST at all. That
// invariant is now false by design, so it is replaced rather than deleted: the
// permitted write is enumerated by name, and any OTHER non-GET appearing in this
// group is a finding. Deleting the check instead would leave the most dangerous
// group in the system with no structural guard at the moment it stopped being
// read-only.
func TestPublicGroupHoldsExactlyOneWriteAndItIsTheDispute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := composeDisputeRouter(t, &reachCounter{}, &refusingLoader{}, 600, 600)

	permittedWrites := map[string]bool{http.MethodPost + " " + publicDisputeRoute: true}

	inspected, writes := 0, 0
	for _, route := range router.Routes() {
		if !strings.Contains(route.Path, "/api/v1/public/") {
			continue
		}
		inspected++
		if route.Method == http.MethodGet {
			continue
		}
		writes++
		if !permittedWrites[route.Method+" "+route.Path] {
			t.Errorf("[P0] %s %s is an unauthenticated WRITE that nobody argued for. The dispute submission is "+
				"the single permitted one (identity-verified, audited, duplicate-guarded, doubly rate limited); "+
				"anything else reachable here is an open door", route.Method, route.Path)
		}
	}

	if writes != 1 {
		t.Errorf("[P0] the public group holds %d writes, want exactly 1", writes)
	}
	// Search + od-records + events + disputes.
	if inspected != 4 {
		t.Errorf("[P0] inspected %d public routes, want 4 (search, od-records, events, disputes)", inspected)
	}
}

// 0.11-ROUTE-004 — the dispute write has its own bucket, and spending it does NOT
// spend the public reads' budget (AC 9, T14).
//
// The group limiter alone cannot express "5 submissions a minute but keep the
// dashboard usable": one bucket means a burst of write refusals also locks a
// student out of reading their own record, and a generous read budget means the
// write is effectively unlimited. Proving the two are independent is the only way
// to tell a correctly composed pair from a single limiter applied twice.
func TestDisputeWriteLimiterIsSeparateFromTheGroupBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Group budget generous, write budget exactly one.
	router := composeDisputeRouter(t, &reachCounter{}, &refusingLoader{}, 600, 1)

	const client = "203.0.113.77:5000"

	if code := postDispute(router, client).Code; code == http.StatusTooManyRequests {
		t.Fatalf("[P0] the FIRST dispute submission was already rate limited (%d)", code)
	}

	second := postDispute(router, client)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("[P0] the second dispute submission answered %d, want 429. The write must carry its OWN "+
			"stricter limiter as ROUTE-level middleware in addition to the group's (T14); with only the group "+
			"limiter attached, the submission budget is the dashboard's browsing budget", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Errorf("[P0] the 429 carries no Retry-After header; the UI has nothing to tell the student except " +
			"\"try again\" forever (AC 9)")
	}

	// The reads must be unaffected by the write refusals above — same client key,
	// different bucket.
	readRequest := httptest.NewRequest(http.MethodGet, publicEventsRoute, nil)
	readRequest.RemoteAddr = client
	readRecorder := httptest.NewRecorder()
	router.ServeHTTP(readRecorder, readRequest)
	if readRecorder.Code == http.StatusTooManyRequests {
		t.Errorf("[P0] a public READ was rate limited after the WRITE budget was exhausted. A student who " +
			"submits twice by accident must not lose the ability to check their own attendance — the buckets " +
			"are independent by construction (AC 9)")
	}
}

// 0.11-ROUTE-005 — the stricter write limiter is attached to the DISPUTE POST
// only, not to the whole public group.
//
// Applying it at group level would throttle the dashboard to five requests a
// minute, which a single debounced search exceeds — the feature 0.10 shipped
// would break as a side effect of this story landing.
func TestDisputeWriteLimiterDoesNotThrottleTheDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := composeDisputeRouter(t, &reachCounter{}, &refusingLoader{}, 600, 1)

	const client = "203.0.113.88:5000"

	// Spend the write bucket first, so an implementation that shares one bucket
	// across the group has already exhausted it.
	postDispute(router, client)
	postDispute(router, client)

	for i := 0; i < 6; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/public/students/search?q=CB.SC", nil)
		request.RemoteAddr = client
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusTooManyRequests {
			t.Fatalf("[P0] the public search was rate limited on request %d after the dispute write budget was "+
				"spent. The stricter limiter belongs on the POST route only — gin composes route-level "+
				"middleware on top of the group's, so this is a one-line addition and not a second group (T14)", i+1)
		}
	}
}

// 0.11-ROUTE-006 — the public dispute handler never reaches an authenticated
// service, checked at the type level.
//
// The narrow interfaces are the guard: handler/dispute's public registration
// takes a submit-only service, so it cannot reach List or Resolve — the two
// methods that return another student's email and note — even by accident. This
// test exists so that widening the interface is a deliberate, visible act rather
// than an autocomplete.
func TestPublicDisputeHandlerCannotReachTheAuthenticatedReads(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// A value implementing ONLY Submit and PublicEvents must satisfy the public
	// registration. If this stops compiling because RegisterPublicRoutes now
	// demands List or Resolve, the public handler has acquired the ability to
	// read every dispute in the system.
	var _ interface {
		Submit(context.Context, dispute.SubmitInput) (dispute.Receipt, error)
	} = submitOnlyService{}

	reached := &reachCounter{}
	router := gin.New()
	router.Use(errormw.Handler())
	limiter, err := ratelimit.New(ratelimit.Config{PerMinute: 600, Burst: 600})
	if err != nil {
		t.Fatalf("build limiter: %v", err)
	}
	public := newPublicGroup(router, limiter.Middleware())
	handlerdispute.RegisterPublicRoutes(public, submitOnlyService{reached: reached}, limiter.Middleware())

	if code := postDispute(router, "203.0.113.99:5000").Code; code >= http.StatusInternalServerError {
		t.Errorf("[P0] POST %s answered %d with a submit-only service; the public handler is reaching for "+
			"something outside the narrow interface", publicDisputeRoute, code)
	}
}

// submitOnlyService implements the smallest surface the PUBLIC dispute routes may
// depend on. Deliberately has no List and no Resolve.
type submitOnlyService struct{ reached *reachCounter }

func (s submitOnlyService) Submit(_ context.Context, _ dispute.SubmitInput) (dispute.Receipt, error) {
	if s.reached != nil {
		s.reached.hits++
	}
	return dispute.Receipt{}, nil
}

func (s submitOnlyService) PublicEvents(_ context.Context) (publicattendance.EventCollection, error) {
	if s.reached != nil {
		s.reached.hits++
	}
	return publicattendance.EventCollection{}, nil
}

// Keeps the student-service import honest: the public dispute path resolves a
// student from an email, and it must do so through the dispute service's own
// roll lookup rather than by widening the roster search 0.10 hard-capped.
var _ = svcstudent.PublicSearchLimit
