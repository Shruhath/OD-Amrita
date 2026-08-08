package cors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
)

func TestExactOriginCORSAllowDenyAndPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(Middleware([]string{"https://portal.od-amrita.example"}, router))
	router.GET("/api/v1/admin/events", func(c *gin.Context) { c.Status(http.StatusOK) })

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/events", nil)
	allowedReq.Header.Set("Origin", "https://portal.od-amrita.example")
	router.ServeHTTP(allowed, allowedReq)
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://portal.od-amrita.example" {
		t.Errorf("allowed origin header = %q", got)
	}

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/events", nil)
	deniedReq.Header.Set("Origin", "https://attacker.example")
	router.ServeHTTP(denied, deniedReq)
	if got := denied.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("arbitrary origin received allow header %q", got)
	}

	preflight := httptest.NewRecorder()
	preflightReq := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/events", nil)
	preflightReq.Header.Set("Origin", "https://portal.od-amrita.example")
	preflightReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflightReq.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	router.ServeHTTP(preflight, preflightReq)
	if preflight.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", preflight.Code)
	}
	if preflight.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("bearer CORS must not enable credentials")
	}
	if preflight.Header().Get("Access-Control-Allow-Headers") == "" || preflight.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight missing allowed headers or methods")
	}

	// A trusted origin may announce an extra non-credentialed request header
	// (a deployment tunnel's bypass header is the motivating case); the
	// preflight must reflect it in Access-Control-Allow-Headers, or the browser
	// blocks the real request before it is ever sent.
	reflect := httptest.NewRecorder()
	reflectReq := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/events", nil)
	reflectReq.Header.Set("Origin", "https://portal.od-amrita.example")
	reflectReq.Header.Set("Access-Control-Request-Method", http.MethodGet)
	reflectReq.Header.Set("Access-Control-Request-Headers", "authorization,ngrok-skip-browser-warning")
	router.ServeHTTP(reflect, reflectReq)
	if got := reflect.Header().Get("Access-Control-Allow-Headers"); got != "authorization,ngrok-skip-browser-warning" {
		t.Errorf("preflight did not reflect requested headers: allow-headers = %q", got)
	}

	deniedPreflight := httptest.NewRecorder()
	deniedPreflightReq := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/events", nil)
	deniedPreflightReq.Header.Set("Origin", "https://attacker.example")
	deniedPreflightReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	router.ServeHTTP(deniedPreflight, deniedPreflightReq)
	if deniedPreflight.Code != http.StatusForbidden {
		t.Fatalf("denied preflight status = %d, want 403", deniedPreflight.Code)
	}
	var envelope contracts.Error
	if err := json.Unmarshal(deniedPreflight.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("denied preflight must return generated error envelope: %v", err)
	}
	if envelope.Code != contracts.ErrorCodeRBACFORBIDDEN || envelope.Message != "Origin is not allowed." {
		t.Fatalf("denied preflight envelope = %#v", envelope)
	}
}

// The preflight must name every method the router actually serves.
//
// A browser refuses to send a request whose method the preflight did not list,
// and nothing below the browser reads this header — so an unadvertised method is
// an endpoint that works in every Go test, every curl and every integration
// harness, and is unreachable from the product. That is exactly what happened to
// the one PUT in this API: the header was the hand-written literal "GET, POST,
// OPTIONS" and stayed that way when the route was added.
//
// Registering the four methods and asserting the header carries all of them is
// what makes the derivation, rather than a fresh list in a second place, the
// thing under test.
func TestPreflightAdvertisesEveryMethodTheRouterServes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware([]string{"https://portal.od-amrita.example"}, router))
	noop := func(c *gin.Context) { c.Status(http.StatusOK) }
	router.GET("/api/v1/admin/events", noop)
	router.POST("/api/v1/admin/events", noop)
	router.PUT("/api/v1/admin/events/:event_id/capture-credits/config", noop)
	router.DELETE("/api/v1/admin/events/:event_id", noop)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/events", nil)
	request.Header.Set("Origin", "https://portal.od-amrita.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPut)
	router.ServeHTTP(recorder, request)

	got := recorder.Header().Get("Access-Control-Allow-Methods")
	if got != "DELETE, GET, OPTIONS, POST, PUT" {
		t.Fatalf("Access-Control-Allow-Methods = %q, want every served method plus OPTIONS", got)
	}
}

// OPTIONS is answered by this middleware and is never a registered route, so it
// has to be added by hand — and a preflight that omits it is one a browser will
// not accept. Asserted on a router with no routes at all so the guarantee cannot
// be satisfied accidentally by some handler someone happened to register.
func TestPreflightAlwaysAdvertisesOptionsItself(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware([]string{"https://portal.od-amrita.example"}, router))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/anything", nil)
	request.Header.Set("Origin", "https://portal.od-amrita.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	router.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods = %q, want OPTIONS on a router with no routes", got)
	}
}
