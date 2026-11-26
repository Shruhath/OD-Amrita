// Package bodylimit applies a hard upper bound before a handler reads a request
// body. Individual handlers may wrap the body again with a smaller, route-
// specific limit.
package bodylimit

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Middleware limits every non-empty request body to maxBytes.
func Middleware(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		panic("bodylimit: maxBytes must be positive")
	}
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
