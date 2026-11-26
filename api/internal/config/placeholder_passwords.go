package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// placeholderMarkers identify the database passwords committed to this
// repository for local development and CI: migrator_dev_only_change_me,
// runtime_dev_only_change_me, studio_dev_only_change_me, dev_only_change_me,
// CHANGE_ME and ci_only_password (api/db/init/00_roles.sql,
// api/docker-compose.yml, api/Makefile, api/.env.example, HOWTO.md and the CI
// workflows). They are public, so a production deployment that still uses one
// effectively has no database password. Matching is a case-insensitive
// substring test, which also catches variations ("changeme", another role
// prefix); no real password should contain any of them.
var placeholderMarkers = []string{"dev_only", "change_me", "changeme", "ci_only_password"}

// rejectPlaceholderPasswords refuses to boot production with a DSN whose
// password is one of the committed development placeholders. Outside
// production it does nothing: local development and CI use those values on
// purpose.
//
// Neither the DSN nor the password is ever included in the returned error.
func rejectPlaceholderPasswords(appEnv string, dsns map[string]string) error {
	if appEnv != "production" {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(dsns)) {
		dsn := dsns[name]
		if dsn == "" {
			continue
		}
		if isPlaceholderPassword(dsnPassword(dsn)) {
			return fmt.Errorf("config: %s uses a development placeholder password committed to this repository; set a unique password for that database role before running with APP_ENV=production", name)
		}
	}
	return nil
}

// dsnPassword extracts the password from a URL or keyword/value DSN the way
// pgx will read it (including PGPASSWORD when the DSN has none). For a DSN pgx
// cannot parse it falls back to scanning the whole string, so a malformed value
// cannot slip a placeholder past this check; opening the pool reports the
// syntax error itself.
func dsnPassword(dsn string) string {
	if cfg, err := pgconn.ParseConfig(dsn); err == nil {
		return cfg.Password
	}
	return dsn
}

func isPlaceholderPassword(pw string) bool {
	lower := strings.ToLower(pw)
	for _, m := range placeholderMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
