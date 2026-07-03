// Package config loads the api process configuration from the environment
// (12-factor). See api/.env.example for the full variable reference.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
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

// Auth holds Supabase-brokered Microsoft OAuth settings. The backend verifies
// Supabase's JWT locally and re-enforces the Amrita domain family.
type Auth struct {
	JWKSURL       string
	Issuer        string
	Audience      string
	AllowedDomain string
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

	TLS  TLS
	Auth Auth
}

// Load reads the environment and fails fast on a configuration the server
// must not start with (missing runtime DSN, invalid port, a malformed boolean,
// TLS enabled without a readable cert/key or with an unknown min version, or
// any attempt to configure the forbidden HTTP redirect listener).
func Load() (*Config, error) {
	tlsEnabled, err := parseBoolStrict("TLS_ENABLED", true)
	if err != nil {
		return nil, err
	}
	redirectListener, err := parseBoolStrict("HTTP_REDIRECT_LISTENER", false)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		AppEnv:              getenvDefault("APP_ENV", "development"),
		APIPort:             getenvDefault("API_PORT", "8443"),
		DatabaseURLMigrator: os.Getenv("DATABASE_URL_MIGRATOR"),
		DatabaseURLRuntime:  os.Getenv("DATABASE_URL_RUNTIME"),
		TLS: TLS{
			Enabled:    tlsEnabled,
			MinVersion: getenvDefault("TLS_MIN_VERSION", "1.2"),
			CertFile:   os.Getenv("TLS_CERT_FILE"),
			KeyFile:    os.Getenv("TLS_KEY_FILE"),
		},
		Auth: Auth{
			JWKSURL:       os.Getenv("AUTH_JWKS_URL"),
			Issuer:        os.Getenv("AUTH_ISSUER"),
			Audience:      getenvDefault("AUTH_AUDIENCE", "authenticated"),
			AllowedDomain: strings.ToLower(getenvDefault("AUTH_ALLOWED_DOMAIN", "amrita.edu")),
		},
	}

	if cfg.DatabaseURLRuntime == "" {
		return nil, fmt.Errorf("config: DATABASE_URL_RUNTIME is required (the od_runtime app DSN — see api/.env.example)")
	}
	// API_PORT must be a real TCP port so a typo fails here, naming the variable,
	// instead of surfacing as an opaque net.Listen error at startup.
	if n, err := strconv.Atoi(cfg.APIPort); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("config: API_PORT must be a TCP port in 1..65535, got %q", cfg.APIPort)
	}
	// NFR15: plain HTTP is rejected, not redirected. There is no code path for
	// a redirect listener; refuse the configuration outright.
	if redirectListener {
		return nil, fmt.Errorf("config: HTTP_REDIRECT_LISTENER must stay false — HTTP is rejected, not redirected (NFR15)")
	}

	if cfg.TLS.Enabled {
		switch cfg.TLS.MinVersion {
		case "1.2", "1.3":
			// ok — the only floors we allow (NFR15: never below 1.2).
		default:
			return nil, fmt.Errorf("config: TLS_MIN_VERSION must be \"1.2\" or \"1.3\", got %q", cfg.TLS.MinVersion)
		}
		if cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" {
			return nil, fmt.Errorf("config: TLS_ENABLED=true requires TLS_CERT_FILE and TLS_KEY_FILE (or set TLS_ENABLED=false behind a TLS-terminating ingress)")
		}
		// Verify the cert and key are actually readable now, not later at
		// ListenAndServeTLS after the pool is already open.
		if err := fileReadable(cfg.TLS.CertFile); err != nil {
			return nil, fmt.Errorf("config: TLS_CERT_FILE: %w", err)
		}
		if err := fileReadable(cfg.TLS.KeyFile); err != nil {
			return nil, fmt.Errorf("config: TLS_KEY_FILE: %w", err)
		}
	}
	if err := validateAuth(cfg.Auth, cfg.AppEnv); err != nil {
		return nil, err
	}

	return cfg, nil
}

func validateAuth(auth Auth, appEnv string) error {
	if auth.Audience == "" {
		return fmt.Errorf("config: AUTH_AUDIENCE is required")
	}
	if auth.AllowedDomain == "" {
		return fmt.Errorf("config: AUTH_ALLOWED_DOMAIN is required")
	}
	requireURLs := appEnv != "development" || auth.JWKSURL != "" || auth.Issuer != ""
	if !requireURLs {
		return nil
	}
	if auth.JWKSURL == "" {
		return fmt.Errorf("config: AUTH_JWKS_URL is required outside development")
	}
	if auth.Issuer == "" {
		return fmt.Errorf("config: AUTH_ISSUER is required outside development")
	}
	if err := validateHTTPSURL("AUTH_JWKS_URL", auth.JWKSURL); err != nil {
		return err
	}
	if err := validateHTTPSURL("AUTH_ISSUER", auth.Issuer); err != nil {
		return err
	}
	return nil
}

func validateHTTPSURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("config: %s must be a parseable https URL, got %q", name, raw)
	}
	return nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseBoolStrict returns def when the variable is unset, but errors on a
// malformed value rather than silently falling back — a typo in a
// security-relevant flag (TLS_ENABLED, HTTP_REDIRECT_LISTENER) must be loud.
func parseBoolStrict(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: %s=%q is not a valid boolean (use true/false)", key, v)
	}
	return b, nil
}

// fileReadable reports whether path can be opened for reading.
func fileReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read %q: %w", path, err)
	}
	return f.Close()
}
