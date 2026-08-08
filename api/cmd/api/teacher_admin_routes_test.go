// Route-composition guard for the three endpoints Story 0.9 adds: the
// teacher/admin portal-mark reads (authenticated, RequirePrincipal) and the
// admin attendance read (RequireAdmin).
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

	handlerodrecord "github.com/Shruhath/OD-Amrita/api/internal/handler/od_record"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

// The three paths this story adds.
var (
	portalMarkPaths = []string{
		"/api/v1/portal/mark/events",
		"/api/v1/portal/mark/events/:event_id/categories",
	}
	adminAttendancePath = "/api/v1/admin/events/:event_id/attendance"
)

// reachingPortalMarkService is a harmless stand-in for both PortalMarkService
// and AttendanceService, so a route that slips past its guard fails as a
// clear assertion rather than a nil-pointer panic.
type reachingPortalMarkService struct{ reached *reachCounter }

func (s reachingPortalMarkService) Events(context.Context, marking.Actor) ([]marking.Event, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingPortalMarkService) Categories(context.Context, marking.Actor, string) ([]marking.Category, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingPortalMarkService) Attendance(context.Context, marking.Actor, string, *string, *int, *string, int, int) (marking.AttendancePage, error) {
	s.reached.hits++
	return marking.AttendancePage{}, nil
}

// composeTeacherAdminMarkRouter builds the chain exactly the way run() does:
// portal-mark reads on the authenticated (non-admin) group, attendance under
// the admin group.
func composeTeacherAdminMarkRouter(t *testing.T, reached *reachCounter, loader rbac.PrincipalLoader) *gin.Engine {
	t.Helper()

	router := gin.New()
	router.Use(errormw.Handler())
	api := router.Group("/api/v1")

	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(loader))

	svc := reachingPortalMarkService{reached: reached}
	handlerodrecord.RegisterPortalMarkRoutes(markAPI, svc)

	admin := newAdminGroup(api, loader)
	handlerodrecord.RegisterAdminAttendanceRoutes(admin, svc)

	return router
}

// 0.9-ROUTE-001a — the portal-mark reads are registered under RequirePrincipal
// and refuse an unauthenticated request.
func TestPortalMarkReadsAreMountedBehindRequirePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}
	router := composeTeacherAdminMarkRouter(t, reached, loader)

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Path] = true
	}
	for _, path := range portalMarkPaths {
		if !registered[path] {
			t.Errorf("[P0] %s is not mounted — the teacher/admin mark screen cannot load without it", path)
		}
	}

	for _, path := range portalMarkPaths {
		t.Run(path, func(t *testing.T) {
			before := reached.hits

			request := httptest.NewRequest(http.MethodGet, concreteURL(path), nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusOK {
				t.Errorf("[P0] GET %s answered 200 with no credentials", path)
			}
			if reached.hits != before {
				t.Errorf("[P0] GET %s reached its handler with no principal", path)
			}
		})
	}
}

// 0.9-ROUTE-001b — the portal-mark reads must NOT be admin-gated, or a
// signed-in teacher gets 403 on every read.
func TestPortalMarkReadsAreNotMountedUnderTheAdminGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := composeTeacherAdminMarkRouter(t, &reachCounter{}, &refusingLoader{})

	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/admin") && strings.Contains(route.Path, "/portal/mark/") {
			t.Errorf("[P0] %s %s is under /admin — a teacher would 403 on every mark read", route.Method, route.Path)
		}
	}
}

// 0.9-ROUTE-002 — the admin attendance read is registered under RequireAdmin
// and refuses an unauthenticated request.
func TestAdminAttendanceIsMountedBehindRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}
	router := composeTeacherAdminMarkRouter(t, reached, loader)

	registered := false
	for _, route := range router.Routes() {
		if route.Path == adminAttendancePath {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("[P0] %s is not mounted — the admin attendance view cannot load without it", adminAttendancePath)
	}

	before := reached.hits
	request := httptest.NewRequest(http.MethodGet, concreteURL(adminAttendancePath), nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code == http.StatusOK {
		t.Errorf("[P0] GET %s answered 200 without credentials — attendance rows would be readable by anyone", adminAttendancePath)
	}
	if reached.hits != before {
		t.Errorf("[P0] GET %s reached its handler without credentials", adminAttendancePath)
	}
	if loader.calls != 0 {
		t.Errorf("principal loader was consulted %d times for an unauthenticated request; "+
			"RequireAdmin must reject before loading a principal", loader.calls)
	}
}
