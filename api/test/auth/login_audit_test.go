// login_audit_test.go — Story 1.7 (AC: "when login succeeds … an immutable
// audit row is written … And no auth event path can complete without its audit
// row"). Handler-level acceptance for the auth.login_succeeded emission at
// GET /api/v1/auth/me. No DB / no build tag — runs under the default
// `go test ./...` with a fake provisioner + fake auditor, reusing the 1.5b
// handler harness (recordingProvisioner lives in provisioning_me_test.go).
//
// It proves the NEW behavior 1.7 layers on the already-proven /auth/me contract:
//   - a successful /auth/me fires LoginSucceeded EXACTLY once, with the
//     provisioned email + role, the identity's subject + session id, and the
//     request device (User-Agent) — and still returns 200 AuthenticatedIdentity;
//   - a failing auditor makes /auth/me render a centralized INTERNAL 500
//     (fail-closed) — never a 200 for a login that could not be audited.
//
// RED: handlerauth.MeHandler still takes a single (Provisioner) arg — this file
// calls the intended 2-arg MeHandler(provisioner, auditor) and references the
// not-yet-built handlerauth.Auditor interface, so package authhttp is compile-red
// until Story 1.7 Task 2 lands (and the 1.5a/1.5b call sites are updated to the
// new signature). Intended TDD red phase.
package authhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
)

// loginAuditCall records one LoginSucceeded invocation for assertion.
type loginAuditCall struct {
	email, role, subject, sessionID, device string
}

// recordingLoginAuditor is the fake for handlerauth.Auditor (the narrow
// login-success seam MeHandler will depend on). err drives the fail-closed path.
type recordingLoginAuditor struct {
	calls int
	err   error
	seen  []loginAuditCall
}

func (a *recordingLoginAuditor) LoginSucceeded(_ context.Context, email, role, subject, sessionID, device string) error {
	a.calls++
	a.seen = append(a.seen, loginAuditCall{email, role, subject, sessionID, device})
	return a.err
}

// loginAuditRouter mirrors the intended main.go wiring: errormw → seeded identity
// → MeHandler(provisioner, auditor). The auth middleware itself is proven
// elsewhere; here we seed the verified identity directly.
func loginAuditRouter(seed svcauth.VerifiedIdentity, p *recordingProvisioner, a *recordingLoginAuditor) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errormw.Handler())
	r.Use(func(c *gin.Context) {
		mwauth.SetIdentity(c, seed)
		c.Next()
	})
	r.GET("/api/v1/auth/me", handlerauth.MeHandler(p, a))
	return r
}

func doMeWithUA(t *testing.T, r *gin.Engine, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestAuthMe_SuccessAuditsLoginSucceededOnce — [P0] a successful /auth/me fires
// LoginSucceeded exactly once with the provisioned email+role, the identity's
// subject+session id, and the request device, and still returns 200.
func TestAuthMe_SuccessAuditsLoginSucceededOnce(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "Student@amrita.edu", Subject: "sub-9", SessionID: "sess-9"}
	p := &recordingProvisioner{user: svcuser.User{
		ID:              "user-9",
		SupabaseSubject: id.Subject,
		Email:           "student@amrita.edu",
		Role:            svcuser.RoleStudent,
	}}
	aud := &recordingLoginAuditor{}

	const ua = "OD-Amrita/1.0 (iPhone)"
	w := doMeWithUA(t, loginAuditRouter(id, p, aud), ua)

	if w.Code != http.StatusOK {
		t.Fatalf("[P0] a fully-audited successful /auth/me must return 200, got %d (%s)", w.Code, w.Body.String())
	}
	if aud.calls != 1 {
		t.Fatalf("[P0] /auth/me must audit auth.login_succeeded exactly once per successful login, got %d", aud.calls)
	}
	got := aud.seen[0]
	if got.email != "student@amrita.edu" {
		t.Errorf("[P0] audit must carry the PROVISIONED (lowercased) email, got %q", got.email)
	}
	if got.role != string(svcuser.RoleStudent) {
		t.Errorf("[P0] audit must carry the provisioned role, got %q", got.role)
	}
	if got.subject != id.Subject {
		t.Errorf("[P1] audit subject must be the identity's Supabase subject, got %q", got.subject)
	}
	if got.sessionID != id.SessionID {
		t.Errorf("[P1] audit session id must be the identity's Supabase session id, got %q", got.sessionID)
	}
	if got.device != ua {
		t.Errorf("[P1] audit device must be the request User-Agent, got %q", got.device)
	}

	// Behavior 1.5b proved must be unchanged: the identity is still echoed.
	var body contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response must still decode as AuthenticatedIdentity: %v (%s)", err, w.Body.String())
	}
	if body.Role != contracts.AuthenticatedIdentityRoleStudent {
		t.Errorf("[P1] provisioned role must still be echoed, got %q", body.Role)
	}
}

// TestAuthMe_FailingAuditorFailsClosed — [P0] "no auth event path can complete
// without its audit row": if the audit write fails, /auth/me renders the
// centralized INTERNAL 500 and does NOT return a 200 for the un-audited login.
func TestAuthMe_FailingAuditorFailsClosed(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "student@amrita.edu", Subject: "sub-e", SessionID: "sess-e"}
	p := &recordingProvisioner{user: svcuser.User{
		ID:              "user-e",
		SupabaseSubject: id.Subject,
		Email:           "student@amrita.edu",
		Role:            svcuser.RoleStudent,
	}}
	aud := &recordingLoginAuditor{err: errContext("audit ledger unavailable")}

	w := doMeWithUA(t, loginAuditRouter(id, p, aud), "OD-Amrita/1.0")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("[P0] a login whose audit write fails must fail closed with 500, got %d (%s)", w.Code, w.Body.String())
	}
	var env contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("fail-closed response must be the standard Error envelope: %v (%s)", err, w.Body.String())
	}
	if env.Code != contracts.ErrorCodeINTERNAL {
		t.Errorf("[P0] want INTERNAL error code on a fail-closed login, got %q", env.Code)
	}
}

// errContext is a tiny error constructor kept local so this file has no extra
// imports; it exists only to drive the failing-auditor branch.
func errContext(msg string) error { return &simpleErr{msg} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
