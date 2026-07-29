// Package config loads the api process configuration from the environment
// (12-factor). See api/.env.example for the full variable reference.
package config

import (
	"encoding/base64"
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
	// BootstrapAdmins is the lowercased set of emails provisioned as `admin` on
	// first login (Story 1.5b). Empty by default — every real login then
	// provisions as the least-privileged `student` until an admin elevates them
	// (Epic 2). Never commit a concrete admin email here; it loads from the
	// environment (AUTH_BOOTSTRAP_ADMINS).
	BootstrapAdmins map[string]struct{}
}

// CardSeal holds server-only card-token HMAC keys. Keys are decoded bytes from
// CARD_SEAL_HMAC_KEYS and must never be logged or returned to clients.
type CardSeal struct {
	ActiveVersion string
	HMACKeys      map[string][]byte
}

// Config is the fully-resolved api process configuration.
type Config struct {
	AppEnv               string
	APIPort              string
	PortalAllowedOrigins []string

	// DatabaseURLMigrator is the od_migrator (DDL) DSN — used by goose, never
	// by the app runtime. Optional here; the Makefile requires it for migrate-*.
	DatabaseURLMigrator string
	// DatabaseURLRuntime is the od_runtime (DML-only) DSN the app pool uses.
	DatabaseURLRuntime string

	TLS      TLS
	Auth     Auth
	CardSeal CardSeal
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
		// Normalize so " Development"/"DEV"-style values compare cleanly against
		// the "development" gate (mirrors the AllowedDomain lowercasing below).
		AppEnv:               strings.ToLower(strings.TrimSpace(getenvDefault("APP_ENV", "development"))),
		APIPort:              getenvDefault("API_PORT", "8443"),
		DatabaseURLMigrator:  os.Getenv("DATABASE_URL_MIGRATOR"),
		DatabaseURLRuntime:   os.Getenv("DATABASE_URL_RUNTIME"),
		PortalAllowedOrigins: parseList(os.Getenv("PORTAL_ALLOWED_ORIGINS")),
		TLS: TLS{
			Enabled:    tlsEnabled,
			MinVersion: getenvDefault("TLS_MIN_VERSION", "1.2"),
			CertFile:   os.Getenv("TLS_CERT_FILE"),
			KeyFile:    os.Getenv("TLS_KEY_FILE"),
		},
		Auth: Auth{
			JWKSURL: os.Getenv("AUTH_JWKS_URL"),
			// Trim a trailing slash so a config typo doesn't silently mismatch the
			// token's `iss` (which Supabase mints without one) and 401 everything.
			Issuer:          strings.TrimRight(os.Getenv("AUTH_ISSUER"), "/"),
			Audience:        getenvDefault("AUTH_AUDIENCE", "authenticated"),
			AllowedDomain:   strings.ToLower(getenvDefault("AUTH_ALLOWED_DOMAIN", "amrita.edu")),
			BootstrapAdmins: parseEmailSet(os.Getenv("AUTH_BOOTSTRAP_ADMINS")),
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
	cardSeal, err := parseCardSealConfig(os.Getenv("CARD_SEAL_ACTIVE_VERSION"), os.Getenv("CARD_SEAL_HMAC_KEYS"))
	if err != nil {
		return nil, err
	}
	cfg.CardSeal = cardSeal
	for _, origin := range cfg.PortalAllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

func parseCardSealConfig(activeRaw, keysRaw string) (CardSeal, error) {
	active := strings.TrimSpace(activeRaw)
	if active == "" {
		return CardSeal{}, fmt.Errorf("config: CARD_SEAL_ACTIVE_VERSION is required")
	}
	keys := map[string][]byte{}
	for _, part := range strings.Split(keysRaw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		version, encoded, ok := strings.Cut(entry, "=")
		version = strings.TrimSpace(version)
		encoded = strings.TrimSpace(encoded)
		if !ok || version == "" || encoded == "" {
			return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS must contain comma-separated version=base64url-key entries")
		}
		if _, exists := keys[version]; exists {
			return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS contains duplicate version %q", version)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS contains malformed base64url key for version %q", version)
		}
		if len(decoded) < 32 {
			return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS key for version %q must decode to at least 32 bytes", version)
		}
		keys[version] = decoded
	}
	if len(keys) == 0 {
		return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS is required")
	}
	if _, ok := keys[active]; !ok {
		return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS must include active version %q", active)
	}
	return CardSeal{ActiveVersion: active, HMACKeys: keys}, nil
}

func validateOrigin(raw string) error {
	if strings.Contains(raw, "*") {
		return fmt.Errorf("config: PORTAL_ALLOWED_ORIGINS must contain exact origins, got %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("config: PORTAL_ALLOWED_ORIGINS contains invalid origin %q", raw)
	}
	return nil
}

func parseList(raw string) []string {
	var values []string
	seen := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
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

// parseEmailSet parses a comma-separated env value into a set of lowercased,
// trimmed emails, dropping blank entries. It is fail-soft by design: an empty
// or all-blank value yields an empty (non-nil) set — a valid "no bootstrap
// admins" configuration — mirroring the auth block's development posture (an
// absent list is not an error, it just grants no one admin at provisioning).
func parseEmailSet(raw string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		email := strings.ToLower(strings.TrimSpace(part))
		if email != "" {
			set[email] = struct{}{}
		}
	}
	return set
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
