package errormw

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandler_LogsUnhandledError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("request_id", "req-test-123")
		c.Next()
	})
	r.Use(Handler())
	r.GET("/test-crash", func(c *gin.Context) {
		_ = c.Error(errors.New("unexpected database disconnect"))
		c.Abort()
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test-crash", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "api: unhandled error: GET /test-crash") {
		t.Errorf("expected log to contain %q, got %q", "api: unhandled error: GET /test-crash", logs)
	}
	if !strings.Contains(logs, `request_id="req-test-123"`) {
		t.Errorf("expected log to contain %q, got %q", `request_id="req-test-123"`, logs)
	}
	if !strings.Contains(logs, "unexpected database disconnect") {
		t.Errorf("expected log to contain %q, got %q", "unexpected database disconnect", logs)
	}
}

func TestHandler_LogsUnhandledErrorWithHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	r := gin.New()
	r.Use(Handler())
	r.GET("/test-header", func(c *gin.Context) {
		_ = c.Error(errors.New("db timeout"))
		c.Abort()
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test-header", nil)
	req.Header.Set("X-Request-ID", "upstream-xyz-999")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, `request_id="upstream-xyz-999"`) {
		t.Errorf("expected log to contain %q, got %q", `request_id="upstream-xyz-999"`, logs)
	}
}
