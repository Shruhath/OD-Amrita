package config

import (
	"strings"
	"testing"
)

// AUTH_ALLOWED_PROVIDERS defaults to Microsoft sign-in only.
func TestLoad_AllowedProvidersDefaultsToAzure(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":   "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":            "false",
		"AUTH_ALLOWED_PROVIDERS": "",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Auth.AllowedProviders) != 1 {
		t.Fatalf("want exactly the default provider, got %v", cfg.Auth.AllowedProviders)
	}
	if _, ok := cfg.Auth.AllowedProviders["azure"]; !ok {
		t.Errorf("default allowed provider must be azure, got %v", cfg.Auth.AllowedProviders)
	}
}

// The list is comma-separated, trimmed and lowercased, so the verifier's
// case-insensitive match holds by construction.
func TestLoad_AllowedProvidersParsesAList(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":   "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":            "false",
		"AUTH_ALLOWED_PROVIDERS": " Azure , google,, ",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{"azure", "google"} {
		if _, ok := cfg.Auth.AllowedProviders[want]; !ok {
			t.Errorf("want %q in %v", want, cfg.Auth.AllowedProviders)
		}
	}
	if len(cfg.Auth.AllowedProviders) != 2 {
		t.Errorf("blank entries must be dropped, got %v", cfg.Auth.AllowedProviders)
	}
}

// A value that names nothing is refused at boot rather than producing a verifier
// that silently rejects every login.
func TestLoad_AllowedProvidersMustNameOne(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":   "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":            "false",
		"AUTH_ALLOWED_PROVIDERS": " , ",
	})

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "AUTH_ALLOWED_PROVIDERS") {
		t.Fatalf("want an AUTH_ALLOWED_PROVIDERS error, got %v", err)
	}
}
