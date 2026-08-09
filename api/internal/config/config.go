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

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
)

// TLS holds the transport-security settings (NFR15: the server is TLS-only —
// plain HTTP is rejected, never redirected).
type TLS struct {
	// Enabled serves TLS directly. False is only for ingress-terminated
	// deployments where the platform enforces TLS (and must also reject, not
	// redirect, plain HTTP).
	Enabled    bool
	TrustedProxyTermination bool
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

	// PublicRateLimit bounds the UNAUTHENTICATED public dashboard endpoints
	// (Story 0.10). Both fields have working defaults on purpose: the deployed
	// Fly app carries no rate-limit secret, and requiring one would take the API
	// down at the next deploy. Tuning them live is the venue-day lever — a
	// `flyctl secrets set` rather than a redeploy.
	PublicRateLimit PublicRateLimit
	// PublicDisputeRateLimit bounds the unauthenticated dispute POST only. It is
	// intentionally stricter than the public read budget.
	PublicDisputeRateLimit PublicRateLimit
	// PublicCacheTTL is how long each public read may be served from memory.
	// Defaults apply when unset, for the same reason the rate limits have them:
	// the deployed app carries no such secret and must keep booting.
	PublicCacheTTL PublicCacheTTL
}

// PublicCacheTTL is the lifetime of each cached public read, in seconds on the
// wire. Zero switches that cache off, which is the live escape hatch if a stale
// record ever matters more than a database read.
type PublicCacheTTL struct {
	// RecordsSeconds is the one a student is actively watching: it bounds how
	// long "I was just marked" can still read as "no OD". Shortest by far.
	RecordsSeconds int
	// SearchSeconds covers the roster, which changes only on an import.
	SearchSeconds int
	// EventsSeconds covers the event and category list, which changes only when
	// an administrator edits it.
	EventsSeconds int
}

// PublicRateLimit is the token-bucket tuning for the public group.
type PublicRateLimit struct {
	// PerMinute is the sustained per-client refill rate.
	PerMinute int
	// Burst is the bucket capacity — how many requests a client may make back
	// to back. A debounced search plus a records fetch must fit comfortably.
	Burst int
}

// Load reads the environment and fails fast on a configuration the server
// must not start with (missing runtime DSN, invalid port, a malformed boolean,
// TLS enabled without a readable cert/key or with an unknown min version, or
// any attempt to configure the forbidden HTTP redirect listener, or production
// plaintext without an explicit trusted-ingress declaration).
func Load() (*Config, error) {
	tlsEnabled, err := parseBoolStrict("TLS_ENABLED", true)
	if err != nil {
		return nil, err
	}
	redirectListener, err := parseBoolStrict("HTTP_REDIRECT_LISTENER", false)
	if err != nil {
		return nil, err
	}
	trustedProxyTermination, err := parseBoolStrict("TLS_TERMINATED_BY_TRUSTED_PROXY", false)
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
			TrustedProxyTermination: trustedProxyTermination,
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
	if !cfg.TLS.Enabled && cfg.AppEnv != "development" && !cfg.TLS.TrustedProxyTermination {
		return nil, fmt.Errorf("config: TLS_ENABLED=false requires TLS_TERMINATED_BY_TRUSTED_PROXY=true outside development")
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
	publicRateLimit, err := parsePublicRateLimit()
	if err != nil {
		return nil, err
	}
	cfg.PublicRateLimit = publicRateLimit
	publicDisputeRateLimit, err := parsePublicDisputeRateLimit()
	if err != nil {
		return nil, err
	}
	cfg.PublicDisputeRateLimit = publicDisputeRateLimit
	publicCacheTTL, err := parsePublicCacheTTL()
	if err != nil {
		return nil, err
	}
	cfg.PublicCacheTTL = publicCacheTTL
	if err := validateAuth(cfg.Auth, cfg.AppEnv); err != nil {
		return nil, err
	}
	cardSeal, err := LoadCardSealFromEnv()
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

// parsePublicRateLimit reads the public-surface limiter tuning.
//
// UNSET IS VALID and yields the defaults — that is what lets the current Fly
// deployment keep booting without a new secret. A SET-BUT-GARBAGE value is not:
// a typo that silently fell back to the default would leave an operator raising
// a cap that never moved, on the one day they need it to.
func parsePublicRateLimit() (PublicRateLimit, error) {
	perMinute, err := positiveIntDefault("PUBLIC_RATE_LIMIT_PER_MINUTE", ratelimit.DefaultPerMinute)
	if err != nil {
		return PublicRateLimit{}, err
	}
	burst, err := positiveIntDefault("PUBLIC_RATE_LIMIT_BURST", ratelimit.DefaultBurst)
	if err != nil {
		return PublicRateLimit{}, err
	}
	return PublicRateLimit{PerMinute: perMinute, Burst: burst}, nil
}

func parsePublicDisputeRateLimit() (PublicRateLimit, error) {
	perMinute, err := positiveIntDefault("PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE", 5)
	if err != nil {
		return PublicRateLimit{}, err
	}
	burst, err := positiveIntDefault("PUBLIC_DISPUTE_RATE_LIMIT_BURST", 3)
	if err != nil {
		return PublicRateLimit{}, err
	}
	return PublicRateLimit{PerMinute: perMinute, Burst: burst}, nil
}

// parsePublicCacheTTL reads the public read-cache lifetimes.
//
// Unset yields the defaults, for the same reason the rate limits have them: the
// deployed app carries no such secret and must keep booting. Unlike those, ZERO
// is a legal value here and means "do not cache this read" — the switch an
// operator reaches for on the day if a stale record turns out to matter more
// than a database round trip. A set-but-garbage value still fails loudly: a typo
// that silently fell back to a default would leave someone believing they had
// turned caching off when they had not.
func parsePublicCacheTTL() (PublicCacheTTL, error) {
	records, err := nonNegativeIntDefault("PUBLIC_CACHE_RECORDS_TTL_SECONDS", 30)
	if err != nil {
		return PublicCacheTTL{}, err
	}
	search, err := nonNegativeIntDefault("PUBLIC_CACHE_SEARCH_TTL_SECONDS", 60)
	if err != nil {
		return PublicCacheTTL{}, err
	}
	events, err := nonNegativeIntDefault("PUBLIC_CACHE_EVENTS_TTL_SECONDS", 300)
	if err != nil {
		return PublicCacheTTL{}, err
	}
	return PublicCacheTTL{RecordsSeconds: records, SearchSeconds: search, EventsSeconds: events}, nil
}

// nonNegativeIntDefault is positiveIntDefault with zero admitted, for settings
// where zero means "off" rather than "misconfigured".
func nonNegativeIntDefault(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("config: %s must be a non-negative integer, got %q", key, raw)
	}
	// An hour is far beyond any legitimate value for these and is much more
	// likely to be milliseconds pasted into a seconds field.
	if value > 3_600 {
		return 0, fmt.Errorf("config: %s must be at most 3600 seconds, got %q", key, raw)
	}
	return value, nil
}

// positiveIntDefault returns def when the variable is absent or blank, and
// fails when it is present but not a positive integer.
func positiveIntDefault(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("config: %s must be a positive integer, got %q", key, raw)
	}
	if value > 10_000 {
		return 0, fmt.Errorf("config: %s must be at most 10000, got %q", key, raw)
	}
	return value, nil
}

// LoadCardSealFromEnv loads only the card-seal keyring configuration. Test
// harnesses that compose the API without the full config still use the same
// server-only env contract and validation path as production.
func LoadCardSealFromEnv() (CardSeal, error) {
	return parseCardSealConfig(os.Getenv("CARD_SEAL_ACTIVE_VERSION"), os.Getenv("CARD_SEAL_HMAC_KEYS"))
}

func parseCardSealConfig(activeRaw, keysRaw string) (CardSeal, error) {
	active := strings.TrimSpace(activeRaw)
	if active == "" {
		return CardSeal{}, fmt.Errorf("config: CARD_SEAL_ACTIVE_VERSION is required")
	}
	if len(active) > 16 {
		return CardSeal{}, fmt.Errorf("config: CARD_SEAL_ACTIVE_VERSION must not exceed 16 characters")
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
		if len(version) > 16 {
			return CardSeal{}, fmt.Errorf("config: CARD_SEAL_HMAC_KEYS contains version %q longer than 16 characters", version)
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
