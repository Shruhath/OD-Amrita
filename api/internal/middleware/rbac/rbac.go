package rbac

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
)

const (
	principalKey = "rbac.principal"
	RoleAdmin    = "admin"
)

var ErrPrincipalNotFound = errors.New("rbac: principal not found")

// Principal contains only server-owned authorization data loaded from users.
type Principal struct {
	UserID  string
	Subject string
	Email   string
	Name    string
	Role    string
}

type PrincipalLoader interface {
	Load(context.Context, string) (Principal, error)
}

func RequireAdmin(loader PrincipalLoader) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := mwauth.IdentityFromContext(c)
		if !ok {
			_ = c.Error(errors.New("rbac: verified identity missing from context"))
			c.Abort()
			return
		}

		principal, err := loader.Load(c.Request.Context(), identity.Subject)
		if err != nil {
			if errors.Is(err, ErrPrincipalNotFound) {
				forbidden(c)
				return
			}
			_ = c.Error(err)
			c.Abort()
			return
		}
		if principal.Role != RoleAdmin {
			forbidden(c)
			return
		}

		c.Set(principalKey, principal)
		c.Next()
	}
}

func forbidden(c *gin.Context) {
	_ = c.Error(domain.DomainError{
		Code:    contracts.ErrorCodeRBACFORBIDDEN,
		Message: "Administrator access is required.",
	})
	c.Abort()
}

func PrincipalFromContext(c *gin.Context) (Principal, bool) {
	raw, ok := c.Get(principalKey)
	if !ok {
		return Principal{}, false
	}
	principal, ok := raw.(Principal)
	return principal, ok
}
