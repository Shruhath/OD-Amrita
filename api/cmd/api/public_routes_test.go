// Route-composition guard for Story 0.10's UNAUTHENTICATED public group.
//
// This is the single most important test in the story. Story 0.9's flagship
// defect was a guard that was written but never wired, so the feature 403'd on
// every request while the notes said "implemented and verified". The direct
// analogue here is the mount point, and the failure mode is worse: gin attaches
// middleware to the GROUP VALUE, not to the path prefix, so
//
//	router.Group("/api/v1/public")   // sibling of the auth group  -> public
//	api.Group("/public")             // child  of the auth group  -> 401 for everyone
//
// produce the IDENTICAL URL and completely different middleware chains. Nothing
// else in the suite can tell them apart: the handler tests build their own
// routers, and the contract test reads a YAML file. Only the composition is at
// risk, so the composition is what gets a test.
//
// COMPILE-RED BY DESIGN until `newPublicGroup` and
// `handlerpublicattendance.RegisterPublicRoutes` exist. That temporarily takes
// the other composition tests in this package with it, which is why Task 4
// should be done EARLY — it is what brings this package back.
//
// Reuses refusingLoader, reachCounter and concreteURL from routes_test.go (same
// package, untagged, so they are always in scope).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	handlerpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/handler/publicattendance"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// The two paths this story adds, in gin's registered form.
var publicPaths = []string{
	"/api/v1/public/students/search",
	"/api/v1/public/students/:student_id/od-records",
}

// reachingPublicService stands in for both public services so a route that
// slips past (or into) the wrong chain fails as a clear assertion rather than a
// nil-pointer panic.
type reachingPublicService struct{ reached *reachCounter }

func (s reachingPublicService) PublicSearch(_ context.Context, _ string) ([]svcstudent.SearchResult, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingPublicService) Records(_ context.Context, _ string) (publicattendance.RecordCollection, error) {
	s.reached.hits++
	return publicattendance.RecordCollection{}, nil
}

// composePublicRouter builds the chain EXACTLY the way run() must: the public
// group is created from the ROOT router, never from `api`, and carries only the
// rate limiter.
func composePublicRouter(t *testing.T, reached *reachCounter, loader rbac.PrincipalLoader) *gin.Engine {
	t.Helper()

	router := gin.New()
	router.Use(errormw.Handler())

	// The authenticated group, wired the way run() wires it, so "an authenticated
	// route still refuses a bare request" is a real assertion about the same
	// router rather than a claim about a different one.
	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(svcauth.RejectingVerifier{}, "amrita.edu", nil))
	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(loader))
	markAPI.GET("/students/search", reached.handler)
	admin := newAdminGroup(api, loader)
	admin.GET("/events", reached.handler)

	limiter, err := ratelimit.New(ratelimit.Config{PerMinute: 600, Burst: 600, IdleTTL: time.Minute, MaxEntries: 1000})
	if err != nil {
		t.Fatalf("build limiter: %v", err)
	}

	public := newPublicGroup(router, limiter.Middleware())
	handlerpublicattendance.RegisterPublicRoutes(public, reachingPublicService{reached: reached}, reachingPublicService{reached: reached})

	return router
}

// 0.10-ROUTE-001 — the load-bearing one. Both public routes answer a request
// carrying NO Authorization header, no cookie and no session.
func TestPublicRoutesAreReachableWithNoCredentialsAtAll(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}
	router := composePublicRouter(t, reached, loader)

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Path] = true
	}
	for _, path := range publicPaths {
		if !registered[path] {
			t.Errorf("[P0] %s is not mounted — the public dashboard cannot load without it", path)
		}
	}

	for _, path := range publicPaths {
		t.Run(path, func(t *testing.T) {
			before := reached.hits

			request := httptest.NewRequest(http.MethodGet, concreteURL(path)+"?q=CB.SC", nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
				t.Errorf("[P0] GET %s answered %d with no credentials. The group was almost certainly built "+
					"with api.Group(\"/public\") instead of router.Group(\"/api/v1/public\") — same URL, "+
					"inherited auth middleware, and a 401 for every student on the campus (T1)",
					path, recorder.Code)
			}
			if reached.hits == before {
				t.Errorf("[P0] GET %s never reached its handler (status %d); an anonymous visitor must be "+
					"served, not refused", path, recorder.Code)
			}
		})
	}

	if loader.calls != 0 {
		t.Errorf("[P0] the RBAC principal loader was consulted %d times for anonymous public requests; "+
			"the public group must carry no principal-loading middleware at all", loader.calls)
	}
}

// 0.10-ROUTE-002 — the other half of the same invariant: opening the public
// group did not open anything else.
//
// A test that only proves the public routes are reachable would also pass if the
// dev had deleted the auth middleware from /api/v1 entirely.
func TestAuthenticatedAndAdminRoutesStillRefuseABareRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	router := composePublicRouter(t, reached, &refusingLoader{})

	for _, path := range []string{"/api/v1/students/search", "/api/v1/admin/events"} {
		t.Run(path, func(t *testing.T) {
			before := reached.hits

			request := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusOK {
				t.Errorf("[P0] GET %s answered 200 with no credentials — adding the public group has opened "+
					"the authenticated surface", path)
			}
			if reached.hits != before {
				t.Errorf("[P0] GET %s reached its handler with no credentials", path)
			}
		})
	}
}

// 0.10-ROUTE-003 — the public routes are NOT registered under /admin or behind
// any principal-loading group, checked structurally rather than by status code.
//
// A status-code check alone can be satisfied by an implementation that answers
// 200 for the wrong reason; the route table is the ground truth about where the
// routes actually hang.
func TestPublicRoutesAreNotNestedUnderAnAuthenticatedGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := composePublicRouter(t, &reachCounter{}, &refusingLoader{})

	for _, route := range router.Routes() {
		if !strings.Contains(route.Path, "/public/") {
			continue
		}
		if strings.HasPrefix(route.Path, "/api/v1/admin") {
			t.Errorf("[P0] %s %s is under /admin", route.Method, route.Path)
		}
		if route.Method != http.MethodGet {
			t.Errorf("[P0] %s %s — the public surface is two READS. There is no POST anywhere in this group "+
				"(T2): a write reachable without authentication is an open door, not a feature",
				route.Method, route.Path)
		}
	}
}

// 0.10-ROUTE-004 — the rate limiter guards the public group and ONLY the public
// group.
//
// Applying it to /api/v1 would throttle a coordinator mid-event, and applying it
// to /health would let a burst of probes mark a healthy app as down. Per-
// coordinator scan rate limiting (FR15) is explicitly out of Epic 0.
func TestRateLimiterGuardsThePublicGroupOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	router := gin.New()
	router.Use(errormw.Handler())
	router.GET("/health", reached.handler)

	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(svcauth.RejectingVerifier{}, "amrita.edu", nil))

	// A burst of exactly one, so the SECOND request on any limited path is
	// refused. Anything the limiter does not guard keeps answering.
	limiter, err := ratelimit.New(ratelimit.Config{PerMinute: 1, Burst: 1, IdleTTL: time.Minute, MaxEntries: 100})
	if err != nil {
		t.Fatalf("build limiter: %v", err)
	}
	public := newPublicGroup(router, limiter.Middleware())
	handlerpublicattendance.RegisterPublicRoutes(public, reachingPublicService{reached: reached}, reachingPublicService{reached: reached})

	send := func(path string) int {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "203.0.113.55:5000"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if code := send("/api/v1/public/students/search?q=CB.SC"); code == http.StatusTooManyRequests {
		t.Fatalf("[P0] the FIRST public request was already rate limited (%d)", code)
	}
	if code := send("/api/v1/public/students/search?q=CB.SC"); code != http.StatusTooManyRequests {
		t.Errorf("[P0] the second public request answered %d, want 429 — the limiter is not attached to the "+
			"public group, leaving the roster open to enumeration", code)
	}

	for i := 0; i < 5; i++ {
		if code := send("/health"); code == http.StatusTooManyRequests {
			t.Errorf("[P0] /health was rate limited; a burst of platform probes would mark a healthy app down")
			break
		}
	}
	for i := 0; i < 5; i++ {
		// Unauthenticated, so 401 is the expected answer — the point is that it
		// is never 429, i.e. the limiter is not in this chain.
		if code := send("/api/v1/auth/me"); code == http.StatusTooManyRequests {
			t.Errorf("[P0] an /api/v1 route was rate limited; FR15 coordinator scan limiting is out of Epic 0 " +
				"and throttling a coordinator mid-event is a live-event outage")
			break
		}
	}
}
