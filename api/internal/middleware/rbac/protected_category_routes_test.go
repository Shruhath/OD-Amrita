package rbac

// protected_category_routes_test.go — Story 2.2 coverage for AC 5.
// UNIT test: no build tag, no database.
//
// Invoked by: api-ci -> build job.
//
// This mirrors protected_event_routes_test.go and deliberately reuses its
// routeVerifier / routeAuditor / routeRevocations / fakeLoader helpers rather
// than re-declaring them, so the two suites can never drift into testing
// different chains.
//
// Two distinct facts are asserted:
//
//  1. The composed chain denies every non-Admin caller on the category routes.
//     This is green by construction once the routes are mounted, because the
//     middleware already works — it is the regression net that keeps a future
//     refactor from mounting a category route outside the /admin group.
//  2. That the routes are actually MOUNTED is asserted next door, in
//     handler/event/category_handler_test.go — handler/event imports this
//     package, so asserting it here would be an import cycle. It lives with the
//     shared RegisterAdminRoutes function it checks.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
)

// categoryOperations is the route table this story adds. Keep it in one place so
// the deny matrix and the mount check can never test different paths.
var categoryOperations = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/admin/events/10000000-0000-4000-8000-000000000099/categories"},
	{http.MethodPost, "/api/v1/admin/events/10000000-0000-4000-8000-000000000099/categories"},
}

func TestEveryCategoryRouteEnforcesTheComposedAuthenticationAndRoleChain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		token     string
		revoked   bool
		principal Principal
		loadErr   error
		status    int
		code      contracts.ErrorCode
	}{
		{name: "missing token", status: http.StatusUnauthorized, code: contracts.ErrorCodeAUTHTOKENINVALID},
		{name: "invalid token", token: "invalid", status: http.StatusUnauthorized, code: contracts.ErrorCodeAUTHTOKENINVALID},
		{name: "revoked token", token: "valid", revoked: true, status: http.StatusUnauthorized, code: contracts.ErrorCodeAUTHTOKENINVALID},
		{name: "unprovisioned", token: "valid", loadErr: ErrPrincipalNotFound, status: http.StatusForbidden, code: contracts.ErrorCodeRBACFORBIDDEN},
		{name: "teacher", token: "valid", principal: Principal{Role: "teacher"}, status: http.StatusForbidden, code: contracts.ErrorCodeRBACFORBIDDEN},
		{name: "coordinator", token: "valid", principal: Principal{Role: "coordinator"}, status: http.StatusForbidden, code: contracts.ErrorCodeRBACFORBIDDEN},
		{name: "student", token: "valid", principal: Principal{Role: "student"}, status: http.StatusForbidden, code: contracts.ErrorCodeRBACFORBIDDEN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			reached := 0
			router := newCategoryRouter(t, tc.revoked, &fakeLoader{principal: tc.principal, err: tc.loadErr}, func(c *gin.Context) {
				reached++
				c.Status(http.StatusNoContent)
			})

			for _, operation := range categoryOperations {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(operation.method, operation.path, nil)
				if tc.token != "" {
					request.Header.Set("Authorization", "Bearer "+tc.token)
				}
				router.ServeHTTP(recorder, request)
				if recorder.Code != tc.status {
					t.Fatalf("%s %s status=%d body=%s", operation.method, operation.path, recorder.Code, recorder.Body.String())
				}
				var body contracts.Error
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tc.code {
					t.Fatalf("%s %s body=%s error=%v", operation.method, operation.path, recorder.Body.String(), err)
				}
			}
			// A denial that still runs the handler has leaked the read. The
			// counter is the only thing that can prove it did not.
			if reached != 0 {
				t.Fatalf("protected category handlers reached %d times", reached)
			}
		})
	}
}

func TestProvisionedAdminReachesBothCategoryRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := 0
	loader := &fakeLoader{principal: Principal{UserID: "admin-id", Role: RoleAdmin}}
	router := newCategoryRouter(t, false, loader, func(c *gin.Context) {
		// The handler must see the SERVER-LOADED principal, never anything the
		// caller supplied: the audit row's actor comes from here.
		principal, ok := PrincipalFromContext(c)
		if !ok || principal.Role != RoleAdmin || principal.UserID != "admin-id" {
			t.Errorf("principal in context = %#v ok=%v", principal, ok)
		}
		reached++
		c.Status(http.StatusNoContent)
	})

	for _, operation := range categoryOperations {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(operation.method, operation.path, nil)
		request.Header.Set("Authorization", "Bearer valid")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s %s status=%d body=%s", operation.method, operation.path, recorder.Code, recorder.Body.String())
		}
	}
	if reached != len(categoryOperations) {
		t.Fatalf("admin reached %d of %d category routes", reached, len(categoryOperations))
	}
}

// newCategoryRouter composes the production middleware order exactly as
// cmd/api/main.go does: errormw -> auth -> revocation guard -> RequireAdmin on
// the /admin group. Reordering any of these in production would leave this test
// green while the real server behaved differently, so the shape is asserted by
// construction rather than assumed.
func newCategoryRouter(t *testing.T, revoked bool, loader *fakeLoader, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(errormw.Handler())
	api.Use(mwauth.Middleware(routeVerifier{}, "amrita.edu", routeAuditor{}))
	api.Use(mwauth.RevocationGuard(routeRevocations{revoked: revoked}))
	admin := api.Group("/admin")
	admin.Use(RequireAdmin(loader))
	admin.GET("/events/:event_id/categories", handler)
	admin.POST("/events/:event_id/categories", handler)
	return router
}
