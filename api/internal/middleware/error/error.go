package errormw

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	requestid "github.com/Shruhath/OD-Amrita/api/internal/middleware/request_id"
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
			if de.Code == contracts.ErrorCodeVALIDATIONFAILED && de.Details != nil {
				c.AbortWithStatusJSON(domain.HTTPStatus(de.Code), contracts.ValidationError{
					Code:    contracts.ValidationErrorCodeVALIDATIONFAILED,
					Message: de.Message,
					Details: de.Details,
				})
				return
			}
			c.AbortWithStatusJSON(domain.HTTPStatus(de.Code), contracts.Error{Code: de.Code, Message: de.Message})
			return
		}
		// THE ONLY PLACE AN UNMAPPED ERROR IS EVER SEEN.
		//
		// Everything above this line is a deliberate answer the caller is meant
		// to read. This branch is the opposite: something the code did not
		// anticipate, rendered as a body that says nothing on purpose. Until
		// this log existed, that body was ALSO all the operator got — the
		// router installs no gin.Logger, so a 500 left no trace anywhere, and
		// diagnosing one meant reconstructing it from the database afterwards.
		//
		// Logged: method, path and the request id, which is what ties a line here
		// to the failure a user is reporting — the same id the response carries
		// in X-Request-Id, so they can read it off their own browser. NOT logged:
		// the query string, any body, or any header — an unmapped error is not a
		// reason to start recording card tokens and bearer tokens.
		log.Printf("api: unhandled error: %s %s request_id=%q: %v",
			c.Request.Method, c.Request.URL.Path, requestid.FromGin(c), err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, contracts.Error{
			Code:    contracts.ErrorCodeINTERNAL,
			Message: "Internal server error.",
		})
	}
}
