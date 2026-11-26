package config

import "testing"

// TestLoad_BootstrapAdmins — AUTH_BOOTSTRAP_ADMINS parses into a lowercased set,
// trimming whitespace and dropping blank entries.
func TestLoad_BootstrapAdmins(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME":  "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":           "false",
		"AUTH_BOOTSTRAP_ADMINS": " First.Admin@Amrita.edu,second.admin@amrita.edu ,, ",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("AUTH_BOOTSTRAP_ADMINS should be optional/fail-soft: %v", err)
	}
	if len(cfg.Auth.BootstrapAdmins) != 2 {
		t.Fatalf("expected two parsed bootstrap admins, got %#v", cfg.Auth.BootstrapAdmins)
	}
	for _, email := range []string{"first.admin@amrita.edu", "second.admin@amrita.edu"} {
		if _, ok := cfg.Auth.BootstrapAdmins[email]; !ok {
			t.Errorf("expected lowercased bootstrap admin %q in set %#v", email, cfg.Auth.BootstrapAdmins)
		}
	}
}

// TestLoad_BootstrapAdminsEmptyByDefault — with the variable unset the set is
// empty (a valid "no bootstrap admins" config), so every real login provisions
// as the least-privileged student until an admin elevates them.
func TestLoad_BootstrapAdminsEmptyByDefault(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Auth.BootstrapAdmins) != 0 {
		t.Fatalf("bootstrap admins must be empty by default, got %#v", cfg.Auth.BootstrapAdmins)
	}
}
