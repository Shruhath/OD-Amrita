// revocation_mw_test.go — Story 1.5c (DoD 6): backend next-request enforcement.
// The revocation guard runs AFTER token signature/audience/issuer + domain
// enforcement and BEFORE the identity is used downstream, so a token that is
// still cryptographically valid but revoked is rejected with 401
// AUTH_TOKEN_INVALID on its very next request — not only at expiry. Composed
// in-process (errormw + auth middleware + guard + handler) with fakes; no DB.
//
// It REUSES the helpers from auth_http_test.go: fakeVerifier, decodeError.
package authhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

// fakeChecker is the revocation-decision seam. In production it consults the
// local deny-list (sqlc) by sub/session_id/iat; here it returns a fixed verdict.
type fakeChecker struct {
	revoked bool
	err     error
	calls   int
}

func (f *fakeChecker) IsRevoked(ctx context.Context, id svcauth.VerifiedIdentity) (bool, error) {
	f.calls++
	return f.revoked, f.err
}

// guardRouter mirrors the intended cmd/api wiring order: errormw (outermost) ->
// auth middleware (verify + domain) -> revocation guard -> protected handler.
// The sentinel handler returns 200 and flips reached, so a test can prove the
// guard blocked (reached stays false, 401) vs. let through (reached true, 200).
func guardRouter(v svcauth.TokenVerifier, checker *fakeChecker, reached *bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.Use(errormw.Handler())
	grp.Use(mwauth.Middleware(v, "amrita.edu", okAuditor{}))
	grp.Use(mwauth.RevocationGuard(checker))
	grp.GET("/protected", func(c *gin.Context) {
		*reached = true
		c.Status(http.StatusOK)
	})
	return r
}

func callProtected(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	r.ServeHTTP(w, req)
	return w
}

// TestRevocationGuard_RevokedTokenIs401 — a VALID @amrita.edu identity whose
// session is revoked is rejected 401 AUTH_TOKEN_INVALID before the handler runs.
// This is the core next-request guarantee: enforcement does not wait for expiry.
func TestRevocationGuard_RevokedTokenIs401(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "staff@amrita.edu", Subject: "sub-1", SessionID: "sess-1"}
	reached := false
	checker := &fakeChecker{revoked: true}

	w := callProtected(t, guardRouter(fakeVerifier{id: id}, checker, &reached))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("[P0] a revoked token must be 401, got %d (%s)", w.Code, w.Body.String())
	}
	if env := decodeError(t, w); env.Code != contracts.ErrorCodeAUTHTOKENINVALID {
		t.Errorf("[P0] want AUTH_TOKEN_INVALID, got %q", string(env.Code))
	}
	if reached {
		t.Error("[P0] the protected handler must NOT run for a revoked token")
	}
	if checker.calls != 1 {
		t.Errorf("[P0] the guard must consult the revocation checker exactly once, got %d", checker.calls)
	}
}

// TestRevocationGuard_LiveTokenReachesHandler — a valid, non-revoked identity
// passes the guard and the downstream handler runs.
func TestRevocationGuard_LiveTokenReachesHandler(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "staff@amrita.edu", Subject: "sub-2", SessionID: "sess-2"}
	reached := false
	checker := &fakeChecker{revoked: false}

	w := callProtected(t, guardRouter(fakeVerifier{id: id}, checker, &reached))

	if w.Code != http.StatusOK {
		t.Fatalf("[P0] a live (non-revoked) token must reach the handler (200), got %d (%s)", w.Code, w.Body.String())
	}
	if !reached {
		t.Error("[P0] the protected handler must run for a non-revoked token")
	}
}

// TestRevocationGuard_CheckerErrorFailsClosed — a revocation-check failure (e.g.
// DB down) must fail closed as INTERNAL 500, never fall through to the handler.
func TestRevocationGuard_CheckerErrorFailsClosed(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "staff@amrita.edu", Subject: "sub-3", SessionID: "sess-3"}
	reached := false
	checker := &fakeChecker{err: context.DeadlineExceeded}

	w := callProtected(t, guardRouter(fakeVerifier{id: id}, checker, &reached))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("[P1] a revocation-check failure must fail closed (500), got %d (%s)", w.Code, w.Body.String())
	}
	if reached {
		t.Error("[P1] the protected handler must NOT run when the revocation check errors")
	}
}
