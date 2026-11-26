package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
)

// Provisioner maps the verified identity to an application user + role on first
// login. *user.Service satisfies it; the handler keeps only this narrow view so
// it depends on the behavior, not the concrete service.
type Provisioner interface {
	Provision(ctx context.Context, id svcauth.VerifiedIdentity) (svcuser.User, error)
}

// Auditor writes the auth.login_succeeded audit event. The handler
// keeps only this narrow login-success view of *service/auth.EventAuditor so the
// login round-trip depends on the behavior, not the concrete service.
type Auditor interface {
	LoginSucceeded(ctx context.Context, email, role, subject, sessionID, device string) error
}

// MeHandler returns the identity verified by the auth middleware, provisioning
// the application user + role on the way. Provisioning runs HERE —
// the /auth/me login round-trip — not in the per-request auth middleware, so an
// authenticated call never carries a DB write-path. Provisioning itself is
// idempotent: a returning identity writes no user.provisioned row, and since the
// cost work it no longer writes AT ALL unless something actually changed (see
// service/user.Service.WithCurrentReader).
//
// The auth.login_succeeded audit used to fire on EVERY successful call. It is
// now throttled to one row per Supabase session per window — this endpoint is
// called on every token refresh, not once per sign-in, and the duplicate rows
// were 79% of the entire audit ledger. See
// service/auth.ThrottledLoginAuditor for what that preserves and what it gives
// up; the wiring lives in cmd/api/main.go. This handler is unchanged in shape:
// it still fails closed if the audit write it is given returns an error.
//
// The provisioned role is echoed so the mobile client can route by role.
func MeHandler(provisioner Provisioner, auditor Auditor) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The auth middleware always sets the identity ahead of this handler; a
		// missing one means the handler was mounted without it — fail closed
		// (the error middleware renders a generic 500) rather than returning 200
		// with a blank identity.
		id, ok := mwauth.IdentityFromContext(c)
		if !ok {
			_ = c.Error(errors.New("auth: identity missing from context"))
			c.Abort()
			return
		}

		user, err := provisioner.Provision(c.Request.Context(), id)
		if err != nil {
			// Never hand-write an error body — push it to the centralized error
			// middleware, which renders the standard Error envelope (e.g. a DB
			// outage surfaces as INTERNAL 500).
			_ = c.Error(err)
			c.Abort()
			return
		}

		// Emit from the provisioned user: the stored email is lowercased and the
		// role is the just-resolved (or existing) global base role — a required
		// field, since provisioning always yields exactly one. Gate the cast on
		// .Valid() so a DB role outside the contract enum (e.g. the CHECK set drifts
		// ahead of the OpenAPI enum) fails loudly as a 500 rather than serializing
		// an out-of-contract value.
		role := contracts.AuthenticatedIdentityRole(user.Role)
		if !role.Valid() {
			_ = c.Error(fmt.Errorf("auth: provisioned role %q is not a valid contract role", user.Role))
			c.Abort()
			return
		}
		// Audit the login BEFORE responding 200. Fail-closed: a login
		// whose audit write fails must not "complete" as a success — push the error
		// to the centralized middleware (rendered INTERNAL 500), never 200. Fires on
		// every successful /auth/me (per-call, not per-session; session_id is in the
		// payload for later dedup). device = the request User-Agent (IP is deferred
		// until a SetTrustedProxies configuration exists).
		device := strings.TrimSpace(c.Request.UserAgent())
		if err := auditor.LoginSucceeded(c.Request.Context(), user.Email, string(user.Role), id.Subject, id.SessionID, device); err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		resp := contracts.AuthenticatedIdentity{
			Email:   user.Email,
			Subject: user.SupabaseSubject,
			Role:    role,
		}
		// name is optional in the contract — emit it only when the provider gave one.
		if user.Name != "" {
			resp.Name = &user.Name
		}
		c.JSON(http.StatusOK, resp)
	}
}
