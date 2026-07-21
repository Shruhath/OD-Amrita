package rbac

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

type routeVerifier struct{}

func (routeVerifier) Verify(_ context.Context, token string) (svcauth.VerifiedIdentity, error) {
	if token == "invalid" {
		return svcauth.VerifiedIdentity{}, domain.DomainError{
			Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid.",
		}
	}
	return svcauth.VerifiedIdentity{Subject: "caller-subject", Email: "caller@amrita.edu", SessionID: "session-id"}, nil
}

type routeAuditor struct{}

func (routeAuditor) DomainRejected(context.Context, string, string, string) error { return nil }

type routeRevocations struct{ revoked bool }

func (r routeRevocations) IsRevoked(context.Context, svcauth.VerifiedIdentity) (bool, error) {
	return r.revoked, nil
}

func TestEveryEventRouteEnforcesTheComposedAuthenticationAndRoleChain(t *testing.T) {
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
			router := gin.New()
			api := router.Group("/api/v1")
			api.Use(errormw.Handler())
			api.Use(mwauth.Middleware(routeVerifier{}, "amrita.edu", routeAuditor{}))
			api.Use(mwauth.RevocationGuard(routeRevocations{revoked: tc.revoked}))
			admin := api.Group("/admin")
			admin.Use(RequireAdmin(&fakeLoader{principal: tc.principal, err: tc.loadErr}))
			handler := func(c *gin.Context) { reached++; c.Status(http.StatusNoContent) }
			admin.GET("/branches", handler)
			admin.GET("/events", handler)
			admin.POST("/events", handler)
			admin.POST("/events/:event_id/archive", handler)

			for _, operation := range []struct{ method, path string }{
				{http.MethodGet, "/api/v1/admin/branches"},
				{http.MethodGet, "/api/v1/admin/events"},
				{http.MethodPost, "/api/v1/admin/events"},
				{http.MethodPost, "/api/v1/admin/events/10000000-0000-4000-8000-000000000099/archive"},
			} {
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
				t.Fatalf("protected handlers reached %d times", reached)
			}
		})
	}
}
