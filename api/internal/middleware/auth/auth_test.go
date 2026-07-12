// auth_test.go — red-phase ATDD for Story 1.5a DoD 2/3: the gin auth
// middleware. Tests the middleware's OWN contract in isolation (fake verifier,
// no errormw, no HTTP-body rendering): on each failure it must abort BEFORE the
// handler and push a typed domain.DomainError with the right code; on success
// it must stash the verified identity in the gin context and call Next. The
// rendered-envelope side (401/403 bodies) is proven separately by errormw_test
// and the composed round-trip in api/test/auth.
//
// RED: middleware/auth is a Story 1.1 doc stub — Middleware, IdentityFromContext
// do not exist yet, and it calls into service/auth (EnforceDomain, TokenVerifier,
// VerifiedIdentity), also unbuilt -> compile-red until Tasks 4-6.
package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

// fakeVerifier lets the middleware tests drive the verify outcome without a
// JWKS server (the real verifier is proven in service/auth).
type fakeVerifier struct {
	id  svcauth.VerifiedIdentity
	err error
}

func (f fakeVerifier) Verify(context.Context, string) (svcauth.VerifiedIdentity, error) {
	return f.id, f.err
}

type outcome struct {
	aborted     bool
	pushedErr   error
	identity    svcauth.VerifiedIdentity
	identityOK  bool
	terminalRan bool
}

// run drives one request through Middleware and captures the middleware's
// effect. A leading probe calls Next() then reads the post-chain state, so it
// sees c.Errors / c.IsAborted even when the auth middleware aborts downstream.
func run(t *testing.T, v svcauth.TokenVerifier, authHeader string) outcome {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var out outcome
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Next()
		out.aborted = c.IsAborted()
		if len(c.Errors) > 0 {
			out.pushedErr = c.Errors.Last().Err
		}
		out.identity, out.identityOK = IdentityFromContext(c)
	})
	r.Use(Middleware(v, "amrita.edu", &recordingDomainAuditor{}))
	r.GET("/me", func(c *gin.Context) {
		out.terminalRan = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return out
}

func bearer(tok string) string { return "Bearer " + tok }

func assertRejected(t *testing.T, o outcome, wantCode contracts.ErrorCode) {
	t.Helper()
	if !o.aborted || o.terminalRan {
		t.Fatalf("[P0] request must abort before the handler (aborted=%v terminalRan=%v)", o.aborted, o.terminalRan)
	}
	if o.pushedErr == nil {
		t.Fatalf("[P0] expected a pushed DomainError with code %s, got none", wantCode)
	}
	var de domain.DomainError
	if !errors.As(o.pushedErr, &de) {
		t.Fatalf("[P0] pushed error must be a domain.DomainError, got %T", o.pushedErr)
	}
	if de.Code != wantCode {
		t.Errorf("[P0] want code %s, got %s", wantCode, de.Code)
	}
}

// TestAuth_Success — a valid bearer for an @amrita.edu identity does not abort,
// runs the handler, and leaves the verified identity in the gin context.
func TestAuth_Success(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "staff@amrita.edu", Name: "Staff", Subject: "sub-1"}
	o := run(t, fakeVerifier{id: id}, bearer("any-verifiable-token"))

	if o.aborted {
		t.Fatalf("[P0] a valid @amrita.edu request must not abort; pushed=%v", o.pushedErr)
	}
	if !o.terminalRan {
		t.Error("[P0] the downstream handler must run on success")
	}
	if !o.identityOK || o.identity != id {
		t.Errorf("[P0] verified identity must be stashed in context; ok=%v id=%+v", o.identityOK, o.identity)
	}
}

// TestAuth_MissingBearer — no Authorization header -> 401 AUTH_TOKEN_INVALID
// (the verifier is never even consulted).
func TestAuth_MissingBearer(t *testing.T) {
	assertRejected(t, run(t, fakeVerifier{}, ""), contracts.ErrorCodeAUTHTOKENINVALID)
}

// TestAuth_MalformedHeader — a header that is not "Bearer <token>" -> 401
// AUTH_TOKEN_INVALID.
func TestAuth_MalformedHeader(t *testing.T) {
	assertRejected(t, run(t, fakeVerifier{}, "Token abc"), contracts.ErrorCodeAUTHTOKENINVALID)
}

// TestAuth_VerifyFails — the verifier rejects the token -> 401
// AUTH_TOKEN_INVALID (the DomainError the verifier produced flows through).
func TestAuth_VerifyFails(t *testing.T) {
	verr := domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "token invalid"}
	assertRejected(t, run(t, fakeVerifier{err: verr}, bearer("bad")), contracts.ErrorCodeAUTHTOKENINVALID)
}

// TestAuth_DomainRejected — a token that verifies but whose email is NOT in the
// amrita.edu family -> 403 AUTH_DOMAIN_REJECTED (identity authentic, not
// permitted). Never collapsed into the 401.
func TestAuth_DomainRejected(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "outsider@gmail.com", Name: "Out", Subject: "sub-2"}
	assertRejected(t, run(t, fakeVerifier{id: id}, bearer("valid-but-wrong-domain")), contracts.ErrorCodeAUTHDOMAINREJECTED)
}
