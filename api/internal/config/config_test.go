package config

import (
	"strings"
	"testing"
)

// setEnv applies a full, isolated environment for one Load call.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	// Clear every key Load reads, then apply the case's values.
	for _, k := range []string{
		"APP_ENV", "API_PORT",
		"DATABASE_URL_MIGRATOR", "DATABASE_URL_RUNTIME",
		"TLS_ENABLED", "TLS_MIN_VERSION", "TLS_CERT_FILE", "TLS_KEY_FILE",
		"HTTP_REDIRECT_LISTENER",
	} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
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
	setEnv(t, map[string]string{
		"APP_ENV":               "production",
		"API_PORT":              "9443",
		"DATABASE_URL_MIGRATOR": "postgres://od_migrator:x@localhost:5432/od_amrita",
		"DATABASE_URL_RUNTIME":  "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":           "true",
		"TLS_MIN_VERSION":       "1.3",
		"TLS_CERT_FILE":         "/certs/server.crt",
		"TLS_KEY_FILE":          "/certs/server.key",
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
	if !cfg.TLS.Enabled || cfg.TLS.CertFile != "/certs/server.crt" || cfg.TLS.KeyFile != "/certs/server.key" {
		t.Errorf("TLS settings not honored: %+v", cfg.TLS)
	}
	if cfg.TLS.MinVersion != "1.3" {
		t.Errorf("TLS_MIN_VERSION not honored: %q", cfg.TLS.MinVersion)
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
