package auth

import (
	"context"
	"errors"
	"net/http"

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

// MeHandler returns the identity verified by the auth middleware, provisioning
// the application user + role on the way (Story 1.5b). Provisioning runs HERE —
// the /auth/me login round-trip — not in the per-request auth middleware, so an
// authenticated call never carries a DB write-path. It is idempotent: a
// returning identity is a no-op (no audit row). The provisioned role is echoed
// so the mobile client can route by role.
func MeHandler(provisioner Provisioner) gin.HandlerFunc {
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
		// field, since provisioning always yields exactly one.
		resp := contracts.AuthenticatedIdentity{
			Email:   user.Email,
			Subject: user.SupabaseSubject,
			Role:    contracts.AuthenticatedIdentityRole(user.Role),
		}
		// name is optional in the contract — emit it only when the provider gave one.
		if user.Name != "" {
			resp.Name = &user.Name
		}
		c.JSON(http.StatusOK, resp)
	}
}
