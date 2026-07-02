// Package config loads the api process configuration from the environment
// (12-factor). See api/.env.example for the full variable reference.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// TLS holds the transport-security settings (NFR15: the server is TLS-only —
// plain HTTP is rejected, never redirected).
type TLS struct {
	// Enabled serves TLS directly. False is only for ingress-terminated
	// deployments where the platform enforces TLS (and must also reject, not
	// redirect, plain HTTP).
	Enabled    bool
	MinVersion string // "1.2" (default) or "1.3"
	CertFile   string
	KeyFile    string
}

// Config is the fully-resolved api process configuration.
type Config struct {
	AppEnv  string
	APIPort string

	// DatabaseURLMigrator is the od_migrator (DDL) DSN — used by goose, never
	// by the app runtime. Optional here; the Makefile requires it for migrate-*.
	DatabaseURLMigrator string
	// DatabaseURLRuntime is the od_runtime (DML-only) DSN the app pool uses.
	DatabaseURLRuntime string

	TLS TLS
}

// Load reads the environment and fails fast on a configuration the server
// must not start with (missing runtime DSN, TLS enabled without cert/key, or
// any attempt to configure the forbidden HTTP redirect listener).
func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:              getenvDefault("APP_ENV", "development"),
		APIPort:             getenvDefault("API_PORT", "8443"),
		DatabaseURLMigrator: os.Getenv("DATABASE_URL_MIGRATOR"),
		DatabaseURLRuntime:  os.Getenv("DATABASE_URL_RUNTIME"),
		TLS: TLS{
			Enabled:    parseBoolDefault("TLS_ENABLED", true),
			MinVersion: getenvDefault("TLS_MIN_VERSION", "1.2"),
			CertFile:   os.Getenv("TLS_CERT_FILE"),
			KeyFile:    os.Getenv("TLS_KEY_FILE"),
		},
	}

	if cfg.DatabaseURLRuntime == "" {
		return nil, fmt.Errorf("config: DATABASE_URL_RUNTIME is required (the od_runtime app DSN — see api/.env.example)")
	}
	if cfg.TLS.Enabled && (cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "") {
		return nil, fmt.Errorf("config: TLS_ENABLED=true requires TLS_CERT_FILE and TLS_KEY_FILE (or set TLS_ENABLED=false behind a TLS-terminating ingress)")
	}
	// NFR15: plain HTTP is rejected, not redirected. There is no code path for
	// a redirect listener; refuse the configuration outright.
	if parseBoolDefault("HTTP_REDIRECT_LISTENER", false) {
		return nil, fmt.Errorf("config: HTTP_REDIRECT_LISTENER must stay false — HTTP is rejected, not redirected (NFR15)")
	}

	return cfg, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBoolDefault(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
