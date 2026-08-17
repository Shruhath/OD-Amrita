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

// countingPinger records whether the probe reached the database at all.
type countingPinger struct{ calls int }

func (c *countingPinger) Ping(ctx context.Context) error { c.calls++; return nil }

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

// The probe does NOT consult the database, so a broken one does not degrade it.
//
// This replaces the old AC-4 db-down test, and the inversion is deliberate — it
// is the trade the cost fix bought, written down where it will be read. The
// handler used to ping the pool on every request; api/fly.toml checks this path
// every 15s across two machines, which is 5,760 database touches a day at zero
// traffic and is why the Neon compute never scaled to zero.
//
// WHAT WE GAVE UP: Fly can no longer route around a machine whose database is
// unreachable. A failing pinger now produces a healthy answer. If that trade is
// ever reversed, uncomment the probe in Handler and restore the 503 assertions
// below — the contract still describes that response, and pingTimeout and
// Pinger are both still here for it.
func TestHandler_DBDown_StillReports200_ProbeIsDeliberatelyDBFree(t *testing.T) {
	w := serve(t, fakePinger{err: errors.New("connection refused")})

	if w.Code != http.StatusOK {
		t.Errorf("want 200 — the probe must not touch the database, got %d", w.Code)
	}
	body := decode(t, w)
	if body["status"] != "ok" {
		t.Errorf("want status=ok, got %q", body["status"])
	}
	if body["db"] != "up" {
		t.Errorf("want db=up, got %q", body["db"])
	}
}

// The pinger is never invoked. Asserted separately from the response shape
// because it is the actual cost property: a handler that pings and then ignores
// the result would satisfy the test above and still bill for the ping.
func TestHandler_NeverCallsPing(t *testing.T) {
	counter := &countingPinger{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", Handler(counter))

	for range 3 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	}

	if counter.calls != 0 {
		t.Errorf("Ping called %d times, want 0 — every call resets the database's "+
			"idle timer and keeps a suspending compute billing", counter.calls)
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
