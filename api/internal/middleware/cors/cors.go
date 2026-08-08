// Package cors applies an exact-origin browser policy for the web portal.
package cors

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// RouteSource reports the routes registered on the router this middleware is
// mounted on. *gin.Engine satisfies it.
//
// The preflight's Access-Control-Allow-Methods is DERIVED from this rather than
// written out by hand, and that is the whole point of the parameter. The list
// used to be the literal GET, POST, OPTIONS — which silently stopped being true
// the day PUT /admin/events/{id}/capture-credits/config was added. A PUT always
// triggers a preflight, and a preflight that does not name PUT makes the browser
// refuse to send the real request, so the fallback-credit cap could not be
// changed from the portal at all while every server-side test passed: nothing
// below the browser ever consults this header.
//
// Deriving it means the next PATCH or DELETE is advertised the moment it is
// registered, with no second place to remember.
type RouteSource interface {
	Routes() gin.RoutesInfo
}

func Middleware(origins []string, routes RouteSource) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	// Resolved on the first preflight, not at construction: this middleware is
	// mounted BEFORE the routes are registered (it has to be — it must run for
	// them), so at construction time the router still reports nothing.
	var (
		once           sync.Once
		allowedMethods string
	)
	methods := func() string {
		once.Do(func() { allowedMethods = joinMethods(routes) })
		return allowedMethods
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
			headers.Set("Access-Control-Allow-Methods", methods())
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

// joinMethods renders every method the router serves, plus OPTIONS.
//
// OPTIONS is added explicitly because it is never a registered route: this
// middleware answers preflights itself and aborts before gin's tree is consulted,
// so it would otherwise be missing from the very header it appears in.
//
// Sorted so the header is byte-stable across boots — gin reports routes in
// registration order, which would otherwise make an assertion on this header
// depend on the order main.go happens to wire its handlers in.
func joinMethods(routes RouteSource) string {
	seen := map[string]struct{}{http.MethodOptions: {}}
	if routes != nil {
		for _, route := range routes.Routes() {
			seen[route.Method] = struct{}{}
		}
	}
	list := make([]string, 0, len(seen))
	for method := range seen {
		list = append(list, method)
	}
	sort.Strings(list)
	return strings.Join(list, ", ")
}
