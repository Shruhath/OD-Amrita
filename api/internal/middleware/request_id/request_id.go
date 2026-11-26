// Package requestid gives every request an identifier this API owns.
//
// WHY IT EXISTS, WRITTEN THE DAY IT WAS NEEDED
//
//	An admin reported that tapping a card returned "Internal server error." The
//	only handle on that failure was a `fly-request-id` header in their browser's
//	network tab, and nothing on the server had written it down — the error
//	renderer dropped the cause, and the router installs no request logger. The
//	diagnosis was done by reading production's schema and audit log backwards
//	until the shape of the failure could be inferred. It took an hour; one log
//	line would have taken a minute.
//
//	`Fly-Request-Id` is the edge's identifier, not ours. It is genuinely useful —
//	it is what a user can read off their own browser — so it is adopted when
//	present. But it exists only because we are on Fly today, and an id that
//	disappears with the hosting provider is not an id the application can build
//	on. So one is minted when the edge does not supply one, and either way it is
//	echoed back on the response, recorded on the request context, and quoted by
//	anything that logs.
//
// TRUST
//
//	The inbound header is taken as a correlation hint and NOTHING else. It never
//	reaches a query, a template or an authorization decision. It is length-capped
//	and stripped of anything but a conservative character set before it is stored,
//	so a caller who invents one can pollute their own log lines and no one else's.
package requestid

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HeaderName is the response header carrying the id. Request headers are read
// from the edge's name first (see edgeHeader) and this one second.
const HeaderName = "X-Request-Id"

// edgeHeader is Fly's own per-request id. Reusing it means a line in our logs
// and a line in Fly's logs name the same request, which is the whole point of
// preferring it over a fresh uuid.
const edgeHeader = "Fly-Request-Id"

// contextKey is unexported so nothing outside this package can plant a value.
type contextKey struct{}

// maxLength bounds an id taken from a header. Fly's is ~30 characters; a uuid is
// 36. Anything past this is a caller being creative with our log file.
const maxLength = 64

// Middleware assigns the id and echoes it. Register it FIRST, before anything
// that might log or fail, or the request that most needs an id is the one
// without one.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitize(c.GetHeader(edgeHeader))
		if id == "" {
			id = sanitize(c.GetHeader(HeaderName))
		}
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(contextKeyName, id)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), contextKey{}, id))
		// Set before the handler runs, not after: a handler that writes its
		// response and returns has already sent the headers, and a value added
		// afterwards would never reach the client — which is precisely the
		// request someone is trying to report.
		c.Writer.Header().Set(HeaderName, id)
		c.Next()
	}
}

// contextKeyName is the gin-context key. gin's own store is string-keyed, so
// this cannot be the unexported struct; it is namespaced instead.
const contextKeyName = "requestid.id"

// FromGin returns the id assigned to this request, or "" if the middleware did
// not run. Callers log it; none of them branch on it.
func FromGin(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get(contextKeyName); ok {
		if id, ok := value.(string); ok {
			return id
		}
	}
	return FromContext(c.Request.Context())
}

// FromContext reads the id off a plain context, for code below the handler that
// never sees a *gin.Context.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// sanitize keeps only what an id is allowed to be made of. Everything else —
// spaces, quotes, newlines above all — is dropped rather than escaped, because a
// log line is the one place a newline someone else chose does real damage.
func sanitize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) > maxLength {
		raw = raw[:maxLength]
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}
