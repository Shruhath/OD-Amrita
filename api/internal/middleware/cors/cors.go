// Package cors applies an exact-origin browser policy for the web portal.
package cors

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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
				c.AbortWithStatus(http.StatusForbidden)
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
			headers.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			headers.Set("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
