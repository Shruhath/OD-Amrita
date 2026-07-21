package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExactOriginCORSAllowDenyAndPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware([]string{"https://portal.od-amrita.example"}))
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
}
