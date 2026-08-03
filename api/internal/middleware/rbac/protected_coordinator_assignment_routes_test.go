package rbac

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

var coordinatorAssignmentOperations = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/admin/events/10000000-0000-4000-8000-000000000099/coordinators"},
	{http.MethodPost, "/api/v1/admin/events/10000000-0000-4000-8000-000000000099/coordinators"},
}

func TestEveryCoordinatorAssignmentRouteEnforcesTheComposedAuthenticationAndRoleChain(t *testing.T) {
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
			router := newCoordinatorAssignmentRouter(t, tc.revoked, &fakeLoader{principal: tc.principal, err: tc.loadErr}, func(c *gin.Context) {
				reached++
				c.Status(http.StatusNoContent)
			})

			for _, operation := range coordinatorAssignmentOperations {
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
			if reached != 0 {
				t.Fatalf("protected coordinator assignment handlers reached %d times", reached)
			}
		})
	}
}

func TestProvisionedAdminReachesBothCoordinatorAssignmentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := 0
	loader := &fakeLoader{principal: Principal{UserID: "admin-id", Role: RoleAdmin}}
	router := newCoordinatorAssignmentRouter(t, false, loader, func(c *gin.Context) {
		principal, ok := PrincipalFromContext(c)
		if !ok || principal.Role != RoleAdmin || principal.UserID != "admin-id" {
			t.Errorf("principal in context = %#v ok=%v", principal, ok)
		}
		reached++
		c.Status(http.StatusNoContent)
	})

	for _, operation := range coordinatorAssignmentOperations {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(operation.method, operation.path, nil)
		request.Header.Set("Authorization", "Bearer valid")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s %s status=%d body=%s", operation.method, operation.path, recorder.Code, recorder.Body.String())
		}
	}
	if reached != len(coordinatorAssignmentOperations) {
		t.Fatalf("admin reached %d of %d coordinator assignment routes", reached, len(coordinatorAssignmentOperations))
	}
}

func newCoordinatorAssignmentRouter(t *testing.T, revoked bool, loader *fakeLoader, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(errormw.Handler())
	api.Use(mwauth.Middleware(routeVerifier{}, "amrita.edu", routeAuditor{}))
	api.Use(mwauth.RevocationGuard(routeRevocations{revoked: revoked}))
	admin := api.Group("/admin")
	admin.Use(RequireAdmin(loader))
	admin.GET("/events/:event_id/coordinators", handler)
	admin.POST("/events/:event_id/coordinators", handler)
	return router
}
