package requestid

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func newRouter() (*gin.Engine, *string) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	seen := new(string)
	router.GET("/probe", func(c *gin.Context) {
		*seen = FromGin(c)
		c.Status(http.StatusOK)
	})
	return router, seen
}

func do(router *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// The edge's id is what a user can read off their own browser, so a line in our
// log and a line in Fly's must name the same request.
func TestMiddleware_AdoptsTheEdgeRequestID(t *testing.T) {
	router, seen := newRouter()

	recorder := do(router, map[string]string{edgeHeader: "01M1B7MN2H272C84WNN7E06110-fra"})

	if *seen != "01M1B7MN2H272C84WNN7E06110-fra" {
		t.Fatalf("handler saw %q, want the edge id", *seen)
	}
	if got := recorder.Header().Get(HeaderName); got != "01M1B7MN2H272C84WNN7E06110-fra" {
		t.Fatalf("%s = %q, want the edge id echoed back", HeaderName, got)
	}
}

// An id that only exists because we are on Fly is not an id the application can
// build on. With no edge header there must still be one.
func TestMiddleware_MintsAnIDWhenTheEdgeSuppliesNone(t *testing.T) {
	router, seen := newRouter()

	recorder := do(router, nil)

	if *seen == "" {
		t.Fatal("no id was assigned; every request must have one")
	}
	if _, err := uuid.Parse(*seen); err != nil {
		t.Fatalf("minted id %q is not a uuid: %v", *seen, err)
	}
	if got := recorder.Header().Get(HeaderName); got != *seen {
		t.Fatalf("%s = %q, want the minted id %q", HeaderName, got, *seen)
	}
}

// The inbound header is a correlation hint from an untrusted caller. A newline in
// it would let that caller forge whole log lines — the one place this value can
// do real damage.
func TestMiddleware_StripsEverythingAnIDMayNotContain(t *testing.T) {
	router, seen := newRouter()

	do(router, map[string]string{HeaderName: "abc 123\r\nfake: line\"'"})

	if strings.ContainsAny(*seen, " \r\n\"'") {
		t.Fatalf("id %q kept characters that can forge a log line", *seen)
	}
	if *seen != "abc123fakeline" {
		t.Fatalf("id = %q, want the allowed characters kept in order", *seen)
	}
}

// A header long enough to bloat every log line it appears in is truncated, not
// rejected: the request is fine, the id is just not allowed to be a payload.
func TestMiddleware_BoundsTheLengthOfABorrowedID(t *testing.T) {
	router, seen := newRouter()

	do(router, map[string]string{edgeHeader: strings.Repeat("a", maxLength*4)})

	if len(*seen) != maxLength {
		t.Fatalf("id length = %d, want it capped at %d", len(*seen), maxLength)
	}
}

// A header present but empty (or entirely made of characters that are stripped)
// must fall through to a minted id rather than leaving the request unnamed.
func TestMiddleware_MintsWhenTheBorrowedIDSanitizesToNothing(t *testing.T) {
	router, seen := newRouter()

	do(router, map[string]string{edgeHeader: "   ", HeaderName: "!!!"})

	if _, err := uuid.Parse(*seen); err != nil {
		t.Fatalf("id = %q, want a minted uuid when nothing usable arrived: %v", *seen, err)
	}
}

// The id must reach code below the handler that never sees a *gin.Context — a
// service, a store call — through the request's own context.
func TestMiddleware_PutsTheIDOnTheRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	var fromPlainContext string
	router.GET("/probe", func(c *gin.Context) {
		fromPlainContext = FromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	do(router, map[string]string{edgeHeader: "edge-id"})

	if fromPlainContext != "edge-id" {
		t.Fatalf("FromContext = %q, want the id to travel on the request context", fromPlainContext)
	}
}

// The header must be set BEFORE the handler runs. A handler that writes its
// response and returns has already flushed the headers, and an id added
// afterwards would never reach the client — which is exactly the request
// somebody is trying to report.
func TestMiddleware_SetsTheHeaderBeforeTheHandlerWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/probe", func(c *gin.Context) {
		c.String(http.StatusInternalServerError, "boom")
	})

	recorder := do(router, nil)

	if recorder.Header().Get(HeaderName) == "" {
		t.Fatal("a failing handler answered with no request id; that is the response that needs one most")
	}
}

// FromGin on a context the middleware never touched returns empty rather than
// panicking. Callers log the result; none of them branch on it.
func TestFromGin_IsSafeWithoutTheMiddleware(t *testing.T) {
	if got := FromGin(nil); got != "" {
		t.Fatalf("FromGin(nil) = %q, want empty", got)
	}
	if got := FromContext(nil); got != "" {
		t.Fatalf("FromContext(nil) = %q, want empty", got)
	}
}
