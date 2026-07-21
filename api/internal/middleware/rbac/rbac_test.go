package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

type fakeLoader struct {
	principal Principal
	err       error
	subject   string
}

func (f *fakeLoader) Load(_ context.Context, subject string) (Principal, error) {
	f.subject = subject
	return f.principal, f.err
}

func TestRequireAdminAllowsStoredAdminAndPublishesPrincipal(t *testing.T) {
	loader := &fakeLoader{principal: Principal{UserID: "admin-id", Role: RoleAdmin}}
	status, body := exerciseGuard(loader, true)
	if status != http.StatusOK || body != "admin-id" {
		t.Fatalf("response = %d %q, want 200 admin-id", status, body)
	}
	if loader.subject != "verified-subject" {
		t.Errorf("loader subject = %q, want verified-subject", loader.subject)
	}
}

func TestRequireAdminDeniesUnprovisionedAndEveryNonAdminRole(t *testing.T) {
	cases := []struct {
		name      string
		principal Principal
		err       error
	}{
		{name: "unprovisioned", err: ErrPrincipalNotFound},
		{name: "teacher", principal: Principal{Role: "teacher"}},
		{name: "coordinator", principal: Principal{Role: "coordinator"}},
		{name: "student", principal: Principal{Role: "student"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := exerciseGuard(&fakeLoader{principal: tc.principal, err: tc.err}, true)
			if status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", status, body)
			}
			if body == "" || !contains(body, "RBAC_FORBIDDEN") {
				t.Errorf("body = %q, want canonical RBAC_FORBIDDEN", body)
			}
		})
	}
}

func TestRequireAdminFailsClosedOnMissingIdentityOrLoaderFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		loader      *fakeLoader
		setIdentity bool
	}{
		{name: "missing identity", loader: &fakeLoader{}, setIdentity: false},
		{name: "loader failure", loader: &fakeLoader{err: errors.New("database unavailable")}, setIdentity: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := exerciseGuard(tc.loader, tc.setIdentity)
			if status != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", status, body)
			}
		})
	}
}

func TestValidNonAdminCannotCallAnyEventRouteDirectly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		mwauth.SetIdentity(c, svcauth.VerifiedIdentity{Subject: "teacher-subject", Email: "teacher@amrita.edu"})
		c.Next()
	})
	admin := router.Group("/api/v1/admin")
	admin.Use(RequireAdmin(&fakeLoader{principal: Principal{UserID: "teacher-id", Role: "teacher"}}))
	reached := 0
	handler := func(c *gin.Context) { reached++; c.Status(http.StatusNoContent) }
	admin.GET("/branches", handler)
	admin.GET("/events", handler)
	admin.POST("/events", handler)
	admin.POST("/events/:event_id/archive", handler)

	for _, operation := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/branches"},
		{http.MethodGet, "/api/v1/admin/events"},
		{http.MethodPost, "/api/v1/admin/events"},
		{http.MethodPost, "/api/v1/admin/events/10000000-0000-4000-8000-000000000099/archive"},
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(operation.method, operation.path, nil)
		router.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", operation.method, operation.path, recorder.Code)
		}
	}
	if reached != 0 {
		t.Errorf("protected handlers reached %d times, want 0", reached)
	}
}

func exerciseGuard(loader PrincipalLoader, setIdentity bool) (int, string) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	if setIdentity {
		router.Use(func(c *gin.Context) {
			mwauth.SetIdentity(c, svcauth.VerifiedIdentity{Subject: "verified-subject", Email: "admin@amrita.edu"})
			c.Next()
		})
	}
	router.Use(RequireAdmin(loader))
	router.GET("/admin", func(c *gin.Context) {
		principal, ok := PrincipalFromContext(c)
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.String(http.StatusOK, principal.UserID)
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	router.ServeHTTP(recorder, req)
	return recorder.Code, recorder.Body.String()
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
