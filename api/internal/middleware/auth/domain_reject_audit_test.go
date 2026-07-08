// domain_reject_audit_test.go — Story 1.7 (AC: "when … an OAuth domain rejection
// occurs … an immutable audit row is written"). Middleware-level acceptance for
// the auth.domain_rejected emission in the auth middleware's EnforceDomain
// failure branch — the one place a domain-rejected request lives, since it is
// aborted before any handler runs. No DB / no build tag: it wires the REAL
// errormw + auth Middleware with a fake verifier + fake auditor and asserts the
// rendered status, reusing fakeVerifier/bearer from auth_test.go (same package).
//
// It proves the NEW behavior 1.7 adds on top of the already-proven 401/403
// contract:
//   - a VALID token whose email is off-domain → audited exactly once
//     (forwarding the verified email + device + reason) and still 403
//     AUTH_DOMAIN_REJECTED;
//   - an invalid / missing bearer → NOT audited (no verified identity, no
//     registered event) and still 401 AUTH_TOKEN_INVALID — no ledger flood;
//   - a domain rejection whose audit write fails → still a clean 403 (best-effort
//     audit, D3 ratified 2026-07-08: a ledger outage must not turn a rejection
//     into a 500; the missed row is logged/alertable instead).
//
// RED: mwauth.Middleware still takes (verifier, allowedDomain) — this file calls
// the intended 3-arg Middleware(verifier, allowedDomain, auditor) and references
// the not-yet-built mwauth Auditor interface, so package auth (middleware) is
// compile-red until Story 1.7 Task 3 lands (and auth_test.go / auth_http_test.go
// are updated to the new signature). Intended TDD red phase.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

// recordingDomainAuditor is the fake for the mwauth Auditor seam (the narrow
// domain-rejection view the middleware will depend on). err drives fail-closed.
type recordingDomainAuditor struct {
	calls                 int
	err                   error
	email, device, reason string
}

func (a *recordingDomainAuditor) DomainRejected(_ context.Context, email, device, reason string) error {
	a.calls++
	a.email, a.device, a.reason = email, device, reason
	return a.err
}

// runReject drives one request through errormw + Middleware(v, "amrita.edu",
// auditor) and reports the rendered response plus whether the protected handler
// ran (it must not, on any rejection).
func runReject(t *testing.T, v svcauth.TokenVerifier, authHeader, userAgent string, auditor *recordingDomainAuditor) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	handlerRan := false
	r := gin.New()
	r.Use(errormw.Handler())
	r.Use(Middleware(v, "amrita.edu", auditor))
	r.GET("/protected", func(c *gin.Context) {
		handlerRan = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	r.ServeHTTP(w, req)
	return w, handlerRan
}

func decodeRejectErr(t *testing.T, w *httptest.ResponseRecorder) contracts.Error {
	t.Helper()
	var env contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response must be the standard Error envelope: %v (%s)", err, w.Body.String())
	}
	return env
}

// TestDomainReject_AuditsAndStillReturns403 — [P0] a VALID token whose email is
// off the amrita.edu family is audited exactly once (forwarding the verified
// email + request device + rejection reason) and still rejected as 403
// AUTH_DOMAIN_REJECTED; the handler never runs.
func TestDomainReject_AuditsAndStillReturns403(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "Outsider@gmail.com", Subject: "sub-x", SessionID: "sess-x"}
	aud := &recordingDomainAuditor{}

	w, handlerRan := runReject(t, fakeVerifier{id: id}, bearer("valid-but-wrong-domain"), "Mozilla/5.0", aud)

	if handlerRan {
		t.Fatalf("[P0] a domain-rejected request must never reach the protected handler")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("[P0] a valid off-domain token must still be 403, got %d (%s)", w.Code, w.Body.String())
	}
	if env := decodeRejectErr(t, w); env.Code != contracts.ErrorCodeAUTHDOMAINREJECTED {
		t.Errorf("[P0] want AUTH_DOMAIN_REJECTED, got %q", env.Code)
	}
	if aud.calls != 1 {
		t.Fatalf("[P0] a domain rejection must be audited exactly once, got %d", aud.calls)
	}
	// The middleware forwards the VERIFIED email as-is (lowercasing is the
	// auditor's job, proven in the service unit test).
	if aud.email != id.Email {
		t.Errorf("[P1] auditor must receive the verified email, got %q", aud.email)
	}
	if aud.device != "Mozilla/5.0" {
		t.Errorf("[P1] auditor must receive the request device, got %q", aud.device)
	}
	if aud.reason == "" {
		t.Errorf("[P1] auditor must receive a non-empty rejection reason")
	}
}

// TestDomainReject_InvalidTokenNotAudited — [P0] an invalid token (verify fails)
// yields 401 AUTH_TOKEN_INVALID and is NOT audited: there is no verified
// identity and no registered event, and auditing it would flood the ledger with
// the 1.6 silent-refresh happy path.
func TestDomainReject_InvalidTokenNotAudited(t *testing.T) {
	verr := domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "token invalid"}
	aud := &recordingDomainAuditor{}

	w, handlerRan := runReject(t, fakeVerifier{err: verr}, bearer("bad-token"), "Mozilla/5.0", aud)

	if handlerRan {
		t.Fatalf("[P0] an invalid-token request must never reach the handler")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("[P0] an invalid token must be 401, got %d (%s)", w.Code, w.Body.String())
	}
	if aud.calls != 0 {
		t.Fatalf("[P0] an invalid/expired token must NOT be audited (no verified identity, no registered event), got %d calls", aud.calls)
	}
}

// TestDomainReject_MissingBearerNotAudited — [P1] a missing Authorization header
// is 401 and never audited (the verifier is not even consulted).
func TestDomainReject_MissingBearerNotAudited(t *testing.T) {
	aud := &recordingDomainAuditor{}

	w, _ := runReject(t, fakeVerifier{}, "", "Mozilla/5.0", aud)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("[P1] a missing bearer must be 401, got %d", w.Code)
	}
	if aud.calls != 0 {
		t.Errorf("[P1] a missing bearer must not be audited, got %d calls", aud.calls)
	}
}

// TestDomainReject_AuditFailureIsBestEffort — [P1] D3 (ratified 2026-07-08): a
// domain rejection whose audit write fails is STILL enforced as a clean 403
// AUTH_DOMAIN_REJECTED — a ledger outage must not convert a rejection into a 500.
// The middleware still attempts the audit (so the miss is logged/alertable), but
// the failure never changes the response.
func TestDomainReject_AuditFailureIsBestEffort(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "outsider@gmail.com", Subject: "sub-x"}
	aud := &recordingDomainAuditor{err: errors.New("audit ledger unavailable")}

	w, handlerRan := runReject(t, fakeVerifier{id: id}, bearer("valid-but-wrong-domain"), "Mozilla/5.0", aud)

	if handlerRan {
		t.Fatalf("[P1] the handler must never run on a rejection")
	}
	if aud.calls != 1 {
		t.Fatalf("[P1] the middleware must still attempt the rejection audit, got %d calls", aud.calls)
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("[P1] a rejection whose audit write fails must still be a clean 403, got %d (%s)", w.Code, w.Body.String())
	}
	if env := decodeRejectErr(t, w); env.Code != contracts.ErrorCodeAUTHDOMAINREJECTED {
		t.Errorf("[P1] want AUTH_DOMAIN_REJECTED on a best-effort rejection, got %q", env.Code)
	}
}
