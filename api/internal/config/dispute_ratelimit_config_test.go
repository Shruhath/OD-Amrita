// Config tests for the dispute form's SECOND, stricter rate limiter.
//
// The surface under test:
//
//	cfg.PublicDisputeRateLimit.PerMinute  <- PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE
//	cfg.PublicDisputeRateLimit.Burst      <- PUBLIC_DISPUTE_RATE_LIMIT_BURST
//
// Reuse the existing PublicRateLimit struct type rather than declaring a near
// identical second one — two fields with the same meaning and the same validation
// do not need two types, and a second type is a second place to forget a bound.
//
// The DEFAULTS are the load-bearing part and they are why this is a config test
// rather than a note in a design doc. The deployed Fly app carries no such secret,
// so a limiter that refuses to boot without one takes the API down at the next
// deploy — the same reasoning that gave the public read pair defaults, restated because a
// stricter limit is exactly the kind of value somebody makes mandatory "to be
// safe". Setting it to something unusable, by contrast, must fail LOUDLY: an
// operator raising the cap on event day has to be told their typo did nothing.
//
// Not gated behind an env flag or a build tag; these run in plain `go test ./...`.
package config

import (
	"testing"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
)

// The dispute write's defaults, from T14. Deliberately far below the dashboard's
// 30/minute: a student files a dispute once, thinks about it, maybe files a
// second for another slot. Anything approaching the read budget is a submission
// path that can be walked by anyone on the internet.
const (
	wantDisputePerMinute = 5
	wantDisputeBurst     = 3
)

// 0.11-CONFIG-001 — both fields have working defaults when unset (T14).
func TestLoad_PublicDisputeRateLimitDefaultsWhenUnset(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("[P0] Load with no dispute rate-limit env: %v — the deployed app carries no such secret, so a "+
			"missing value must NOT stop the server booting (T14)", err)
	}

	if cfg.PublicDisputeRateLimit.PerMinute != wantDisputePerMinute {
		t.Errorf("[P0] PublicDisputeRateLimit.PerMinute = %d, want %d",
			cfg.PublicDisputeRateLimit.PerMinute, wantDisputePerMinute)
	}
	if cfg.PublicDisputeRateLimit.Burst != wantDisputeBurst {
		t.Errorf("[P0] PublicDisputeRateLimit.Burst = %d, want %d",
			cfg.PublicDisputeRateLimit.Burst, wantDisputeBurst)
	}

	// The whole reason a second limiter exists: it must actually be stricter than
	// the group's. Two limiters with the same budget is one limiter with extra
	// bookkeeping, and nothing else in the suite would notice.
	if cfg.PublicDisputeRateLimit.PerMinute >= cfg.PublicRateLimit.PerMinute {
		t.Errorf("[P0] the dispute write budget (%d/min) is not stricter than the public read budget (%d/min). "+
			"T14 adds a second limiter precisely because a write and a read need different budgets — equal ones "+
			"mean the write inherited the browsing allowance",
			cfg.PublicDisputeRateLimit.PerMinute, cfg.PublicRateLimit.PerMinute)
	}
	if cfg.PublicRateLimit.PerMinute != ratelimit.DefaultPerMinute {
		t.Errorf("[P1] the public read default moved to %d (was %d); the dispute limiter must not retune the read "+
			"budget as a side effect of adding a write one",
			cfg.PublicRateLimit.PerMinute, ratelimit.DefaultPerMinute)
	}
}

// 0.11-CONFIG-002 — a SET value overrides, and a set-but-unusable value fails
// fast rather than silently falling back to the default.
//
// Falling back is the dangerous behaviour here, not failing: an operator who
// typed `PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE=5/min` and got a clean boot would
// believe they had raised a limit that never moved.
func TestLoad_PublicDisputeRateLimitOverridesAndRejectsGarbage(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":                 "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":                          "false",
		"PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE": "20",
		"PUBLIC_DISPUTE_RATE_LIMIT_BURST":      "8",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("[P0] Load: %v", err)
	}
	if cfg.PublicDisputeRateLimit.PerMinute != 20 || cfg.PublicDisputeRateLimit.Burst != 8 {
		t.Fatalf("[P0] dispute rate limit = %+v, want {20 8}", cfg.PublicDisputeRateLimit)
	}

	for _, invalid := range []string{"0", "-1", "five", "5/min", "1.5", "10001"} {
		setEnv(t, map[string]string{
			"DATABASE_URL_RUNTIME":                 "postgres://od_runtime:x@localhost:5432/od_amrita",
			"TLS_ENABLED":                          "false",
			"PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE": invalid,
		})
		if _, err := Load(); err == nil {
			t.Errorf("[P0] Load accepted PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE=%q. Use the SAME positive-integer "+
				"plus upper-bound validation the 0.10 pair already has — a limiter set to a million is "+
				"indistinguishable from no limiter at all", invalid)
		}
	}

	for _, invalid := range []string{"0", "-3", "three", "10001"} {
		setEnv(t, map[string]string{
			"DATABASE_URL_RUNTIME":            "postgres://od_runtime:x@localhost:5432/od_amrita",
			"TLS_ENABLED":                     "false",
			"PUBLIC_DISPUTE_RATE_LIMIT_BURST": invalid,
		})
		if _, err := Load(); err == nil {
			t.Errorf("[P0] Load accepted PUBLIC_DISPUTE_RATE_LIMIT_BURST=%q", invalid)
		}
	}
}
