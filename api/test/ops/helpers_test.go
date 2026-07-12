// Package ops is the acceptance harness for the operational artifacts that make
// the database recoverable and the service observable: the logical-backup script
// (api/scripts/pg_backup.sh) and the operator runbook
// (docs/backup-and-monitoring-runbook.md).
//
// Why these are tested at all, given neither is Go code:
//
// A backup you never exercised is not a backup — it is a belief. The failure mode
// this suite exists to prevent is the invisible one: a script that exits 0 while
// dumping nothing, a dump aimed at the wrong database, or a restore procedure that
// omits the one step without which the restored database refuses every connection.
// Each of those looks like success right up to the moment the immutable audit
// ledger is needed back, which is the moment it is too late to find out.
//
// These tests are deliberately UNTAGGED: they need no database, no network and no
// Postgres client binaries, so they run inside the existing `go test ./...` gate in
// api-ci with no new toolchain and no new CI wiring.
//
//	go test ./test/ops/...
//
// What they can and cannot prove: they prove the runbook exists, is structured,
// names the right database and leaks no credentials. They CANNOT prove its prose is
// correct or that the procedure works. The real acceptance test is the restore drill
// — an operator restoring into a throwaway DB and confirming the app boots. This
// suite protects that drill's preconditions; it does not replace it.
package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Paths of the artifacts under test, relative to the repository root.
const (
	backupScriptRel = "api/scripts/pg_backup.sh"
	runbookRel      = "docs/backup-and-monitoring-runbook.md"
)

// repoRoot walks up from the test's working directory (api/test/ops) until it finds
// the go.work that marks the monorepo root. The artifacts under test straddle the
// module boundary — the script lives inside api/, the runbook does not — so paths
// are anchored at the repo root rather than the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root (no go.work found walking up from the test dir)")
		}
		dir = parent
	}
}

// readArtifact returns the contents of a repo-root-relative path, failing the test
// with the acceptance-criterion context when the artifact has not been created yet.
// This is the RED-phase entry point: today both artifacts are absent and every test
// in this package fails here, by design.
func readArtifact(t *testing.T, rel, why string) string {
	t.Helper()

	path := filepath.Join(repoRoot(t), rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("[P0] %s does not exist yet — %s\n  (RED phase: create it to turn this suite green)\n  underlying error: %v", rel, why, err)
	}
	return string(b)
}

// containsAny reports whether haystack contains any of the given needles,
// case-insensitively.
func containsAny(haystack string, needles ...string) bool {
	lower := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// containsName reports whether names contains an exact match for want.
func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
