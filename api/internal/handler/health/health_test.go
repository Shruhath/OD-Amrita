package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

// countingPinger records whether the probe reached the database at all.
type countingPinger struct{ calls int }

func (c *countingPinger) Ping(ctx context.Context) error { c.calls++; return nil }

// blockingPinger waits until its context is done, like a wedged pool.
type blockingPinger struct{ deadline time.Duration }

func (b *blockingPinger) Ping(ctx context.Context) error {
	if d, ok := ctx.Deadline(); ok {
		b.deadline = time.Until(d)
	}
	<-ctx.Done()
	return ctx.Err()
}

func serve(t *testing.T, p Pinger, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", Handler(p))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
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

func assertBody(t *testing.T, w *httptest.ResponseRecorder, code int, status, db string) {
	t.Helper()
	if w.Code != code {
		t.Errorf("want %d, got %d", code, w.Code)
	}
	body := decode(t, w)
	if body["status"] != status {
		t.Errorf("want status=%s, got %q", status, body["status"])
	}
	if body["db"] != db {
		t.Errorf("want db=%s, got %q", db, body["db"])
	}
}

// The shallow probe is honest: it did not check, so it says "unchecked" rather
// than "up" — even when the database is in fact down.
func TestHandler_Shallow_ReportsUnchecked(t *testing.T) {
	assertBody(t, serve(t, fakePinger{}, "/health"), http.StatusOK, "ok", "unchecked")
	assertBody(t, serve(t, fakePinger{err: errors.New("connection refused")}, "/health"), http.StatusOK, "ok", "unchecked")
}

// The cost property: the frequent platform check must never touch the database
// (every ping resets Neon's idle timer and keeps a suspending compute billing).
// Only the documented ?db=1 pings; any other value stays shallow.
func TestHandler_OnlyDBEqualsOnePings(t *testing.T) {
	for _, target := range []string{"/health", "/health?db=", "/health?db=0", "/health?db=true", "/health?deep=1"} {
		counter := &countingPinger{}
		w := serve(t, counter, target)
		if counter.calls != 0 {
			t.Errorf("%s: Ping called %d times, want 0", target, counter.calls)
		}
		if body := decode(t, w); body["db"] != "unchecked" {
			t.Errorf("%s: want db=unchecked, got %q", target, body["db"])
		}
	}
	counter := &countingPinger{}
	serve(t, counter, "/health?db=1")
	if counter.calls != 1 {
		t.Errorf("?db=1: Ping called %d times, want exactly 1", counter.calls)
	}
}

// Deep probe, database reachable → 200 {ok, up}.
func TestHandler_Deep_DBUp_200(t *testing.T) {
	assertBody(t, serve(t, fakePinger{}, "/health?db=1"), http.StatusOK, "ok", "up")
}

// Deep probe, database unreachable → 503 {degraded, down}.
func TestHandler_Deep_DBDown_503(t *testing.T) {
	assertBody(t, serve(t, fakePinger{err: errors.New("connection refused")}, "/health?db=1"),
		http.StatusServiceUnavailable, "degraded", "down")
}

// A wedged pool degrades the probe within the 2s bound instead of hanging it.
func TestHandler_Deep_PingIsBoundedByTheTimeout(t *testing.T) {
	pinger := &blockingPinger{}
	start := time.Now()
	w := serve(t, pinger, "/health?db=1")
	if elapsed := time.Since(start); elapsed > pingTimeout+time.Second {
		t.Errorf("probe took %v, want it bounded near %v", elapsed, pingTimeout)
	}
	if pinger.deadline <= 0 || pinger.deadline > pingTimeout {
		t.Errorf("ping deadline = %v, want within %v", pinger.deadline, pingTimeout)
	}
	assertBody(t, w, http.StatusServiceUnavailable, "degraded", "down")
}

// The wire shape is exactly the generated HealthStatus contract: both required
// fields, nothing else — in both depths.
func TestHandler_WireShapeMatchesContract(t *testing.T) {
	for _, target := range []string{"/health", "/health?db=1"} {
		w := serve(t, fakePinger{}, target)
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(raw) != 2 {
			t.Errorf("%s: HealthStatus carries exactly {status, db}, got keys: %v", target, raw)
		}
		for _, k := range []string{"status", "db"} {
			if _, ok := raw[k]; !ok {
				t.Errorf("%s: missing required contract field %q", target, k)
			}
		}
	}
}
