package health

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// CachedPinger wraps a Pinger with TTL caching, singleflight coalescing,
// and cold-start retries to minimize DB CPU/wakeups and prevent false 503s.
type CachedPinger struct {
	pinger      Pinger
	ttl         time.Duration
	group       singleflight.Group
	lastSuccess atomic.Int64 // UnixNano timestamp of last successful ping
}

// NewCachedPinger constructs a CachedPinger with the specified cache TTL.
func NewCachedPinger(pinger Pinger, ttl time.Duration) *CachedPinger {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &CachedPinger{
		pinger: pinger,
		ttl:    ttl,
	}
}

// Ping implements Pinger, checking cache first and coalescing concurrent DB pings.
func (c *CachedPinger) Ping(ctx context.Context) error {
	now := time.Now().UnixNano()
	last := c.lastSuccess.Load()

	// Return cached health if within TTL window
	if last > 0 && time.Duration(now-last) < c.ttl {
		return nil
	}

	// Coalesce concurrent probes to a single DB ping
	_, err, _ := c.group.Do("db_ping", func() (any, error) {
		pingCtx, pingCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
		defer pingCancel()

		var lastErr error
		for attempt := 1; attempt <= 2; attempt++ {
			if pingCtx.Err() != nil {
				return nil, pingCtx.Err()
			}

			attemptTimeout := 2 * time.Second
			if attempt == 2 {
				attemptTimeout = 1500 * time.Millisecond
			}

			attemptCtx, cancel := context.WithTimeout(pingCtx, attemptTimeout)
			lastErr = c.pinger.Ping(attemptCtx)
			cancel()

			if lastErr == nil {
				c.lastSuccess.Store(time.Now().UnixNano())
				return nil, nil
			}

			if attempt < 2 {
				select {
				case <-time.After(150 * time.Millisecond):
				case <-pingCtx.Done():
					return nil, pingCtx.Err()
				}
			}
		}
		return nil, fmt.Errorf("ping failed after retries: %w", lastErr)
	})

	return err
}
