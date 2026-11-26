package config

import (
	"strings"
	"testing"
)

// productionEnv is the smallest environment Load accepts with APP_ENV=production.
func productionEnv(runtimeDSN, migratorDSN string) map[string]string {
	return map[string]string{
		"APP_ENV":               "production",
		"DATABASE_URL_RUNTIME":  runtimeDSN,
		"DATABASE_URL_MIGRATOR": migratorDSN,
		"TLS_ENABLED":           "false",
		"AUTH_JWKS_URL":         "https://auth.example.test/auth/v1/.well-known/jwks.json",
		"AUTH_ISSUER":           "https://auth.example.test/auth/v1",
	}
}

const strongRuntimeDSN = "postgres://od_runtime:Zq7-unique-runtime-secret@db.example.test:5432/od_amrita?sslmode=require"

func TestLoad_ProductionRejectsCommittedPlaceholderPasswords(t *testing.T) {
	t.Setenv("PGPASSWORD", "")
	cases := []struct {
		name     string
		runtime  string
		migrator string
		wantVar  string
		secret   string
	}{
		{"runtime role placeholder", "postgres://od_runtime:runtime_dev_only_change_me@db.example.test/od_amrita", "", "DATABASE_URL_RUNTIME", "runtime_dev_only_change_me"},
		{"migrator role placeholder", strongRuntimeDSN, "postgres://od_migrator:migrator_dev_only_change_me@db.example.test/od_amrita", "DATABASE_URL_MIGRATOR", "migrator_dev_only_change_me"},
		{"compose superuser placeholder", "postgres://od_amrita:dev_only_change_me@db.example.test/od_amrita", "", "DATABASE_URL_RUNTIME", "dev_only_change_me"},
		{"studio placeholder", "postgres://od_runtime:studio_dev_only_change_me@db.example.test/od_amrita", "", "DATABASE_URL_RUNTIME", "studio_dev_only_change_me"},
		{"env example CHANGE_ME", "postgres://od_runtime:CHANGE_ME@db.example.test/od_amrita", "", "DATABASE_URL_RUNTIME", "CHANGE_ME"},
		{"CI password", strongRuntimeDSN, "postgres://od_migrator:ci_only_password@db.example.test/od_amrita", "DATABASE_URL_MIGRATOR", "ci_only_password"},
		{"keyword/value form", "host=db.example.test user=od_runtime password=runtime_dev_only_change_me dbname=od_amrita", "", "DATABASE_URL_RUNTIME", "runtime_dev_only_change_me"},
		{"case variation", "postgres://od_runtime:Runtime_Dev_Only_Change_Me@db.example.test/od_amrita", "", "DATABASE_URL_RUNTIME", "Runtime_Dev_Only_Change_Me"},
		{"unparseable DSN still scanned", "postgres://od_runtime:runtime_dev_only_change_me@db.example.test:notaport/od_amrita", "", "DATABASE_URL_RUNTIME", "runtime_dev_only_change_me"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, productionEnv(tc.runtime, tc.migrator))
			_, err := Load()
			if err == nil {
				t.Fatal("expected Load to refuse a committed placeholder password in production")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantVar) {
				t.Errorf("error must name %s, got: %v", tc.wantVar, err)
			}
			if strings.Contains(msg, tc.secret) || strings.Contains(msg, "db.example.test") || strings.Contains(msg, "postgres://") {
				t.Errorf("error must not contain the DSN or its password, got: %v", err)
			}
		})
	}
}

func TestLoad_ProductionRejectsPlaceholderFromPGPASSWORD(t *testing.T) {
	// pgx falls back to PGPASSWORD when the DSN carries no password, so that is
	// the password the pool would actually use.
	setEnv(t, productionEnv("postgres://od_runtime@db.example.test/od_amrita", ""))
	t.Setenv("PGPASSWORD", "runtime_dev_only_change_me")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to refuse a placeholder supplied through PGPASSWORD")
	}
}

func TestLoad_ProductionAcceptsRealPasswords(t *testing.T) {
	t.Setenv("PGPASSWORD", "")
	setEnv(t, productionEnv(strongRuntimeDSN, "postgres://od_migrator:another-unique-secret@db.example.test/od_amrita"))
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoad_PlaceholderPasswordsAllowedOutsideProduction(t *testing.T) {
	t.Setenv("PGPASSWORD", "")
	for _, appEnv := range []string{"development", "test", "ci"} {
		t.Run(appEnv, func(t *testing.T) {
			env := productionEnv(
				"postgres://od_runtime:runtime_dev_only_change_me@localhost:5432/od_amrita?sslmode=disable",
				"postgres://od_migrator:migrator_dev_only_change_me@localhost:5432/od_amrita?sslmode=disable",
			)
			env["APP_ENV"] = appEnv
			setEnv(t, env)
			if _, err := Load(); err != nil {
				t.Fatalf("Load (%s) must accept the local placeholders: %v", appEnv, err)
			}
		})
	}
}
