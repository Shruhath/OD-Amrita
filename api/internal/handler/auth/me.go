package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
)

// MeHandler returns the identity verified by the auth middleware.
func MeHandler() gin.HandlerFunc {
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
		resp := contracts.AuthenticatedIdentity{Email: id.Email, Subject: id.Subject}
		// name is optional in the contract — emit it only when the provider gave one.
		if id.Name != "" {
			resp.Name = &id.Name
		}
		c.JSON(http.StatusOK, resp)
	}
}
