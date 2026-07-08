// me_test.go — red-phase ATDD for Story 1.5a DoD 4: the GET /api/v1/auth/me
// handler. It reads the identity the auth middleware stashed in the gin context
// and returns 200 with the GENERATED contracts.AuthenticatedIdentity
// { email, name, subject } — the round-trip prover and future home of
// first-login provisioning (1.5b).
//
// RED: handler/auth is a Story 1.1 doc stub — MeHandler does not exist; it reads
// via middleware/auth.SetIdentity/IdentityFromContext (unbuilt) and returns the
// generated AuthenticatedIdentity type, which the contract does not define until
// Task 2 regenerates it -> compile-red until Tasks 2/6.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
)

// echoProvisioner reflects the verified identity into a provisioned User
// (default role student), isolating the handler from the provisioning DB write
// while still exercising provision-then-return (Story 1.5b).
type echoProvisioner struct{}

func (echoProvisioner) Provision(_ context.Context, id svcauth.VerifiedIdentity) (svcuser.User, error) {
	return svcuser.User{
		SupabaseSubject: id.Subject,
		Email:           id.Email,
		Name:            id.Name,
		Role:            svcuser.RoleStudent,
	}, nil
}

// noopAuditor is a login-success Auditor that records nothing (Story 1.7 wired the
// dependency into MeHandler; these 1.5a/1.5b handler tests only care that the
// login-success audit does not disturb the identity round-trip). The dedicated
// audit assertions live in test/auth/login_audit_test.go.
type noopAuditor struct{}

func (noopAuditor) LoginSucceeded(_ context.Context, _, _, _, _, _ string) error { return nil }

// serveMe seeds the context with `seed` (as the auth middleware would) via the
// exported SetIdentity, then invokes MeHandler — isolating the handler from the
// verify/domain path.
func serveMe(t *testing.T, seed *svcauth.VerifiedIdentity) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if seed != nil {
		r.Use(func(c *gin.Context) {
			mwauth.SetIdentity(c, *seed)
			c.Next()
		})
	}
	r.GET("/api/v1/auth/me", MeHandler(echoProvisioner{}, noopAuditor{}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	return w
}

// TestMe_Returns200Identity — the seeded identity round-trips through the
// generated AuthenticatedIdentity contract type.
func TestMe_Returns200Identity(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "staff@amrita.edu", Name: "Staff Member", Subject: "user-uuid-123"}
	w := serveMe(t, &id)

	if w.Code != http.StatusOK {
		t.Fatalf("[P0] want 200 for an authenticated identity, got %d (%s)", w.Code, w.Body.String())
	}
	var got contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("[P0] body must be the generated AuthenticatedIdentity: %v (%s)", err, w.Body.String())
	}
	if got.Email != id.Email || got.Name == nil || *got.Name != id.Name || got.Subject != id.Subject {
		t.Errorf("[P0] identity round-trip mismatch: want %+v, got %+v", id, got)
	}
}

// TestMe_MissingIdentity_500 — if the handler is ever reached without the auth
// middleware seeding an identity, it fails closed (generic 500 via the error
// middleware), never a 200 with a blank identity.
func TestMe_MissingIdentity_500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errormw.Handler())
	r.GET("/api/v1/auth/me", MeHandler(echoProvisioner{}, noopAuditor{})) // no identity seeded

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("[P0] want 500 when identity is missing, got %d (%s)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "\"email\"") {
		t.Errorf("[P0] a fail-closed 500 must not carry an identity payload: %s", w.Body.String())
	}
}

// TestMe_WireShape — the response is exactly { email, name, subject, role } and
// nothing else (the generated contract shape after Story 1.5b added the required
// role), mirroring the health handler's wire-shape guard.
func TestMe_WireShape(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "staff@amrita.edu", Name: "Staff Member", Subject: "user-uuid-123"}
	w := serveMe(t, &id)

	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 4 {
		t.Errorf("[P2] AuthenticatedIdentity carries exactly {email, name, subject, role}, got keys: %v", raw)
	}
	for _, k := range []string{"email", "name", "subject", "role"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("[P2] missing required contract field %q", k)
		}
	}
}
