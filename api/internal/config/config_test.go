package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
)

// writeTempCertKey creates readable placeholder cert/key files and returns their
// paths — the config layer only checks readability, not X.509 validity.
func writeTempCertKey(t *testing.T) (cert, key string) {
	t.Helper()
	dir := t.TempDir()
	cert = filepath.Join(dir, "server.crt")
	key = filepath.Join(dir, "server.key")
	for _, p := range []string{cert, key} {
		if err := os.WriteFile(p, []byte("placeholder"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return cert, key
}

// setEnv applies a full, isolated environment for one Load call.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	// Clear every key Load reads, then apply the case's values.
	for _, k := range []string{
		"APP_ENV", "API_PORT",
		"DATABASE_URL_MIGRATOR", "DATABASE_URL_RUNTIME",
		"TLS_ENABLED", "TLS_MIN_VERSION", "TLS_CERT_FILE", "TLS_KEY_FILE",
		"HTTP_REDIRECT_LISTENER",
		"AUTH_JWKS_URL", "AUTH_ISSUER", "AUTH_AUDIENCE", "AUTH_ALLOWED_DOMAIN",
		"AUTH_BOOTSTRAP_ADMINS",
		"PORTAL_ALLOWED_ORIGINS",
		"CARD_SEAL_ACTIVE_VERSION", "CARD_SEAL_HMAC_KEYS",
		"PUBLIC_RATE_LIMIT_PER_MINUTE", "PUBLIC_RATE_LIMIT_BURST",
	} {
		t.Setenv(k, "")
	}
	if _, ok := env["CARD_SEAL_ACTIVE_VERSION"]; !ok {
		t.Setenv("CARD_SEAL_ACTIVE_VERSION", "dev")
	}
	if _, ok := env["CARD_SEAL_HMAC_KEYS"]; !ok {
		t.Setenv("CARD_SEAL_HMAC_KEYS", "dev="+base64.RawURLEncoding.EncodeToString([]byte("dev-only-card-seal-key-32-bytes!!")))
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoadPortalOriginsRequiresExactHTTPOrigins(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":   "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":            "false",
		"PORTAL_ALLOWED_ORIGINS": "https://portal.example, http://localhost:5173",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.PortalAllowedOrigins) != 2 || cfg.PortalAllowedOrigins[0] != "https://portal.example" {
		t.Fatalf("origins = %#v", cfg.PortalAllowedOrigins)
	}

	for _, invalid := range []string{"*", "https://*.example", "https://portal.example/path", "javascript:alert(1)"} {
		setEnv(t, map[string]string{
			"DATABASE_URL_RUNTIME":   "postgres://od_runtime:x@localhost:5432/od_amrita",
			"TLS_ENABLED":            "false",
			"PORTAL_ALLOWED_ORIGINS": invalid,
		})
		if _, err := Load(); err == nil {
			t.Errorf("Load accepted invalid origin %q", invalid)
		}
	}
}

func TestLoad_RuntimeDSNRequired(t *testing.T) {
	setEnv(t, map[string]string{})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail fast when DATABASE_URL_RUNTIME is missing")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL_RUNTIME") {
		t.Errorf("error must name the missing variable, got: %v", err)
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppEnv != "development" {
		t.Errorf("APP_ENV default: want development, got %q", cfg.AppEnv)
	}
	if cfg.APIPort != "8443" {
		t.Errorf("API_PORT default: want 8443, got %q", cfg.APIPort)
	}
	if cfg.TLS.Enabled {
		t.Error("TLS_ENABLED=false must disable TLS (ingress-terminated mode)")
	}
}

func TestLoad_TLSEnabledRequiresCertAndKey(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "true",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail when TLS is enabled but cert/key are missing")
	}
}

func TestLoad_TLSFullyConfigured(t *testing.T) {
	cert, key := writeTempCertKey(t)
	setEnv(t, map[string]string{
		"APP_ENV":               "production",
		"API_PORT":              "9443",
		"DATABASE_URL_MIGRATOR": "postgres://od_migrator:x@localhost:5432/od_amrita",
		"DATABASE_URL_RUNTIME":  "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":           "true",
		"TLS_MIN_VERSION":       "1.3",
		"TLS_CERT_FILE":         cert,
		"TLS_KEY_FILE":          key,
		// Auth (Supabase JWT) is required outside development, so a fully
		// configured production box must supply it.
		"AUTH_JWKS_URL":       "https://project-ref.supabase.co/auth/v1/.well-known/jwks.json",
		"AUTH_ISSUER":         "https://project-ref.supabase.co/auth/v1",
		"AUTH_AUDIENCE":       "authenticated",
		"AUTH_ALLOWED_DOMAIN": "amrita.edu",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppEnv != "production" || cfg.APIPort != "9443" {
		t.Errorf("explicit APP_ENV/API_PORT not honored: %+v", cfg)
	}
	if cfg.DatabaseURLMigrator == "" || cfg.DatabaseURLRuntime == "" {
		t.Error("both DSNs must be loaded")
	}
	if !cfg.TLS.Enabled || cfg.TLS.CertFile != cert || cfg.TLS.KeyFile != key {
		t.Errorf("TLS settings not honored: %+v", cfg.TLS)
	}
	if cfg.TLS.MinVersion != "1.3" {
		t.Errorf("TLS_MIN_VERSION not honored: %q", cfg.TLS.MinVersion)
	}
	if cfg.Auth.JWKSURL == "" || cfg.Auth.Issuer == "" {
		t.Errorf("auth JWKS/issuer must be loaded in production: %+v", cfg.Auth)
	}
	if cfg.Auth.Audience != "authenticated" {
		t.Errorf("auth audience: want authenticated, got %q", cfg.Auth.Audience)
	}
	if cfg.Auth.AllowedDomain != "amrita.edu" {
		t.Errorf("auth allowed-domain: want amrita.edu, got %q", cfg.Auth.AllowedDomain)
	}
}

// ── Auth (Supabase JWT) config ──
// JWKS + issuer are REQUIRED when APP_ENV != development, so the no-Supabase dev
// box still boots but a real deploy fails fast on missing/malformed auth config.
// If an explicit AUTH_ENABLED flag (TLS-style) is preferred instead, swap the env
// setup in these four tests — the observable behavior (prod requires it, dev doesn't)
// is what is pinned.

// TestLoad_AuthRequiredInProduction — outside development, missing auth config
// must fail fast, naming an AUTH_ variable.
func TestLoad_AuthRequiredInProduction(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":              "production",
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
		// AUTH_* deliberately unset.
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail fast when auth config is missing outside development")
	}
	if !strings.Contains(err.Error(), "AUTH_") {
		t.Errorf("error must name the missing AUTH_ variable, got: %v", err)
	}
}

// TestLoad_AuthInvalidJWKSURL — a malformed JWKS URL fails fast, naming the
// variable (parseable https URL, like the TLS/DSN checks).
func TestLoad_AuthInvalidJWKSURL(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":              "production",
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
		"AUTH_JWKS_URL":        "://not a url",
		"AUTH_ISSUER":          "https://project-ref.supabase.co/auth/v1",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to reject a malformed AUTH_JWKS_URL")
	}
	if !strings.Contains(err.Error(), "AUTH_JWKS_URL") {
		t.Errorf("error must name AUTH_JWKS_URL, got: %v", err)
	}
}

// TestLoad_AuthOptionalInDevelopment — the no-Supabase dev box still boots, and
// the non-secret defaults (audience, allowed-domain) are applied.
func TestLoad_AuthOptionalInDevelopment(t *testing.T) {
	setEnv(t, map[string]string{
		// APP_ENV defaults to development.
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
		// AUTH_* unset.
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("development must boot without Supabase auth config: %v", err)
	}
	if cfg.Auth.Audience != "authenticated" {
		t.Errorf("auth audience default: want authenticated, got %q", cfg.Auth.Audience)
	}
	if cfg.Auth.AllowedDomain != "amrita.edu" {
		t.Errorf("auth allowed-domain default: want amrita.edu, got %q", cfg.Auth.AllowedDomain)
	}
}

// TestLoad_AuthLowercasesAllowedDomain — the allowed domain is normalized to
// lowercase so the per-request family check is case-insensitive by construction.
func TestLoad_AuthLowercasesAllowedDomain(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
		"AUTH_ALLOWED_DOMAIN":  "AMRITA.EDU",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.AllowedDomain != "amrita.edu" {
		t.Errorf("allowed-domain must be lowercased, got %q", cfg.Auth.AllowedDomain)
	}
}

// TLS enabled with a cert/key path that does not exist must fail fast at Load,
// not later at ListenAndServeTLS after the pool is already open.
func TestLoad_TLSCertKeyMustBeReadable(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "true",
		"TLS_CERT_FILE":        "/nope/server.crt",
		"TLS_KEY_FILE":         "/nope/server.key",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail when the cert/key files are not readable")
	}
	if !strings.Contains(err.Error(), "TLS_CERT_FILE") {
		t.Errorf("error must name the unreadable file variable, got: %v", err)
	}
}

// An unknown TLS_MIN_VERSION must be rejected, not silently downgraded to 1.2.
func TestLoad_InvalidTLSMinVersionRejected(t *testing.T) {
	cert, key := writeTempCertKey(t)
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "true",
		"TLS_MIN_VERSION":      "1.4",
		"TLS_CERT_FILE":        cert,
		"TLS_KEY_FILE":         key,
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to reject an unknown TLS_MIN_VERSION")
	}
	if !strings.Contains(err.Error(), "TLS_MIN_VERSION") {
		t.Errorf("error must name TLS_MIN_VERSION, got: %v", err)
	}
}

// A non-numeric / out-of-range API_PORT must fail at Load, naming the variable.
func TestLoad_InvalidPortRejected(t *testing.T) {
	for _, port := range []string{"abc", "0", "99999"} {
		setEnv(t, map[string]string{
			"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
			"TLS_ENABLED":          "false",
			"API_PORT":             port,
		})
		_, err := Load()
		if err == nil {
			t.Fatalf("expected Load to reject API_PORT=%q", port)
		}
		if !strings.Contains(err.Error(), "API_PORT") {
			t.Errorf("API_PORT=%q: error must name the variable, got: %v", port, err)
		}
	}
}

// A malformed boolean in a security-relevant flag must be loud, not silently
// defaulted.
func TestLoad_MalformedBoolRejected(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "flase",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to reject a malformed TLS_ENABLED value")
	}
	if !strings.Contains(err.Error(), "TLS_ENABLED") {
		t.Errorf("error must name the offending variable, got: %v", err)
	}
}

func TestLoad_TLSMinVersionDefault12(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TLS.MinVersion != "1.2" {
		t.Errorf("TLS_MIN_VERSION default: want 1.2, got %q", cfg.TLS.MinVersion)
	}
}

// NFR15: HTTP is rejected, not redirected. The config layer refuses to even
// load a configuration that asks for a redirect listener.
func TestLoad_RedirectListenerForbidden(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":   "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":            "false",
		"HTTP_REDIRECT_LISTENER": "true",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to reject HTTP_REDIRECT_LISTENER=true (NFR15)")
	}
	if !strings.Contains(err.Error(), "HTTP_REDIRECT_LISTENER") {
		t.Errorf("error must name the offending variable, got: %v", err)
	}
}

// The public rate limit must work with NO configuration at all: the deployed
// Fly app carries no such secret, and a limiter that required one would take
// the API down at the next deploy.
func TestLoad_PublicRateLimitDefaultsWhenUnset(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PublicRateLimit.PerMinute != ratelimit.DefaultPerMinute {
		t.Errorf("PerMinute = %d, want the package default %d", cfg.PublicRateLimit.PerMinute, ratelimit.DefaultPerMinute)
	}
	if cfg.PublicRateLimit.Burst != ratelimit.DefaultBurst {
		t.Errorf("Burst = %d, want the package default %d", cfg.PublicRateLimit.Burst, ratelimit.DefaultBurst)
	}
}

func TestLoad_PublicRateLimitOverridesAndRejectsGarbage(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":         "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":                  "false",
		"PUBLIC_RATE_LIMIT_PER_MINUTE": "600",
		"PUBLIC_RATE_LIMIT_BURST":      "120",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PublicRateLimit.PerMinute != 600 || cfg.PublicRateLimit.Burst != 120 {
		t.Fatalf("public rate limit = %+v, want {600 120}", cfg.PublicRateLimit)
	}

	// A SET-but-unusable value must fail fast rather than fall back. An operator
	// raising the cap on event day has to be told their typo did nothing, not
	// left believing a limit moved when it did not.
	for _, invalid := range []string{"0", "-1", "thirty", "30/min", "1.5"} {
		setEnv(t, map[string]string{
			"DATABASE_URL_RUNTIME":         "postgres://od_runtime:x@localhost:5432/od_amrita",
			"TLS_ENABLED":                  "false",
			"PUBLIC_RATE_LIMIT_PER_MINUTE": invalid,
		})
		if _, err := Load(); err == nil {
			t.Errorf("Load accepted PUBLIC_RATE_LIMIT_PER_MINUTE=%q", invalid)
		}
	}
}
