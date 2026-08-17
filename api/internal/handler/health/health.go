// Package health serves GET /health — the liveness probe. It answers with the
// GENERATED contract type (contracts.HealthStatus): 200 {status:"ok", db:"up"}.
// The wire shape is owned by contracts/openapi.yaml — never hand-roll it here.
//
// ── THE DB PROBE IS DELIBERATELY DISABLED — READ BEFORE RE-ENABLING ─────────
//
// This handler used to call pool.Ping on every request, and api/fly.toml health
// checks this path at interval = "15s" across TWO machines. That is 5,760
// database touches per day AT ZERO TRAFFIC, and it is why the Neon compute
// never once scaled to zero: every ping resets Neon's 5-minute inactivity
// timer, so the project billed compute 730 hours a month to serve an app used
// for a few hours a week.
//
// The ping is COMMENTED OUT, not deleted, in Handler below. Uncommenting it
// restores the old behaviour exactly — but do not do that without also raising
// api/fly.toml's interval well past Neon's suspend window, or the bill comes
// straight back.
//
// WHAT THIS COSTS US, STATED PLAINLY: Fly can no longer pull a machine out of
// rotation when the database is unreachable. A machine with a dead database
// keeps serving, and a mark fails with a 5xx instead of being routed to a
// healthy peer. The probe is now fail-OPEN on the database, where it used to be
// fail-closed. docs/deployment-runbook.md §7 records that trade.
//
// The 503 branch and the `down` enum member stay in contracts/openapi.yaml on
// purpose: the contract still describes the response this handler is capable of
// producing, and re-enabling the ping must not require a contract change.
package health

import (
	"context"
	"net/http"
	"time"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
	"github.com/gin-gonic/gin"
)

// pingTimeout bounds the DB probe so a wedged pool degrades the probe instead
// of hanging it.
const pingTimeout = 2 * time.Second

// Pinger is the slice of the runtime pool the probe needs (satisfied by
// *pgxpool.Pool).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler returns the gin handler for GET /health.
//
// db is still accepted, and Pinger/pingTimeout still exist, so re-enabling the
// probe is a matter of uncommenting the block below and nothing else — no
// signature change, no rewiring in cmd/api/main.go.
func Handler(db Pinger) gin.HandlerFunc {
	// Referenced so the parameter stays live for the commented-out probe; drop
	// this line when the block below is restored.
	_ = db
	return func(c *gin.Context) {
		// ── DISABLED: the per-request database ping (see the package comment) ──
		//
		// ctx, cancel := context.WithTimeout(c.Request.Context(), pingTimeout)
		// defer cancel()
		//
		// if err := db.Ping(ctx); err != nil {
		// 	c.JSON(http.StatusServiceUnavailable, contracts.HealthStatus{
		// 		Status: contracts.HealthStatusStatusDegraded,
		// 		Db:     contracts.HealthStatusDbDown,
		// 	})
		// 	return
		// }
		//
		// ───────────────────────────────────────────────────────────────────────
		c.JSON(http.StatusOK, contracts.HealthStatus{
			Status: contracts.HealthStatusStatusOk,
			Db:     contracts.HealthStatusDbUp,
		})
	}
}
