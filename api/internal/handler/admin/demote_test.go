package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcuserrole "github.com/Shruhath/OD-Amrita/api/internal/service/userrole"
)

type fakeDemotion struct {
	result svcuserrole.Result
	err    error
	actor  svcuserrole.Actor
	target string
}

func (f *fakeDemotion) Demote(_ context.Context, actor svcuserrole.Actor, target string) (svcuserrole.Result, error) {
	f.actor, f.target = actor, target
	return f.result, f.err
}

func demoteRouter(svc DemotionService, withPrincipal bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errormw.Handler())
	if withPrincipal {
		r.Use(func(c *gin.Context) {
			c.Set("rbac.principal", rbac.Principal{UserID: "10000000-0000-4000-8000-000000000001", Email: "admin@amrita.edu", Role: "admin"})
			c.Next()
		})
	}
	r.POST("/users/:user_id/demote", DemoteUserHandler(svc))
	return r
}

func TestDemoteUserHandler_RendersTheGeneratedResult(t *testing.T) {
	const target = "10000000-0000-4000-8000-000000000002"
	svc := &fakeDemotion{result: svcuserrole.Result{
		UserID: target, Email: "t@amrita.edu", PreviousRole: "teacher", Role: "student", Changed: true,
		RemovedCategoryAssignments: 2, RemovedTeacherBranches: 1, SessionsRevoked: true,
	}}
	rec := httptest.NewRecorder()
	demoteRouter(svc, true).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/users/"+target+"/demote", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if svc.target != target || svc.actor.Role != "admin" {
		t.Fatalf("service got actor=%#v target=%q", svc.actor, svc.target)
	}
	var body contracts.UserDemotionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PreviousRole != "teacher" || body.Role != "student" || !body.Changed || body.RemovedTeacherBranches != 1 || !body.SessionsRevoked {
		t.Fatalf("body = %#v", body)
	}
}

func TestDemoteUserHandler_RendersRefusalsThroughTheEnvelope(t *testing.T) {
	for _, tc := range []struct {
		code   contracts.ErrorCode
		status int
	}{
		{contracts.ErrorCodeSELFDEMOTIONREFUSED, http.StatusConflict},
		{contracts.ErrorCodeLASTADMINDEMOTIONREFUSED, http.StatusConflict},
		{contracts.ErrorCodeUSERNOTFOUND, http.StatusNotFound},
		{contracts.ErrorCodeRBACFORBIDDEN, http.StatusForbidden},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			svc := &fakeDemotion{err: domain.DomainError{Code: tc.code, Message: "refused"}}
			rec := httptest.NewRecorder()
			demoteRouter(svc, true).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/users/x/demote", nil))
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want %d", rec.Code, tc.status)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != string(tc.code) {
				t.Fatalf("body=%s", rec.Body.String())
			}
		})
	}
}

func TestDemoteUserHandler_FailsClosedWithoutAPrincipal(t *testing.T) {
	svc := &fakeDemotion{}
	rec := httptest.NewRecorder()
	demoteRouter(svc, false).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/users/x/demote", nil))
	if rec.Code != http.StatusInternalServerError || svc.target != "" {
		t.Fatalf("status=%d target=%q, want 500 without reaching the service", rec.Code, svc.target)
	}
}
