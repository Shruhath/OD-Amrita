// Package cors applies an exact-origin browser policy for the web portal.
package cors

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

func Middleware(origins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		if _, ok := allowed[origin]; !ok {
			if c.Request.Method == http.MethodOptions {
				_ = c.Error(domain.DomainError{
					Code:    contracts.ErrorCodeRBACFORBIDDEN,
					Message: "Origin is not allowed.",
				})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		headers := c.Writer.Header()
		headers.Add("Vary", "Origin")
		headers.Set("Access-Control-Allow-Origin", origin)
		if c.Request.Method == http.MethodOptions {
			headers.Set("Access-Control-Allow-Methods", strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodOptions}, ", "))
			// Reflect the browser's requested headers, falling back to the
			// standard set when none are announced. This lets a client send an
			// additional non-credentialed request header (e.g. a deployment
			// tunnel's bypass header) without a server change. Safe here: the
			// policy is bearer-only — Access-Control-Allow-Credentials is never
			// set, so no cookies ride along — and the origin has already been
			// checked against the exact allow-list above, so only a trusted
			// origin ever reaches this line.
			requested := c.GetHeader("Access-Control-Request-Headers")
			if requested == "" {
				requested = "Authorization, Content-Type, Accept"
			}
			headers.Set("Access-Control-Allow-Headers", requested)
			// The preflight answer now depends on Access-Control-Request-Headers,
			// so it must vary on that header too — otherwise a shared cache keyed
			// on method+URL(+Origin) could serve this 600s-cached Allow-Headers to
			// a later preflight announcing a different header set and the browser
			// would wrongly block the real request.
			headers.Add("Vary", "Access-Control-Request-Headers")
			headers.Set("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
