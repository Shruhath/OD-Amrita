package rbac

// protected_teacher_routes_test.go — Story 2.3 coverage for AC 5.
// UNIT test: no build tag, no database.
//
// Invoked by: api-ci -> build job.
//
// Mirrors protected_event_routes_test.go / protected_category_routes_test.go
// and deliberately reuses their routeVerifier / routeAuditor / routeRevocations
// / fakeLoader helpers rather than re-declaring them, so all three suites can
// never drift into testing different chains.
//
// EXECUTABLE TODAY: this file composes the middleware chain against LITERAL
// path strings, exactly like protected_category_routes_test.go did before
// handler/event existed for categories — it needs no symbol from the
// not-yet-created handler/teacher or service/teacher packages, so it is green
// (denial-wise) from the moment it lands, independent of Task 4's package
// creation. The complementary "the routes are actually MOUNTED" assertion
// (RegisterAdminRoutes reaches these exact paths) belongs in
// handler/teacher/teacher_handler_test.go once that package exists — asserting
// it here would need to import handler/teacher, which imports this package,
// which would be a cycle.

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

// teacherOperations is the route table this story adds. Keep it in one place
// so the deny matrix and the (future) mount check can never test different
// paths.
var teacherOperations = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/admin/teachers"},
	{http.MethodPost, "/api/v1/admin/teachers"},
}

func TestEveryTeacherRouteEnforcesTheComposedAuthenticationAndRoleChain(t *testing.T) {
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
			router := newTeacherRouter(t, tc.revoked, &fakeLoader{principal: tc.principal, err: tc.loadErr}, func(c *gin.Context) {
				reached++
				c.Status(http.StatusNoContent)
			})

			for _, operation := range teacherOperations {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(operation.method, operation.path, nil)
				if tc.token != "" {
					request.Header.Set("Authorization", "Bearer "+tc.token)
				}
				router.ServeHTTP(recorder, request)
				if recorder.Code != tc.status {
					t.Fatalf("[P0] %s %s status=%d body=%s", operation.method, operation.path, recorder.Code, recorder.Body.String())
				}
				var body contracts.Error
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tc.code {
					t.Fatalf("[P0] %s %s body=%s error=%v", operation.method, operation.path, recorder.Body.String(), err)
				}
			}
			// A denial that still runs the handler has leaked the read/write. The
			// counter is the only thing that can prove it did not.
			if reached != 0 {
				t.Fatalf("[P0] protected teacher handlers reached %d times", reached)
			}
		})
	}
}

func TestProvisionedAdminReachesBothTeacherRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := 0
	loader := &fakeLoader{principal: Principal{UserID: "admin-id", Role: RoleAdmin}}
	router := newTeacherRouter(t, false, loader, func(c *gin.Context) {
		// The handler must see the SERVER-LOADED principal, never anything the
		// caller supplied: the audit row's actor comes from here.
		principal, ok := PrincipalFromContext(c)
		if !ok || principal.Role != RoleAdmin || principal.UserID != "admin-id" {
			t.Errorf("[P1] principal in context = %#v ok=%v", principal, ok)
		}
		reached++
		c.Status(http.StatusNoContent)
	})

	for _, operation := range teacherOperations {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(operation.method, operation.path, nil)
		request.Header.Set("Authorization", "Bearer valid")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("[P0] %s %s status=%d body=%s", operation.method, operation.path, recorder.Code, recorder.Body.String())
		}
	}
	if reached != len(teacherOperations) {
		t.Fatalf("[P0] admin reached %d of %d teacher routes", reached, len(teacherOperations))
	}
}

// newTeacherRouter composes the production middleware order exactly as
// cmd/api/main.go does: errormw -> auth -> revocation guard -> RequireAdmin on
// the /admin group. Reordering any of these in production would leave this
// test green while the real server behaved differently, so the shape is
// asserted by construction rather than assumed.
func newTeacherRouter(t *testing.T, revoked bool, loader *fakeLoader, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(errormw.Handler())
	api.Use(mwauth.Middleware(routeVerifier{}, "amrita.edu", routeAuditor{}))
	api.Use(mwauth.RevocationGuard(routeRevocations{revoked: revoked}))
	admin := api.Group("/admin")
	admin.Use(RequireAdmin(loader))
	admin.GET("/teachers", handler)
	admin.POST("/teachers", handler)
	return router
}
