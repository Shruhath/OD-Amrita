package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

func serve(t *testing.T, p Pinger) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", Handler(p))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return body
}

// AC 4: DB ping succeeds → 200 { status: "ok", db: "up" }.
func TestHandler_DBUp_200(t *testing.T) {
	w := serve(t, fakePinger{err: nil})

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	body := decode(t, w)
	if body["status"] != "ok" {
		t.Errorf("want status=ok, got %q", body["status"])
	}
	if body["db"] != "up" {
		t.Errorf("want db=up, got %q", body["db"])
	}
}

// AC 4: DB ping fails → 503 { status: "degraded", db: "down" }.
func TestHandler_DBDown_503(t *testing.T) {
	w := serve(t, fakePinger{err: errors.New("connection refused")})

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
	body := decode(t, w)
	if body["status"] != "degraded" {
		t.Errorf("want status=degraded, got %q", body["status"])
	}
	if body["db"] != "down" {
		t.Errorf("want db=down, got %q", body["db"])
	}
}

// The wire shape is exactly the generated HealthStatus contract: both required
// fields, nothing else.
func TestHandler_WireShapeMatchesContract(t *testing.T) {
	w := serve(t, fakePinger{err: nil})

	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 2 {
		t.Errorf("HealthStatus carries exactly {status, db}, got keys: %v", raw)
	}
	for _, k := range []string{"status", "db"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing required contract field %q", k)
		}
	}
}
