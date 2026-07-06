package auth

// revocation_guard.go — the next-request forced-logout enforcement (Story 1.5c,
// DoD 6). It runs IMMEDIATELY AFTER Middleware (which verifies the token, enforces
// the domain, and sets the identity) and BEFORE any protected handler, so a token
// that is still cryptographically valid but whose session has been revoked is
// rejected with 401 on its very next request — not only at token expiry.
//
// It is a SEPARATE middleware (not folded into Middleware) so the existing
// verify+domain wiring and its tests stay untouched; cmd/api mounts it right after
// Middleware on the /api/v1 group.

import (
	"errors"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

// RevocationGuard rejects requests whose session has been revoked. It consults
// the checker (the local deny-list in production) using the identity Middleware
// set. A revoked token is 401 AUTH_TOKEN_INVALID; a check FAILURE fails closed as
// a centralized 500 (INTERNAL) rather than letting the request through.
func RevocationGuard(checker svcauth.RevocationChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := IdentityFromContext(c)
		if !ok {
			// The guard was mounted without the auth middleware ahead of it — fail
			// closed (generic 500) rather than waving the request through.
			_ = c.Error(errors.New("auth: identity missing before revocation guard"))
			c.Abort()
			return
		}
		revoked, err := checker.IsRevoked(c.Request.Context(), id)
		if err != nil {
			// A deny-list check failure (e.g. DB down) must NOT fall through to the
			// handler. Push the raw error → errormw renders INTERNAL 500 (fail closed).
			_ = c.Error(err)
			c.Abort()
			return
		}
		if revoked {
			_ = c.Error(domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid."})
			c.Abort()
			return
		}
		c.Next()
	}
}
