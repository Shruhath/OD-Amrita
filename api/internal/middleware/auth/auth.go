package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

const identityKey = "auth.identity"

// Middleware verifies the bearer token, enforces the Amrita domain family, and
// stashes the verified identity for handlers.
func Middleware(verifier svcauth.TokenVerifier, allowedDomain string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			_ = c.Error(domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid."})
			c.Abort()
			return
		}
		id, err := verifier.Verify(c.Request.Context(), token)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		if err := svcauth.EnforceDomain(id.Email, allowedDomain); err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		SetIdentity(c, id)
		c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func SetIdentity(c *gin.Context, id svcauth.VerifiedIdentity) {
	c.Set(identityKey, id)
}

func IdentityFromContext(c *gin.Context) (svcauth.VerifiedIdentity, bool) {
	raw, ok := c.Get(identityKey)
	if !ok {
		return svcauth.VerifiedIdentity{}, false
	}
	id, ok := raw.(svcauth.VerifiedIdentity)
	return id, ok
}
