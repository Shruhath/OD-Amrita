// provisioning_me_test.go — Story 1.5b acceptance for the /auth/me provisioning
// round-trip: the handler provisions the verified identity exactly once at
// login, echoes the provisioned role, and renders provisioning failures through
// the centralized error middleware. No DB / no build tag — runs under the
// default `go test ./...` with a fake provisioner.
package authhttp

import (
	"context"
	"encoding/json"
	"errors"
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

type recordingProvisioner struct {
	user  svcuser.User
	err   error
	calls int
	seen  []svcauth.VerifiedIdentity
}

func (p *recordingProvisioner) Provision(ctx context.Context, id svcauth.VerifiedIdentity) (svcuser.User, error) {
	p.calls++
	p.seen = append(p.seen, id)
	if p.err != nil {
		return svcuser.User{}, p.err
	}
	return p.user, nil
}

func provisionRouter(seed svcauth.VerifiedIdentity, p *recordingProvisioner) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errormw.Handler())
	r.Use(func(c *gin.Context) {
		mwauth.SetIdentity(c, seed)
		c.Next()
	})
	r.GET("/api/v1/auth/me", handlerauth.MeHandler(p))
	return r
}

func doProvisionMe(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	return w
}

func TestAuthMe_ProvisionsOnLoginRoundTripAndReturnsRole(t *testing.T) {
	id := svcauth.VerifiedIdentity{
		Email:   "Student@CB.Students.Amrita.edu",
		Name:    "Student Name",
		Subject: "supabase-sub-1",
	}
	p := &recordingProvisioner{user: svcuser.User{
		ID:              "user-id-1",
		SupabaseSubject: id.Subject,
		Email:           "student@cb.students.amrita.edu",
		Name:            id.Name,
		Role:            svcuser.RoleStudent,
	}}

	w := doProvisionMe(t, provisionRouter(id, p))

	if w.Code != http.StatusOK {
		t.Fatalf("/auth/me should return 200 after provisioning, got %d (%s)", w.Code, w.Body.String())
	}
	if p.calls != 1 {
		t.Fatalf("/auth/me must call provisioning exactly once, got %d", p.calls)
	}
	if p.seen[0] != id {
		t.Fatalf("provisioning must receive the verified middleware identity\nwant: %+v\n got: %+v", id, p.seen[0])
	}

	var got contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response must decode as AuthenticatedIdentity: %v (%s)", err, w.Body.String())
	}
	if got.Email != "student@cb.students.amrita.edu" {
		t.Errorf("response email should come from provisioned user and be lowercased, got %q", got.Email)
	}
	if got.Subject != id.Subject {
		t.Errorf("response subject should remain Supabase sub, got %q", got.Subject)
	}
	if got.Role != contracts.AuthenticatedIdentityRoleStudent {
		t.Errorf("response role must be required student enum, got %q", got.Role)
	}
}

func TestAuthMe_ReturningBootstrapAdminReturnsAdminRole(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "admin@amrita.edu", Name: "Admin", Subject: "sub-admin"}
	p := &recordingProvisioner{user: svcuser.User{
		ID:              "admin-id",
		SupabaseSubject: id.Subject,
		Email:           id.Email,
		Name:            id.Name,
		Role:            svcuser.RoleAdmin,
	}}

	w := doProvisionMe(t, provisionRouter(id, p))

	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap admin /auth/me should return 200, got %d (%s)", w.Code, w.Body.String())
	}
	var got contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role != contracts.AuthenticatedIdentityRoleAdmin {
		t.Errorf("bootstrap admin should surface role=admin, got %q", got.Role)
	}
}

func TestAuthMe_ProvisioningErrorUsesCentralErrorMiddleware(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "student@amrita.edu", Subject: "sub-err"}
	p := &recordingProvisioner{err: errors.New("database unavailable")}

	w := doProvisionMe(t, provisionRouter(id, p))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("provisioning failure should render centralized 500, got %d (%s)", w.Code, w.Body.String())
	}
	var env contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("error response must be standard Error envelope: %v (%s)", err, w.Body.String())
	}
	if env.Code != contracts.ErrorCodeINTERNAL {
		t.Errorf("want INTERNAL error code from errormw, got %q", env.Code)
	}
}

func TestAuthMe_WireShapeIncludesRoleExactlyOnce(t *testing.T) {
	id := svcauth.VerifiedIdentity{Email: "student@amrita.edu", Subject: "sub-shape"}
	p := &recordingProvisioner{user: svcuser.User{
		ID:              "user-id",
		SupabaseSubject: id.Subject,
		Email:           id.Email,
		Role:            svcuser.RoleStudent,
	}}

	w := doProvisionMe(t, provisionRouter(id, p))

	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	wantKeys := map[string]bool{"email": true, "subject": true, "role": true}
	if len(raw) != len(wantKeys) {
		t.Fatalf("AuthenticatedIdentity must carry exactly email, subject, role when name is absent; got keys %v", raw)
	}
	for k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing required field %q", k)
		}
	}
}
