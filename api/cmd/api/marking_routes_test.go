// Route-composition guard for the coordinator marking reads.
//
// routes_test.go proves no ADMIN route escapes RequireAdmin. This file proves
// the mirror property for the coordinator surface: the four new marking reads
// must sit on the AUTHENTICATED /api/v1 group behind RequirePrincipal, and must
// NOT be mounted under /admin.
//
// Both halves matter. Mounted under /admin, a coordinator gets 403 on every
// read and the screen is dead. Mounted with no guard at all, the roster and the
// live counters are world-readable — `students` is 1,534 real people.
//
// This reuses refusingLoader, reachCounter and concreteURL from routes_test.go
// (same package, untagged, so they are always in scope).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	handlerodrecord "github.com/Shruhath/OD-Amrita/api/internal/handler/od_record"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

// The four paths this story adds to the authenticated group.
var markingReadPaths = []string{
	"/api/v1/events",
	"/api/v1/events/:event_id/categories",
	"/api/v1/students/search",
	"/api/v1/mark/status",
}

// reachingMarkingService is a harmless stand-in for the read service. A route
// that slips past the guard fails as a clear assertion rather than a
// nil-pointer panic. This test is about the CHAIN in front of the handlers,
// never the handlers themselves, so the bodies stay trivial.
type reachingMarkingService struct{ reached *reachCounter }

func (s reachingMarkingService) VisibleEvents(context.Context, marking.Actor) ([]marking.Event, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingMarkingService) Categories(context.Context, marking.Actor, string) ([]marking.Category, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingMarkingService) SearchStudents(context.Context, marking.Actor, string, int) ([]marking.Student, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingMarkingService) MarkStatus(context.Context, marking.Actor, string, string) (marking.Status, error) {
	s.reached.hits++
	return marking.Status{}, nil
}

// composeMarkingRouter builds the chain exactly the way run() does for the
// authenticated (non-admin) group.
func composeMarkingRouter(t *testing.T, reached *reachCounter, loader rbac.PrincipalLoader) *gin.Engine {
	t.Helper()

	router := gin.New()
	router.Use(errormw.Handler())
	api := router.Group("/api/v1")

	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(loader))

	handlerodrecord.RegisterMarkingReadRoutes(markAPI, reachingMarkingService{reached: reached})

	// The admin surfaces are composed too, so the "not under /admin" and "the
	// admin routes survived" assertions below run against a router that genuinely
	// has them — an empty admin group would make both pass vacuously.
	admin := newAdminGroup(api, loader)
	handlerevent.RegisterAdminRoutes(admin, reachingService{reached: reached})
	handlerstudent.RegisterAdminRoutes(admin, reachingStudentService{reached: reached})

	return router
}

// 0.6-ROUTE-001a — every new read is registered, and none of them answers an
// unauthenticated request.
func TestMarkingReadsAreMountedBehindRequirePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}
	router := composeMarkingRouter(t, reached, loader)

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Path] = true
	}

	for _, path := range markingReadPaths {
		if !registered[path] {
			t.Errorf("[P0] %s is not mounted — the marking screen cannot load without it", path)
		}
	}

	for _, route := range router.Routes() {
		if !isMarkingReadPath(route.Path) {
			continue
		}
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			before := reached.hits

			request := httptest.NewRequest(route.Method, concreteURL(route.Path), nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusOK {
				t.Errorf("[P0] %s %s answered 200 with no credentials — the roster and live counters would be world-readable",
					route.Method, route.Path)
			}
			if reached.hits != before {
				t.Errorf("[P0] %s %s reached its handler with no principal", route.Method, route.Path)
			}
		})
	}
}

// 0.6-ROUTE-001b — the reads must NOT be admin-gated. This is the failure that
// would otherwise be found only by a coordinator on the day of the event.
func TestMarkingReadsAreNotMountedUnderTheAdminGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := composeMarkingRouter(t, &reachCounter{}, &refusingLoader{})

	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/admin") && isMarkingReadName(route.Path) {
			t.Errorf("[P0] %s %s is under /admin — a coordinator would get 403 on every read (C2/NFR11)",
				route.Method, route.Path)
		}
	}

	// The admin surfaces this story must leave alone.
	for _, path := range []string{"/api/v1/admin/events", "/api/v1/admin/students"} {
		found := false
		for _, route := range router.Routes() {
			if route.Path == path {
				found = true
			}
		}
		if !found {
			t.Errorf("[P0] %s disappeared — the scoped reads are siblings of the admin routes, not replacements", path)
		}
	}
}

func isMarkingReadPath(path string) bool {
	for _, candidate := range markingReadPaths {
		if candidate == path {
			return true
		}
	}
	return false
}

// adminEventPaths are admin routes that legitimately share a suffix with a
// marking read. Event administration owns /api/v1/admin/events and its category
// sub-path and predates the marking surface entirely, so matching on the suffix
// alone would flag them forever. They are named here rather than the matcher
// being narrowed back down, so the exemption is visible and finite.
var adminEventPaths = map[string]bool{
	"/api/v1/admin/events":                      true,
	"/api/v1/admin/events/:event_id/categories": true,
}

// isMarkingReadName matches a marking read wherever it is mounted, so an
// admin-prefixed copy is recognised as the SAME read.
//
// It derives the suffixes from markingReadPaths rather than naming a couple by
// hand. The earlier version tested only "/mark/status" and "/students/search",
// so registering the reads on the admin group would have produced
// /api/v1/admin/students/search and /api/v1/admin/mark/status and this test
// would still have passed — while the coordinator's event selector 403'd on the
// day of the event, which is the exact failure it exists to catch.
func isMarkingReadName(path string) bool {
	if adminEventPaths[path] {
		return false
	}
	for _, candidate := range markingReadPaths {
		suffix := strings.TrimPrefix(candidate, "/api/v1")
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}
