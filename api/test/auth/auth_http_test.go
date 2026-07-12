// Package authhttp — red-phase ATDD for Story 1.5a: the composed round-trip AC
// table. It wires the REAL error-middleware + auth middleware + MeHandler into
// the /api/v1 group exactly as cmd/api/main.go will, driving it in-process with
// a fake verifier (no DB, no live Supabase, no build tag — runs under the
// default `go test ./...`). This is the per-AC proof that the units compose
// into the endpoint contract:
//
//	valid @amrita.edu -> 200 AuthenticatedIdentity
//	bad/expired token -> 401 AUTH_TOKEN_INVALID
//	non-@amrita.edu    -> 403 AUTH_DOMAIN_REJECTED + exact message (NOT a 401)
//	missing bearer     -> 401 AUTH_TOKEN_INVALID
//
// RED: it imports the still-unbuilt handler/auth, middleware/{auth,error},
// service/auth and the not-yet-generated contracts.AuthenticatedIdentity /
// AUTH_TOKEN_INVALID -> compile-red until Tasks 2/4/5/6 land.
package authhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
)

// echoProvisioner is the 1.5b provisioning seam for these 1.5a round-trip tests:
// it reflects the verified identity into a provisioned User (default role
// student) so the composed endpoint still returns the identity claims. The real
// provisioning DB write is covered by the service unit + DB integration suites.
type echoProvisioner struct{}

func (echoProvisioner) Provision(_ context.Context, id svcauth.VerifiedIdentity) (svcuser.User, error) {
	return svcuser.User{
		SupabaseSubject: id.Subject,
		Email:           id.Email,
		Name:            id.Name,
		Role:            svcuser.RoleStudent,
	}, nil
}

const rejectMessage = "Only @amrita.edu accounts can access this system."

type fakeVerifier struct {
	id  svcauth.VerifiedIdentity
	err error
}

func (f fakeVerifier) Verify(context.Context, string) (svcauth.VerifiedIdentity, error) {
	return f.id, f.err
}

// okAuditor is a succeed-always auth-event auditor for the composed round-trip +
// revocation-guard tests: it satisfies BOTH the handler's login-success Auditor
// and the middleware's domain-rejection Auditor so those suites can wire the real
// middleware/handler without asserting audit behavior (the audit assertions live
// in login_audit_test.go and domain_reject_audit_test.go).
type okAuditor struct{}

func (okAuditor) LoginSucceeded(_ context.Context, _, _, _, _, _ string) error { return nil }
func (okAuditor) DomainRejected(_ context.Context, _, _, _ string) error       { return nil }

// router mirrors the intended cmd/api/main.go wiring: /api/v1 group carrying the
// error-middleware (outermost, single envelope renderer) then the auth
// middleware, with GET /auth/me behind them. /health stays out of this group.
func router(v svcauth.TokenVerifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.Use(errormw.Handler())
	grp.Use(mwauth.Middleware(v, "amrita.edu", okAuditor{}))
	grp.GET("/auth/me", handlerauth.MeHandler(echoProvisioner{}, okAuditor{}))
	return r
}

func do(t *testing.T, r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	r.ServeHTTP(w, req)
	return w
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) contracts.Error {
	t.Helper()
	var env contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("body must be the Error envelope: %v (%s)", err, w.Body.String())
	}
	return env
}

// TestAuthRoundTrip_ValidAmritaIdentity200 — the happy path end to end.
func TestAuthRoundTrip_ValidAmritaIdentity200(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "student@cb.students.amrita.edu", Name: "A Student", Subject: "sub-9"}
	w := do(t, router(fakeVerifier{id: id}), "Bearer good-token")

	if w.Code != http.StatusOK {
		t.Fatalf("[P0] valid @amrita.edu login must be 200, got %d (%s)", w.Code, w.Body.String())
	}
	var got contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("[P0] 200 body must be AuthenticatedIdentity: %v", err)
	}
	if got.Email != id.Email || got.Name == nil || *got.Name != id.Name || got.Subject != id.Subject {
		t.Errorf("[P0] identity mismatch: want %+v, got %+v", id, got)
	}
}

// TestAuthRoundTrip_BadToken401 — a verifier rejection surfaces as a clean 401
// AUTH_TOKEN_INVALID envelope.
func TestAuthRoundTrip_BadToken401(t *testing.T) {
	verr := domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "token invalid"}
	w := do(t, router(fakeVerifier{err: verr}), "Bearer bad-token")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("[P0] a bad token must be 401, got %d", w.Code)
	}
	if env := decodeError(t, w); env.Code != contracts.ErrorCodeAUTHTOKENINVALID {
		t.Errorf("[P0] want AUTH_TOKEN_INVALID, got %q", string(env.Code))
	}
}

// TestAuthRoundTrip_NonAmrita403 — a verified-but-not-permitted identity is a
// 403 AUTH_DOMAIN_REJECTED with the EXACT message (never a generic 401).
func TestAuthRoundTrip_NonAmrita403(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "outsider@gmail.com", Name: "Out", Subject: "sub-x"}
	w := do(t, router(fakeVerifier{id: id}), "Bearer valid-but-wrong-domain")

	if w.Code != http.StatusForbidden {
		t.Fatalf("[P0] a non-amrita identity must be 403, got %d", w.Code)
	}
	env := decodeError(t, w)
	if env.Code != contracts.ErrorCodeAUTHDOMAINREJECTED {
		t.Errorf("[P0] want AUTH_DOMAIN_REJECTED, got %q", string(env.Code))
	}
	if env.Message != rejectMessage {
		t.Errorf("[P0] exact AC message required.\n want: %q\n  got: %q", rejectMessage, env.Message)
	}
}

// TestAuthRoundTrip_MissingBearer401 — no credentials -> 401 AUTH_TOKEN_INVALID.
func TestAuthRoundTrip_MissingBearer401(t *testing.T) {
	w := do(t, router(fakeVerifier{}), "")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("[P0] a missing bearer must be 401, got %d", w.Code)
	}
	if env := decodeError(t, w); env.Code != contracts.ErrorCodeAUTHTOKENINVALID {
		t.Errorf("[P0] want AUTH_TOKEN_INVALID, got %q", string(env.Code))
	}
}
