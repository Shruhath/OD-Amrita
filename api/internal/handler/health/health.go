// Package health serves GET /health. It answers with the GENERATED contract type
// (contracts.HealthStatus); the wire shape is owned by contracts/openapi.yaml —
// never hand-roll it here.
//
// TWO DEPTHS, AND WHY THE DEFAULT IS THE SHALLOW ONE
//
//	GET /health        liveness only. 200 {status:"ok", db:"unchecked"}. It does
//	                   NOT touch the database, and says so.
//	GET /health?db=1   readiness. Pings the runtime pool (2s timeout): 200
//	                   {status:"ok", db:"up"}, or 503 {status:"degraded",
//	                   db:"down"}.
//
// api/fly.toml health-checks the shallow form every 15s on two machines. When
// that check pinged the database it was 5,760 touches a day at zero traffic, and
// it kept the Neon compute from ever scaling to zero — every ping resets Neon's
// 5-minute inactivity timer. So the frequent probe stays cheap.
//
// What the shallow probe used to do instead was answer db:"up" without checking,
// which is a lie an operator reads as "the database is fine". It now reports
// "unchecked", and anything that genuinely needs to know — the post-deploy smoke
// check (api/scripts/smoke.sh), a restore verification — asks with ?db=1.
//
// The trade stated plainly: Fly cannot pull a machine out of rotation when the
// database is unreachable, because its check is the shallow one. A dead database
// shows up as 5xx on real requests, not as an unhealthy machine. The deployment
// runbook records that.
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
func Handler(db Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !deepCheckRequested(c) {
			c.JSON(http.StatusOK, contracts.HealthStatus{
				Status: contracts.HealthStatusStatusOk,
				Db:     contracts.HealthStatusDbUnchecked,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), pingTimeout)
		defer cancel()
		if db == nil || db.Ping(ctx) != nil {
			c.JSON(http.StatusServiceUnavailable, contracts.HealthStatus{
				Status: contracts.HealthStatusStatusDegraded,
				Db:     contracts.HealthStatusDbDown,
			})
			return
		}
		c.JSON(http.StatusOK, contracts.HealthStatus{
			Status: contracts.HealthStatusStatusOk,
			Db:     contracts.HealthStatusDbUp,
		})
	}
}

// deepCheckRequested is true only for the documented ?db=1. Anything else —
// absent, empty, "0", a typo — gets the shallow answer, which says "unchecked"
// rather than pretending, so a mistyped query can never be read as a pass.
func deepCheckRequested(c *gin.Context) bool {
	return c.Query("db") == string(contracts.GetHealthParamsDbN1)
}
