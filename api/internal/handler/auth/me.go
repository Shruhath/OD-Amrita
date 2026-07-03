package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
)

// MeHandler returns the identity verified by the auth middleware.
func MeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := mwauth.IdentityFromContext(c)
		c.JSON(http.StatusOK, contracts.AuthenticatedIdentity{
			Email:   id.Email,
			Name:    id.Name,
			Subject: id.Subject,
		})
	}
}
