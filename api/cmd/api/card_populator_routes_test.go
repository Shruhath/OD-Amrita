package main

// card_populator_routes_test.go — mount proof for the admin roster read.
//
// TestAdminGroupGuardsEveryRouteRegisterAdminRoutesMounts discovers routes from
// the router, which makes it strong about routes that EXIST and silent about
// one that was never registered. This file asserts the specific mount, so
// deleting `group.GET("/students", ...)` fails loudly instead of quietly
// shrinking the guard test's subject.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
)

func TestAdminStudentListRouteIsMountedBehindAdminGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}
	router := gin.New()
	router.Use(errormw.Handler())
	admin := newAdminGroup(router.Group("/api/v1"), loader)
	handlerstudent.RegisterAdminRoutes(admin, reachingStudentService{reached: reached})

	if !hasRoute(router, http.MethodGet, "/api/v1/admin/students") {
		t.Fatal("[P0] GET /api/v1/admin/students is not mounted")
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/students?page=1&page_size=100", nil)
	router.ServeHTTP(recorder, req)

	if recorder.Code == http.StatusOK {
		t.Fatalf("[P0] unauthenticated student list request answered 200")
	}
	if reached.hits != 0 {
		t.Fatalf("[P0] student list handler was reached before admin guard")
	}
	if loader.calls != 0 {
		t.Fatalf("principal loader calls = %d, want zero before token auth", loader.calls)
	}
}

// Every route the student handler registers must land under the guarded admin
// prefix. A future route written with a leading "../" or registered on the
// parent group would expose the roster — or the CSV import — to any signed-in
// caller, and the discovery-based guard test above would never notice because
// it only inspects paths that already start with /api/v1/admin.
func TestStudentHandlerRegistersOnlyAdminPrefixedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(errormw.Handler())
	api := router.Group("/api/v1")
	handlerstudent.RegisterAdminRoutes(newAdminGroup(api, &refusingLoader{}), reachingStudentService{reached: &reachCounter{}})

	routes := router.Routes()
	if len(routes) == 0 {
		t.Fatal("[P0] no student routes were registered — this test is vacuous")
	}
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/v1/admin/") {
			t.Errorf("[P0] %s %s is registered outside the guarded admin prefix", route.Method, route.Path)
		}
	}
}

func hasRoute(router *gin.Engine, method, path string) bool {
	for _, route := range router.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}
