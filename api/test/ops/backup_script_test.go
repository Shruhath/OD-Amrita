package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Suite A — api/scripts/pg_backup.sh
//
// The CLI contract these tests pin down (dev implements to this):
//
//	pg_backup.sh [--dry-run] [--help]
//
//	Env:
//	  BACKUP_DATABASE_URL   required. Postgres DSN to dump.
//	                        Falls back to DATABASE_URL_MIGRATOR (the owner role —
//	                        pg_dump needs read across every object).
//	  BACKUP_DIR            default ./backups. Local staging dir for the artifact.
//	  RETENTION_DAYS        default 90. Prune dumps older than this (Decision D2).
//	  BACKUP_UPLOAD_CMD     optional. Pluggable upload; receives the dump path as $1.
//
//	Behavior:
//	  - `set -euo pipefail`
//	  - dump:     pg_dump -Fc "$DSN" -f "$BACKUP_DIR/od-amrita-<UTC>.dump"
//	  - filename: od-amrita-YYYYMMDDTHHMMSSZ.dump  (UTC, sortable, collision-free)
//	  - upload:   "$BACKUP_UPLOAD_CMD" "$dump_path"   (when set)
//	  - prune:    delete artifacts older than RETENTION_DAYS
//	  - --dry-run prints the plan and exits 0 WITHOUT invoking pg_dump, without
//	    uploading, and without deleting or writing anything
//	  - missing DSN  → non-zero exit + actionable stderr
//	  - unknown flag → non-zero exit (fail closed)
//
// NOTE: --dry-run must not *invoke* pg_dump. Merely probing for its presence
// (`command -v`) is tolerated — that is what keeps this suite runnable in api-ci
// with no Postgres client binaries installed.
// ─────────────────────────────────────────────────────────────────────────────

// scriptPath returns the absolute path to pg_backup.sh, failing with RED-phase
// context when it has not been written yet.
func scriptPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(repoRoot(t), backupScriptRel)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("[P0] %s does not exist yet — the story's 90-day recoverability AC needs a runnable pg_dump wrapper\n  (RED phase: create it to turn this suite green)\n  underlying error: %v", backupScriptRel, err)
	}
	return path
}

// sabotageDir builds a directory of executable shims for every command that would
// touch the database or the outside world, and puts it first on PATH. Each shim
// records its own invocation in a sentinel file and exits non-zero.
//
// This is how "--dry-run performs no writes" is proven rather than assumed: if the
// script actually shells out to pg_dump (or an uploader) during a dry run, the
// sentinel appears and the test fails. Grepping the source could not establish this.
func sabotageDir(t *testing.T) (dir, sentinel string) {
	t.Helper()

	dir = t.TempDir()
	sentinel = filepath.Join(t.TempDir(), "invoked.log")

	for _, cmd := range []string{"pg_dump", "aws", "rclone", "gsutil", "s3cmd"} {
		shim := "#!/usr/bin/env bash\necho \"$0 $*\" >> \"" + sentinel + "\"\nexit 99\n"
		if err := os.WriteFile(filepath.Join(dir, cmd), []byte(shim), 0o755); err != nil {
			t.Fatalf("write %s shim: %v", cmd, err)
		}
	}
	return dir, sentinel
}

// runScript executes pg_backup.sh with a controlled environment and returns its
// combined stdout, stderr and exit code.
func runScript(t *testing.T, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.Command("bash", append([]string{scriptPath(t)}, args...)...)
	cmd.Env = env

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	code = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s %v: %v", backupScriptRel, args, err)
	}
	return outBuf.String(), errBuf.String(), code
}

// dryRunEnv returns an environment with a valid DSN, a scratch backup dir, and the
// sabotage shims first on PATH.
func dryRunEnv(t *testing.T, shimDir, sentinel, backupDir string) []string {
	t.Helper()

	return []string{
		"PATH=" + shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"SENTINEL=" + sentinel,
		// A syntactically valid DSN that must never be dialed: dry-run prints, never connects.
		"BACKUP_DATABASE_URL=postgresql://od_migrator@localhost:5432/od_amrita",
		"BACKUP_DIR=" + backupDir,
	}
}

// A1 [P0] The script exists and is executable. Foundation for every other scenario.
func TestBackupScript_ExistsAndIsExecutable(t *testing.T) {
	info, err := os.Stat(scriptPath(t))
	if err != nil {
		t.Fatalf("[P0] stat %s: %v", backupScriptRel, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("[P0] %s is not executable (mode %v) — an operator-facing script that needs `chmod +x` before it runs is a trap", backupScriptRel, info.Mode().Perm())
	}
}

// A2 [P0] --dry-run exits 0 and invokes NOTHING: no pg_dump, no uploader, no writes
// into the backup directory.
//
// This is the assertion that makes --dry-run trustworthy. A dry-run flag that still
// shells out is worse than no flag at all, because operators will reach for it to
// rehearse against production.
func TestBackupScript_DryRun_InvokesNothingAndWritesNothing(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)
	backupDir := t.TempDir()

	stdout, stderr, code := runScript(t, dryRunEnv(t, shimDir, sentinel, backupDir), "--dry-run")

	if code != 0 {
		t.Errorf("[P0] --dry-run must exit 0, got %d\n  stdout: %s\n  stderr: %s", code, stdout, stderr)
	}
	if _, err := os.Stat(sentinel); err == nil {
		blame, _ := os.ReadFile(sentinel)
		t.Errorf("[P0] --dry-run actually invoked an external command — it must only print the plan.\n  invoked: %s", strings.TrimSpace(string(blame)))
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read BACKUP_DIR: %v", err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("[P0] --dry-run wrote %d file(s) into BACKUP_DIR; it must write nothing: %v", len(entries), names)
	}
}

// A3 [P0] SECURITY: no credential is baked into the script.
//
// The rule is absolute and deliberately simple: the source must contain no DSN
// literal at all. Help text must *describe* the DSN env var, never *exemplify* a
// connection string — the moment an example DSN is in the file, the next person
// edits it into a real one and commits it.
func TestBackupScript_ContainsNoHardcodedCredentials(t *testing.T) {
	src := readArtifact(t, backupScriptRel, "the backup script is the story's one code artifact")

	forbidden := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"a Postgres DSN literal (use the BACKUP_DATABASE_URL env var; describe it, don't exemplify it)", regexp.MustCompile(`(?i)postgres(ql)?://`)},
		{"a literal PGPASSWORD assignment (pass the secret via the DSN from env)", regexp.MustCompile(`PGPASSWORD=[^$\s"']`)},
		{"an AWS access key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
		{"a PEM private key block", regexp.MustCompile(`BEGIN [A-Z ]*PRIVATE KEY`)},
	}
	for _, f := range forbidden {
		if loc := f.re.FindString(src); loc != "" {
			t.Errorf("[P0] %s contains %s — found %q. Never commit a secret; the script reads its DSN from the environment.", backupScriptRel, f.name, loc)
		}
	}
}

// A4 [P0] With no DSN in the environment the script FAILS CLOSED: non-zero exit and
// an error that names the variable to set.
//
// The alternative — exiting 0 having dumped nothing — is the canonical invisible
// backup failure: green cron, empty bucket, discovered during an incident.
func TestBackupScript_MissingDSN_FailsClosed(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)

	env := []string{
		"PATH=" + shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"SENTINEL=" + sentinel,
		"BACKUP_DIR=" + t.TempDir(),
		// deliberately: no BACKUP_DATABASE_URL, no DATABASE_URL_MIGRATOR
	}
	stdout, stderr, code := runScript(t, env)

	if code == 0 {
		t.Errorf("[P0] with no DSN set the script exited 0 — it must fail closed rather than silently back up nothing\n  stdout: %s", stdout)
	}
	if !containsAny(stdout+stderr, "BACKUP_DATABASE_URL") {
		t.Errorf("[P0] the missing-DSN error must name the variable the operator has to set (BACKUP_DATABASE_URL)\n  stdout: %s\n  stderr: %s", stdout, stderr)
	}
}

// A5 [P1] The planned dump uses pg_dump's custom format (-Fc).
//
// Custom format is what makes selective `pg_restore` possible. A plain-SQL dump
// quietly forecloses the restore path the runbook promises Amrita IT.
func TestBackupScript_DryRunPlan_UsesCustomFormatDump(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)
	plan, stderr, _ := runScript(t, dryRunEnv(t, shimDir, sentinel, t.TempDir()), "--dry-run")
	plan += stderr

	if !containsAny(plan, "pg_dump") {
		t.Errorf("[P1] --dry-run plan must show the pg_dump command it would run\n  plan: %s", plan)
	}
	if !containsAny(plan, "-Fc", "--format=custom", "--format custom") {
		t.Errorf("[P1] --dry-run plan must dump in custom format (-Fc) so pg_restore can do a selective restore\n  plan: %s", plan)
	}
}

// A6 [P1] The dump filename carries a UTC timestamp.
//
// Without one, every run overwrites the last and "90 days of snapshots" collapses
// into exactly one snapshot.
func TestBackupScript_DryRunPlan_TimestampsTheArtifact(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)
	plan, stderr, _ := runScript(t, dryRunEnv(t, shimDir, sentinel, t.TempDir()), "--dry-run")
	plan += stderr

	// Contract: od-amrita-YYYYMMDDTHHMMSSZ.dump
	stamp := regexp.MustCompile(`\d{8}T\d{6}Z`)
	if !stamp.MatchString(plan) {
		t.Errorf("[P1] --dry-run plan must name a UTC-timestamped artifact (YYYYMMDDTHHMMSSZ) so successive runs never overwrite each other\n  plan: %s", plan)
	}
	if !containsAny(plan, ".dump") {
		t.Errorf("[P1] --dry-run plan must name a .dump artifact\n  plan: %s", plan)
	}
	if got := stamp.FindString(plan); got != "" {
		if _, err := time.Parse("20060102T150405Z", got); err != nil {
			t.Errorf("[P1] artifact timestamp %q is not a valid UTC instant: %v", got, err)
		}
	}
}

// A7 [P0] Retention is 90 days — Decision D2, the reinterpretation of NFR29.
//
// "90-day PITR" is unachievable on Neon or Supabase (both cap at 30d). The story's
// whole resolution is PITR-at-host-max PLUS 90-day pg_dump snapshots. If this
// constant drifts, that resolution is void and the NFR is silently unmet.
func TestBackupScript_Retains90Days(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)
	plan, stderr, _ := runScript(t, dryRunEnv(t, shimDir, sentinel, t.TempDir()), "--dry-run")
	plan += stderr

	if !containsAny(plan, "90") {
		t.Errorf("[P0] --dry-run plan must state the 90-day retention it would enforce (D2: PITR@host-max + pg_dump@90d is what satisfies NFR29)\n  plan: %s", plan)
	}
	if !containsAny(plan, "prune", "retention", "older than", "delete") {
		t.Errorf("[P0] --dry-run plan must describe the prune step (delete artifacts older than RETENTION_DAYS)\n  plan: %s", plan)
	}
}

// A8 [P1] An unrecognized flag fails closed.
//
// Silently ignoring an unknown flag means a typo'd `--dry-run` becomes a REAL run
// against production. Fail loudly instead.
func TestBackupScript_UnknownFlag_FailsClosed(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)
	backupDir := t.TempDir()

	stdout, stderr, code := runScript(t, dryRunEnv(t, shimDir, sentinel, backupDir), "--dry-rnu")

	if code == 0 {
		t.Errorf("[P0] an unknown flag must be rejected with a non-zero exit — a mistyped --dry-run silently becoming a real run is exactly the accident this guards\n  stdout: %s", stdout)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Errorf("[P0] the script invoked an external command while rejecting an unknown flag; it must refuse before doing any work")
	}
	if !containsAny(stdout+stderr, "usage", "unknown", "unrecognized", "invalid") {
		t.Errorf("[P1] rejecting an unknown flag should print usage or an explanatory error\n  stdout: %s\n  stderr: %s", stdout, stderr)
	}
}

// A9 [P1] The script enables strict bash mode.
//
// Without `set -euo pipefail` a failed pg_dump lets the script sail on to upload and
// prune — publishing a truncated artifact, deleting older good ones, and exiting 0.
// That turns a backup system into a data-destruction system.
func TestBackupScript_UsesStrictBashMode(t *testing.T) {
	src := readArtifact(t, backupScriptRel, "the backup script is the story's one code artifact")

	if !regexp.MustCompile(`set\s+-[a-z]*e`).MatchString(src) {
		t.Errorf("[P1] %s must `set -e` (prefer `set -euo pipefail`) — otherwise a failed dump still uploads and prunes, then exits 0", backupScriptRel)
	}
	if !containsAny(src, "pipefail") {
		t.Errorf("[P1] %s must `set -o pipefail` — a failure inside a pipeline (e.g. pg_dump | gzip) must not be masked by the last command's success", backupScriptRel)
	}
	if !containsAny(src, "set -u", "set -euo", "nounset") {
		t.Errorf("[P1] %s must `set -u` — an unset BACKUP_DIR must not expand to empty and turn a prune into a wildcard delete", backupScriptRel)
	}
}

// A10 [P2] --help works and documents the operator-facing surface.
func TestBackupScript_Help_DocumentsUsage(t *testing.T) {
	shimDir, sentinel := sabotageDir(t)
	stdout, stderr, code := runScript(t, dryRunEnv(t, shimDir, sentinel, t.TempDir()), "--help")
	help := stdout + stderr

	if code != 0 {
		t.Errorf("[P2] --help must exit 0, got %d", code)
	}
	for _, want := range []string{"BACKUP_DATABASE_URL", "RETENTION_DAYS", "--dry-run"} {
		if !containsAny(help, want) {
			t.Errorf("[P2] --help must document %s\n  help: %s", want, help)
		}
	}
	if !containsAny(help, "cron", "schedule") {
		t.Errorf("[P2] --help must say how to schedule the script (cron/CI) — the runbook points operators here\n  help: %s", help)
	}
}
