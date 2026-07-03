package errormw

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// Handler is the single renderer for domain errors. Upstream code should push
// errors with c.Error and abort, not hand-write error bodies.
func Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		err := c.Errors.Last().Err
		var de domain.DomainError
		if errors.As(err, &de) {
			c.AbortWithStatusJSON(domain.HTTPStatus(de.Code), contracts.Error{Code: de.Code, Message: de.Message})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, contracts.Error{
			Code:    contracts.ErrorCodeINTERNAL,
			Message: "Internal server error.",
		})
	}
}
