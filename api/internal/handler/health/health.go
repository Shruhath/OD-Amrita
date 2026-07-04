// Package health serves GET /health — the liveness + DB-readiness probe. It
// pings the runtime pool and answers with the GENERATED contract type
// (contracts.HealthStatus): 200 {status:"ok", db:"up"} on ping success,
// 503 {status:"degraded", db:"down"} on failure. The wire shape is owned by
// contracts/openapi.yaml — never hand-roll it here.
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
		ctx, cancel := context.WithTimeout(c.Request.Context(), pingTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
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
