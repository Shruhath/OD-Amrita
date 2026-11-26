// admin_revoke_test.go — the admin forced-logout
// endpoint POST /api/v1/admin/users/{user_id}/sessions/revoke. Composed
// in-process round-trip (errormw + seeded actor identity + handler) with a fake
// revocation service; no DB. It reuses decodeError from auth_http_test.go.
//
// Settled shapes: handler package admin ("api/internal/handler/admin"),
// admin.RevokeSessionsHandler(svc RevocationService); body/format validation in
// the handler (invalid body -> 400 without calling the service); the narrow admin
// gate in the service (RBAC_FORBIDDEN forwarded via errormw); 200 on success.
package authhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	handleradmin "github.com/Shruhath/OD-Amrita/api/internal/handler/admin"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

const targetUserID = "44444444-4444-4444-4444-444444444444"

// fakeRevSvc records the revocation call and returns a canned result/error so
// the composed test asserts handler wiring (parse, gate-forwarding, validation)
// without a real service.
type fakeRevSvc struct {
	calls    int
	gotActor svcauth.VerifiedIdentity
	gotUser  string
	gotReq   svcauth.RevokeRequest
	rev      svcauth.Revocation
	err      error
}

func (f *fakeRevSvc) RevokeUserSessions(ctx context.Context, actor svcauth.VerifiedIdentity, targetUserID string, req svcauth.RevokeRequest) (svcauth.Revocation, error) {
	f.calls++
	f.gotActor = actor
	f.gotUser = targetUserID
	f.gotReq = req
	return f.rev, f.err
}

// adminRevokeRouter seeds an actor identity (as the auth middleware would) then
// mounts the admin handler. seed is the authenticated caller.
func adminRevokeRouter(seed svcauth.VerifiedIdentity, svc *fakeRevSvc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.Use(errormw.Handler())
	grp.Use(func(c *gin.Context) {
		mwauth.SetIdentity(c, seed)
		c.Next()
	})
	grp.POST("/admin/users/:user_id/sessions/revoke", handleradmin.RevokeSessionsHandler(svc))
	return r
}

func postRevoke(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+targetUserID+"/sessions/revoke", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// TestAdminRevoke_AdminForcedLogoutSucceeds — a provisioned admin's valid
// request reaches the service with the parsed body + actor, and the endpoint
// returns a success status.
func TestAdminRevoke_AdminForcedLogoutSucceeds(t *testing.T) {
	actor := svcauth.VerifiedIdentity{Email: "admin@amrita.edu", Subject: "actor-admin", SessionID: "sess-admin"}
	svc := &fakeRevSvc{rev: svcauth.Revocation{ID: "rev-1", Inserted: true}}

	w := postRevoke(t, adminRevokeRouter(actor, svc), `{"scope":"all","reason":"offboarding a leaver"}`)

	if w.Code < 200 || w.Code >= 300 {
		t.Fatalf("[P0] admin forced logout must succeed (2xx), got %d (%s)", w.Code, w.Body.String())
	}
	if svc.calls != 1 {
		t.Fatalf("[P0] the revocation service must be called exactly once, got %d", svc.calls)
	}
	if svc.gotUser != targetUserID {
		t.Errorf("[P0] target user id from the path must be forwarded, want %q got %q", targetUserID, svc.gotUser)
	}
	if svc.gotActor != actor {
		t.Errorf("[P0] the authenticated actor must be forwarded, want %+v got %+v", actor, svc.gotActor)
	}
	if svc.gotReq.Scope != svcauth.RevokeScopeAll {
		t.Errorf("[P0] parsed scope must be all, got %q", svc.gotReq.Scope)
	}
}

// TestAdminRevoke_NonAdminForbidden — the narrow admin gate (DoD 8) lives in the
// service; the handler forwards its RBAC_FORBIDDEN through errormw. (A
// non-admin/unprovisioned actor is the service's decision; here the fake returns
// it to prove the handler renders 403, not 500.)
func TestAdminRevoke_NonAdminForbidden(t *testing.T) {
	actor := svcauth.VerifiedIdentity{Email: "student@amrita.edu", Subject: "actor-student", SessionID: "sess-s"}
	svc := &fakeRevSvc{err: domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "forbidden"}}

	w := postRevoke(t, adminRevokeRouter(actor, svc), `{"scope":"all","reason":"offboarding a leaver"}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("[P0] a non-admin actor must be 403, got %d (%s)", w.Code, w.Body.String())
	}
	if env := decodeError(t, w); env.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Errorf("[P0] want RBAC_FORBIDDEN, got %q", string(env.Code))
	}
}

// TestAdminRevoke_InvalidBodyIsValidationFailed — body validation (DoD 7) is
// rejected 400 VALIDATION_FAILED and the revocation service is NOT invoked.
func TestAdminRevoke_InvalidBodyIsValidationFailed(t *testing.T) {
	actor := svcauth.VerifiedIdentity{Email: "admin@amrita.edu", Subject: "actor-admin", SessionID: "sess-admin"}
	uuid := "55555555-5555-5555-5555-555555555555"

	cases := []struct {
		name string
		body string
	}{
		{"missing scope", `{"reason":"a valid reason"}`},
		{"unknown scope", `{"scope":"everything","reason":"a valid reason"}`},
		{"session without session_id", `{"scope":"session","reason":"a valid reason"}`},
		{"all with session_id", `{"scope":"all","session_id":"` + uuid + `","reason":"a valid reason"}`},
		{"reason too short", `{"scope":"all","reason":"ab"}`},
		{"reason blank", `{"scope":"all","reason":"   "}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeRevSvc{}
			w := postRevoke(t, adminRevokeRouter(actor, svc), tc.body)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("[P0] %s must be 400, got %d (%s)", tc.name, w.Code, w.Body.String())
			}
			if env := decodeError(t, w); env.Code != contracts.ErrorCodeVALIDATIONFAILED {
				t.Errorf("[P0] %s want VALIDATION_FAILED, got %q", tc.name, string(env.Code))
			}
			if svc.calls != 0 {
				t.Errorf("[P0] %s must not reach the revocation service, calls=%d", tc.name, svc.calls)
			}
		})
	}
}
