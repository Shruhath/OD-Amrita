//go:build integration

// Package health is the acceptance harness for the real gin /health handler.
//
// Black-box: it drives the running server over HTTP and asserts the JSON shape
// (status + db) rather than importing the handler. It is integration-tagged and
// env-gated on API_BASE_URL, so `go test ./...` (no tag) stays green.
//
//	API_BASE_URL=https://localhost:8443 go test -tags integration ./test/health/... -v
//
// Two depths are exercised: the shallow default (db "unchecked", never touches
// the database — what the frequent Fly check hits) and the deep ?db=1 probe,
// which pings the pool. The deep db-down case (503) needs the DB stopped while
// the server runs; set HEALTH_EXPECT_DB_DOWN=1 for that run.
//
// Priority tags: [P1] important (readiness surface).
package health

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

// healthStatus mirrors the generated contract type contracts/generated/go →
// HealthStatus, which carries `db`. The handler must return the
// generated type; this harness asserts the wire shape that type serializes to.
type healthStatus struct {
	Status string `json:"status"`
	DB     string `json:"db"`
}

func baseURLOrSkip(t *testing.T) string {
	t.Helper()
	u := os.Getenv("API_BASE_URL")
	if u == "" {
		t.Skip("API_BASE_URL not set; skipping /health integration test (start the server to run)")
	}
	return u
}

// getHealth issues GET {base}/health over a TLS client that tolerates the
// localhost self-signed cert (server is TLS-only per NFR15).
func getHealth(t *testing.T, base string) (int, healthStatus) {
	t.Helper()
	return getHealthPath(t, base, "/health")
}

// getDeepHealth asks for the database ping (?db=1).
func getDeepHealth(t *testing.T, base string) (int, healthStatus) {
	t.Helper()
	return getHealthPath(t, base, "/health?db=1")
}

func getHealthPath(t *testing.T, base, path string) (int, healthStatus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // localhost self-signed
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	var body healthStatus
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s body: %v", path, err)
	}
	return resp.StatusCode, body
}

// The shallow default never touches the database, and says so: 200
// {status:"ok", db:"unchecked"} whether or not the database is up.
func TestHealth_Shallow_ReportsUnchecked(t *testing.T) {
	base := baseURLOrSkip(t)

	code, body := getHealth(t, base)
	if code != http.StatusOK {
		t.Errorf("[P1] expected 200 from the shallow probe, got %d", code)
	}
	if body.Status != "ok" {
		t.Errorf("[P1] expected status=ok, got %q", body.Status)
	}
	if body.DB != "unchecked" {
		t.Errorf("[P1] the shallow probe does not ping, so db must be \"unchecked\", got %q", body.DB)
	}
}

// With the DB reachable, GET /health?db=1 → 200 {status:"ok", db:"up"}.
func TestHealth_Deep_DBUp_200(t *testing.T) {
	if os.Getenv("HEALTH_EXPECT_DB_DOWN") == "1" {
		t.Skip("HEALTH_EXPECT_DB_DOWN=1 — this run exercises the db-down case instead")
	}
	base := baseURLOrSkip(t)

	code, body := getDeepHealth(t, base)
	if code != http.StatusOK {
		t.Errorf("[P1] expected 200 with DB up, got %d", code)
	}
	if body.Status != "ok" {
		t.Errorf("[P1] expected status=ok, got %q", body.Status)
	}
	if body.DB != "up" {
		t.Errorf("[P1] expected db=up, got %q", body.DB)
	}
}

// With the DB unreachable, GET /health?db=1 → 503 {status:"degraded", db:"down"}.
// Gated on HEALTH_EXPECT_DB_DOWN so it only runs in a phase that has torn the DB
// down under a still-running server.
func TestHealth_Deep_DBDown_503(t *testing.T) {
	if os.Getenv("HEALTH_EXPECT_DB_DOWN") != "1" {
		t.Skip("set HEALTH_EXPECT_DB_DOWN=1 (server up, DB stopped) to run the db-down case")
	}
	base := baseURLOrSkip(t)

	code, body := getDeepHealth(t, base)
	if code != http.StatusServiceUnavailable {
		t.Errorf("[P1] expected 503 with DB down, got %d", code)
	}
	if body.Status != "degraded" {
		t.Errorf("[P1] expected status=degraded, got %q", body.Status)
	}
	if body.DB != "down" {
		t.Errorf("[P1] expected db=down, got %q", body.DB)
	}
}

// Both depths conform to the HealthStatus contract — both fields present, and
// db constrained to the up|down|unchecked enum.
func TestHealth_ResponseShapeMatchesContract(t *testing.T) {
	base := baseURLOrSkip(t)

	for _, get := range []func(*testing.T, string) (int, healthStatus){getHealth, getDeepHealth} {
		_, body := get(t, base)
		if body.Status == "" {
			t.Error("[P1] status is required and must be non-empty")
		}
		switch body.DB {
		case "up", "down", "unchecked":
		default:
			t.Errorf("[P1] db must be one of the contract enum {up,down,unchecked}, got %q", body.DB)
		}
	}
}
