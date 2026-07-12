package ops

import (
	"fmt"
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
// The CLI contract these tests pin down:
//
//	pg_backup.sh [--dry-run] [--local-only] [--help]
//
//	Env:
//	  BACKUP_DATABASE_URL   required. Postgres DSN to dump. Falls back to
//	                        DATABASE_URL_MIGRATOR only when UNSET; set-but-empty
//	                        is a hard error (a failed secret lookup must not
//	                        silently become "back up some other database").
//	  BACKUP_UPLOAD_CMD     required unless --local-only. Pluggable upload;
//	                        receives the dump path as $1.
//	  BACKUP_DIR            default ./backups. Local staging dir for the artifact.
//	  RETENTION_DAYS        default 90, minimum 1. Prune dumps older than this (D2).
//	  BACKUP_MIN_BYTES      default 4096. Reject a smaller dump.
//	  BACKUP_TIMEOUT_SECONDS  default 3600. Kill a hung pg_dump.
//
//	Behavior:
//	  - `set -euo pipefail`, `umask 077`
//	  - dump:     PGDATABASE=$DSN pg_dump -Fc --file="$BACKUP_DIR/od-amrita-<UTC>.dump.part"
//	              (DSN in the ENVIRONMENT, never in argv — argv is world-readable via ps)
//	  - verify:   artifact >= BACKUP_MIN_BYTES, and `pg_restore -l` shows audit_log data
//	  - rename:   .part -> od-amrita-YYYYMMDDTHHMMSSZ.dump  (only on success)
//	  - upload:   bash -euo pipefail -c "$BACKUP_UPLOAD_CMD" _ "$dump_path"
//	  - prune:    delete artifacts older than RETENTION_DAYS — last, and only after
//	              a verified, uploaded dump
//	  - --dry-run validates config, prints the plan, exits 0, invokes NOTHING
//	  - missing/empty DSN, missing uploader, unknown flag, failed dump, empty dump,
//	    wrong-database dump → non-zero exit, nothing uploaded, nothing pruned
//
// HOW THIS SUITE IS BUILT, AND WHY IT LOOKS THE WAY IT DOES
//
// An earlier version of this suite asserted against the text of the --dry-run plan,
// which was a hand-written echo block. That is testing the advertisement instead of
// the product: the plan said "-Fc" and "90 days" as literals, so the script could
// have dumped plain SQL and retained a week while every test stayed green. (Both
// mutations were confirmed to pass.)
//
// So: the script now PRINTS the same argv array it EXECUTES, and this suite runs the
// script for real against shimmed pg_dump/pg_restore/uploader binaries and asserts on
// what those shims were actually asked to do — captured argv, files on disk, exit
// codes. A behavior no shim can observe is not a behavior we claim to have tested.
// ─────────────────────────────────────────────────────────────────────────────

// scriptPath returns the absolute path to pg_backup.sh.
func scriptPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(repoRoot(t), backupScriptRel)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("[P0] %s does not exist — the story's 90-day recoverability AC needs a runnable pg_dump wrapper\n  underlying error: %v", backupScriptRel, err)
	}
	return path
}

// stub is a fake external binary placed first on PATH. Each one appends its own argv
// to $ARGV_LOG, so a test can assert on precisely what the script asked the outside
// world to do — which is the only way to prove the real (non-dry) code path, short of
// owning a Postgres.
type stub struct {
	name string
	body string
}

// pgDumpStub fakes pg_dump. It honors --file= (writing STUB_DUMP_BYTES of content, so
// the size check has something real to measure) and exits with STUB_PGDUMP_EXIT. On a
// non-zero exit it still writes a partial file first — that is exactly the truncated
// artifact the script must clean up rather than upload.
const pgDumpStub = `#!/usr/bin/env bash
printf 'pg_dump %s\n' "$*" >> "$ARGV_LOG"
out=""
for a in "$@"; do
    case "$a" in --file=*) out="${a#--file=}" ;; esac
done
[[ -n "$out" ]] || { echo "stub pg_dump: no --file= in argv" >&2; exit 3; }
printf 'PGDATABASE=%s\n' "${PGDATABASE:-<unset>}" >> "$ARGV_LOG"
head -c "${STUB_DUMP_BYTES:-8192}" /dev/zero | tr '\0' 'x' > "$out"
exit "${STUB_PGDUMP_EXIT:-0}"
`

// pgRestoreStub fakes `pg_restore -l`. STUB_HAS_AUDIT=0 simulates the dump of a wrong
// or empty database — one that lists cleanly but contains no audit_log data.
const pgRestoreStub = `#!/usr/bin/env bash
printf 'pg_restore %s\n' "$*" >> "$ARGV_LOG"
if [[ "${STUB_HAS_AUDIT:-1}" == "1" ]]; then
    echo ";     123; 0 0 TABLE DATA public audit_log od_migrator"
    echo ";     124; 0 0 TABLE DATA public users od_migrator"
fi
exit 0
`

// uploaderStub fakes the object-storage CLI named in BACKUP_UPLOAD_CMD. It records its
// argv (so we can prove the dump path really arrives as "$1") and exits STUB_UPLOAD_EXIT.
const uploaderStub = `#!/usr/bin/env bash
printf 'upload %s\n' "$*" >> "$ARGV_LOG"
exit "${STUB_UPLOAD_EXIT:-0}"
`

// stubDir builds a directory of fake binaries and returns it plus the argv log they
// all append to. Anything the script shells out to that is NOT stubbed here (psql,
// curl, scp…) will simply not exist on the sabotaged PATH, so an unexpected call
// fails loudly rather than reaching a real database.
func stubDir(t *testing.T) (dir, argvLog string) {
	t.Helper()

	dir = t.TempDir()
	argvLog = filepath.Join(t.TempDir(), "argv.log")

	stubs := []stub{
		{"pg_dump", pgDumpStub},
		{"pg_restore", pgRestoreStub},
		{"aws", uploaderStub},
		{"rclone", uploaderStub},
		{"gsutil", uploaderStub},
		{"s3cmd", uploaderStub},
	}
	for _, s := range stubs {
		if err := os.WriteFile(filepath.Join(dir, s.name), []byte(s.body), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", s.name, err)
		}
	}
	return dir, argvLog
}

// env builds the script's environment. Callers override or append entries to drive a
// specific scenario. The uploader is ALWAYS configured by default: a dryRunEnv with no
// BACKUP_UPLOAD_CMD would leave the upload stubs unarmed, and "the dry run invoked no
// uploader" would then be trivially true even for a script that uploaded eagerly.
func env(t *testing.T, stubs, argvLog, backupDir string, extra ...string) []string {
	t.Helper()

	base := []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"ARGV_LOG=" + argvLog,
		// A syntactically valid DSN that must never be dialed — every external binary
		// that could dial it is stubbed.
		"BACKUP_DATABASE_URL=postgresql://od_migrator@localhost:5432/od_amrita",
		"BACKUP_UPLOAD_CMD=aws s3 cp \"$1\" s3://od-backups/od-amrita/",
		"BACKUP_DIR=" + backupDir,
	}
	return append(base, extra...)
}

// runScript executes pg_backup.sh and returns its stdout, stderr and exit code.
func runScript(t *testing.T, environ []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.Command("bash", append([]string{scriptPath(t)}, args...)...)
	cmd.Env = environ

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

// invocations returns everything the script asked the outside world to do.
func invocations(t *testing.T, argvLog string) string {
	t.Helper()

	b, err := os.ReadFile(argvLog)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	return string(b)
}

// dumps returns the names of the artifacts (od-amrita-*.dump) currently in dir.
func dumps(t *testing.T, dir string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "od-amrita-*.dump"))
	if err != nil {
		t.Fatalf("glob dumps: %v", err)
	}
	var names []string
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	return names
}

// seedOldDump plants an artifact with an mtime `age` days in the past, so a test can
// prove the prune both fires when it should and stays its hand when it should not.
func seedOldDump(t *testing.T, dir string, ageDays int) string {
	t.Helper()

	when := time.Now().AddDate(0, 0, -ageDays)
	name := fmt.Sprintf("od-amrita-%s.dump", when.UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("old-but-good-snapshot"), 0o600); err != nil {
		t.Fatalf("seed old dump: %v", err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("backdate old dump: %v", err)
	}
	return name
}

// ── Existence & static properties ────────────────────────────────────────────

// A1 [P0] The script exists and is executable.
func TestBackupScript_ExistsAndIsExecutable(t *testing.T) {
	info, err := os.Stat(scriptPath(t))
	if err != nil {
		t.Fatalf("[P0] stat %s: %v", backupScriptRel, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("[P0] %s is not executable (mode %v) — an operator-facing script that needs `chmod +x` before it runs is a trap", backupScriptRel, info.Mode().Perm())
	}
}

// A2 [P0] SECURITY: no credential is baked into the script.
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
		// The quotes matter: `export PGPASSWORD="s3cret!"` is the only form that
		// survives special characters, so it is the form anyone would actually
		// commit. A pattern that excluded quotes would wave it straight through.
		{"a literal PGPASSWORD assignment (pass the secret via the DSN from env)", regexp.MustCompile(`PGPASSWORD=['"]?[^$\s'"]`)},
		{"a libpq keyword-form password (password=… in a connection string)", regexp.MustCompile(`(?i)password\s*=\s*['"]?[^$\s'"]`)},
		{"an AWS access key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
		{"a PEM private key block", regexp.MustCompile(`BEGIN [A-Z ]*PRIVATE KEY`)},
	}
	for _, f := range forbidden {
		if loc := f.re.FindString(src); loc != "" {
			t.Errorf("[P0] %s contains %s — found %q. Never commit a secret; the script reads its DSN from the environment.", backupScriptRel, f.name, loc)
		}
	}
}

// A3 [P1] The script enables strict bash mode and a private umask.
//
// A source grep, and honestly labelled as one: it proves the lines are present, not
// that they work. The behavioral consequences are covered by the real-run tests below
// (a failed dump must not upload or prune; artifacts must not be world-readable),
// which is what actually guards the property.
func TestBackupScript_UsesStrictBashModeAndPrivateUmask(t *testing.T) {
	src := readArtifact(t, backupScriptRel, "the backup script is the story's one code artifact")

	if !regexp.MustCompile(`(?m)^set -euo pipefail$`).MatchString(src) {
		t.Errorf("[P1] %s must have `set -euo pipefail` on a line of its own — otherwise a failed dump still uploads and prunes, and a failure inside a pipeline is masked by the last command's success", backupScriptRel)
	}
	if !regexp.MustCompile(`(?m)^umask 077$`).MatchString(src) {
		t.Errorf("[P1] %s must `umask 077` — the dump is a full copy of every student record and the audit ledger, and the ambient umask leaves it world-readable on the backup host", backupScriptRel)
	}
}

// A4 [P0] SECURITY: the DSN is never passed in pg_dump's argv.
//
// argv is world-readable through `ps auxww` and /proc/<pid>/cmdline for the whole
// duration of the dump — the longest-running command of the day. The credential must
// travel in the environment (PGDATABASE), which is not. This asserts on what the
// pg_dump stub actually RECEIVED, so it cannot be satisfied by a comment.
func TestBackupScript_RealRun_DoesNotLeakDSNInArgv(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	_, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir))
	if code != 0 {
		t.Fatalf("[P0] expected a clean run, got exit %d\n  stderr: %s", code, stderr)
	}

	got := invocations(t, argvLog)
	if regexp.MustCompile(`(?i)pg_dump .*postgres(ql)?://`).MatchString(got) {
		t.Errorf("[P0] the DSN was passed in pg_dump's ARGV — every local user can read it with `ps auxww` while the dump runs.\n  Pass it in the environment (PGDATABASE) instead.\n  invocations:\n%s", got)
	}
	if !strings.Contains(got, "PGDATABASE=postgresql://od_migrator@localhost:5432/od_amrita") {
		t.Errorf("[P0] pg_dump did not receive the DSN via PGDATABASE — it must reach the dump through the environment\n  invocations:\n%s", got)
	}
}

// ── The real dump path ───────────────────────────────────────────────────────

// A5 [P0] A clean run dumps in custom format, verifies, uploads, and prunes — in that
// order — and asserts on the ACTUAL argv, not on the plan's description of it.
//
// This is the test the old suite lacked. Mutating the real command to `pg_dump -Fp`
// (plain SQL — which silently forecloses the selective-restore path the runbook
// promises Amrita IT) previously passed every test. It fails here.
func TestBackupScript_RealRun_DumpsCustomFormat_VerifiesUploadsPrunes(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()
	stale := seedOldDump(t, backupDir, 120) // older than the 90-day retention
	fresh := seedOldDump(t, backupDir, 3)   // well inside it

	stdout, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir))
	if code != 0 {
		t.Fatalf("[P0] expected a clean run, got exit %d\n  stdout: %s\n  stderr: %s", code, stdout, stderr)
	}

	got := invocations(t, argvLog)

	// The dump really is custom-format.
	if !regexp.MustCompile(`pg_dump .*(-Fc|--format=custom)`).MatchString(got) {
		t.Errorf("[P0] pg_dump was not invoked with custom format (-Fc) — pg_restore cannot do a selective restore from a plain-SQL dump, and the runbook's restore path depends on it\n  invocations:\n%s", got)
	}
	// Ownership and GRANTs must stay in the dump: od_runtime's table grants come from
	// the goose migrations, and goose will not re-issue them on a restored DB. Strip
	// them and the restore "succeeds" into a database the app cannot read.
	if regexp.MustCompile(`pg_dump .*--no-(owner|privileges|acl)`).MatchString(got) {
		t.Errorf("[P0] pg_dump was invoked with --no-owner/--no-privileges — those GRANTs are issued by the goose migrations and are NOT re-issued on a restored database. Stripping them ships a restore the application cannot read.\n  invocations:\n%s", got)
	}
	// The dump was verified before it was trusted.
	if !strings.Contains(got, "pg_restore") {
		t.Errorf("[P0] the dump was never verified with pg_restore -l — an empty or wrong-database dump would be uploaded as if it were good\n  invocations:\n%s", got)
	}
	// It was uploaded, and the dump path arrived as "$1".
	uploaded := regexp.MustCompile(`upload s3 cp (\S+) s3://`).FindStringSubmatch(got)
	if uploaded == nil {
		t.Fatalf("[P0] the uploader was never invoked — the artifact never left the machine, which is not a backup\n  invocations:\n%s", got)
	}
	if filepath.Dir(uploaded[1]) != backupDir || !strings.HasSuffix(uploaded[1], ".dump") {
		t.Errorf("[P0] the uploader received %q as $1; expected the finished .dump artifact in %s", uploaded[1], backupDir)
	}
	if strings.HasSuffix(uploaded[1], ".part") {
		t.Errorf("[P0] the uploader was handed the in-progress .part file, not the finished artifact: %q", uploaded[1])
	}

	// Exactly the stale artifact was pruned; the fresh one and the new one survive.
	remaining := dumps(t, backupDir)
	for _, name := range remaining {
		if name == stale {
			t.Errorf("[P0] the %d-day-old artifact %q was NOT pruned — retention is 90 days\n  remaining: %v", 120, stale, remaining)
		}
	}
	if !containsName(remaining, fresh) {
		t.Errorf("[P0] the 3-day-old artifact %q was pruned — the prune must only remove artifacts older than RETENTION_DAYS\n  remaining: %v", fresh, remaining)
	}
	if len(remaining) != 2 { // fresh + the one we just took
		t.Errorf("[P0] expected 2 artifacts after the run (the fresh seed + today's dump), got %d: %v", len(remaining), remaining)
	}
	// No .part corpse left behind.
	if parts, _ := filepath.Glob(filepath.Join(backupDir, "*.part")); len(parts) != 0 {
		t.Errorf("[P0] a .part file survived a successful run: %v", parts)
	}
}

// A6 [P0] The finished artifact is not world-readable.
//
// It is a complete copy of the users table and the immutable audit ledger, and it sits
// in the staging directory for up to the whole retention window.
func TestBackupScript_RealRun_ArtifactIsNotWorldReadable(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	_, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir))
	if code != 0 {
		t.Fatalf("expected a clean run, got exit %d\n  stderr: %s", code, stderr)
	}

	names := dumps(t, backupDir)
	if len(names) != 1 {
		t.Fatalf("expected exactly one artifact, got %v", names)
	}
	info, err := os.Stat(filepath.Join(backupDir, names[0]))
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("[P0] the dump is group/world-readable (mode %04o) — it contains every student record and the entire audit ledger. Expected 0600 (umask 077).", perm)
	}
}

// A7 [P0] The artifact carries a UTC timestamp, so successive runs never overwrite
// each other and "90 days of snapshots" cannot collapse into one.
func TestBackupScript_RealRun_TimestampsTheArtifact(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	_, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir))
	if code != 0 {
		t.Fatalf("expected a clean run, got exit %d\n  stderr: %s", code, stderr)
	}

	names := dumps(t, backupDir)
	if len(names) != 1 {
		t.Fatalf("expected exactly one artifact, got %v", names)
	}
	m := regexp.MustCompile(`^od-amrita-(\d{8}T\d{6}Z)\.dump$`).FindStringSubmatch(names[0])
	if m == nil {
		t.Fatalf("[P0] artifact %q does not match the frozen contract od-amrita-YYYYMMDDTHHMMSSZ.dump", names[0])
	}
	if _, err := time.Parse("20060102T150405Z", m[1]); err != nil {
		t.Errorf("[P0] artifact timestamp %q is not a valid UTC instant: %v", m[1], err)
	}
}

// ── Fail-closed paths: the failures that must never look like success ────────

// A8 [P0] A failed pg_dump uploads nothing, prunes nothing, leaves no partial
// artifact, and exits non-zero.
//
// This is the property A9-of-the-old-suite claimed to guard by grepping for `set -e`.
// Grepping proves the line is present; it does not prove the behavior, because `set -e`
// is disabled inside `if`, `&&`, `||` and command substitution. So: exercise it.
func TestBackupScript_RealRun_FailedDump_UploadsNothingPrunesNothing(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()
	stale := seedOldDump(t, backupDir, 120) // would be pruned by a successful run

	_, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir, "STUB_PGDUMP_EXIT=99"))

	if code == 0 {
		t.Errorf("[P0] pg_dump failed but the script exited 0 — the cron alert never fires and the bucket stays empty\n  stderr: %s", stderr)
	}
	if got := invocations(t, argvLog); strings.Contains(got, "upload ") {
		t.Errorf("[P0] a truncated artifact from a FAILED dump was uploaded\n  invocations:\n%s", got)
	}
	if !containsName(dumps(t, backupDir), stale) {
		t.Errorf("[P0] the failed run PRUNED a good old snapshot (%q) — a bad day must never delete good backups", stale)
	}
	if parts, _ := filepath.Glob(filepath.Join(backupDir, "*.part")); len(parts) != 0 {
		t.Errorf("[P0] a failed dump left a partial artifact behind: %v\n  During an incident an operator grabs the newest dump — it must never be a corpse.", parts)
	}
}

// A9 [P0] A dump that is technically valid but EMPTY is rejected.
//
// `set -e` only catches a non-zero pg_dump. A dump of an empty database exits ZERO and
// writes a small, well-formed, useless archive. Uploading it and pruning good snapshots
// on the strength of it is how a backup system becomes a data-destruction system.
func TestBackupScript_RealRun_EmptyDump_IsRejected(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()
	stale := seedOldDump(t, backupDir, 120)

	stdout, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir, "STUB_DUMP_BYTES=12"))

	if code == 0 {
		t.Errorf("[P0] a 12-byte dump was accepted as a successful backup (exit 0)\n  stdout: %s", stdout)
	}
	if got := invocations(t, argvLog); strings.Contains(got, "upload ") {
		t.Errorf("[P0] an almost-empty dump was uploaded\n  invocations:\n%s", got)
	}
	if !containsName(dumps(t, backupDir), stale) {
		t.Errorf("[P0] an almost-empty dump caused a good old snapshot (%q) to be pruned", stale)
	}
	if !containsAny(stdout+stderr, "bytes") {
		t.Errorf("[P1] the rejection should tell the operator the dump was too small\n  stderr: %s", stderr)
	}
}

// A10 [P0] A dump of the WRONG DATABASE is rejected — the one that contains no
// audit_log.
//
// This is the exact mistake §1 of the runbook exists to prevent: pointing the backup at
// the Supabase auth broker instead of the Neon app DB. That dump succeeds, is a
// plausible size, and misses the entire immutable ledger. Size alone cannot catch it;
// only looking inside can.
func TestBackupScript_RealRun_DumpWithoutAuditLog_IsRejected(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()
	stale := seedOldDump(t, backupDir, 120)

	stdout, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir, "STUB_HAS_AUDIT=0"))

	if code == 0 {
		t.Errorf("[P0] a dump containing NO audit_log data was accepted as a good backup (exit 0).\n  That is what backing up the Supabase auth broker instead of the Neon app DB looks like: a confident, regular, useless backup.\n  stdout: %s", stdout)
	}
	if got := invocations(t, argvLog); strings.Contains(got, "upload ") {
		t.Errorf("[P0] a dump with no audit ledger in it was uploaded\n  invocations:\n%s", got)
	}
	if !containsName(dumps(t, backupDir), stale) {
		t.Errorf("[P0] a wrong-database dump caused a good old snapshot (%q) to be pruned", stale)
	}
	if !containsAny(stdout+stderr, "audit_log") {
		t.Errorf("[P1] the rejection must name audit_log so the operator knows what is missing\n  stderr: %s", stderr)
	}
}

// A11 [P0] A failed UPLOAD prunes nothing and exits non-zero.
//
// Pruning after a failed upload would delete a good old snapshot in exchange for one
// that never reached the bucket.
func TestBackupScript_RealRun_FailedUpload_PrunesNothing(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()
	stale := seedOldDump(t, backupDir, 120)

	_, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir, "STUB_UPLOAD_EXIT=1"))

	if code == 0 {
		t.Errorf("[P0] the upload failed but the script exited 0 — the bucket has no new snapshot and nothing alerts\n  stderr: %s", stderr)
	}
	if !containsName(dumps(t, backupDir), stale) {
		t.Errorf("[P0] the prune ran after a FAILED upload and deleted a good old snapshot (%q)", stale)
	}
}

// A12 [P0] With no DSN at all the script fails closed, naming the variable to set.
func TestBackupScript_MissingDSN_FailsClosed(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	environ := []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"ARGV_LOG=" + argvLog,
		"BACKUP_DIR=" + backupDir,
		"BACKUP_UPLOAD_CMD=aws s3 cp \"$1\" s3://od-backups/",
		// deliberately: no BACKUP_DATABASE_URL, no DATABASE_URL_MIGRATOR
	}
	stdout, stderr, code := runScript(t, environ)

	if code == 0 {
		t.Errorf("[P0] with no DSN set the script exited 0 — it must fail closed rather than silently back up nothing\n  stdout: %s", stdout)
	}
	if !containsAny(stdout+stderr, "BACKUP_DATABASE_URL") {
		t.Errorf("[P0] the missing-DSN error must name the variable the operator has to set (BACKUP_DATABASE_URL)\n  stderr: %s", stderr)
	}
	if got := invocations(t, argvLog); got != "" {
		t.Errorf("[P0] the script shelled out before validating its configuration\n  invocations:\n%s", got)
	}
}

// A13 [P0] A SET-BUT-EMPTY DSN is a hard error, never a silent fallback.
//
// This is the highest-consequence configuration bug available here, and it is the one
// the script's own documented cron recipe used to produce: `BACKUP_DATABASE_URL="$(pass
// od/backup-dsn)"` under cron has no TTY, `pass` fails, and the variable arrives EMPTY.
// With a `${VAR:-fallback}` chain that silently becomes DATABASE_URL_MIGRATOR — the DEV
// database — dumped, uploaded under a production filename, and pruned. Green cron,
// wrong data, discovered at restore time.
func TestBackupScript_EmptyDSN_DoesNotFallBack(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	environ := []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"ARGV_LOG=" + argvLog,
		"BACKUP_DIR=" + backupDir,
		"BACKUP_UPLOAD_CMD=aws s3 cp \"$1\" s3://od-backups/",
		"BACKUP_DATABASE_URL=", // set, but empty — a failed secret lookup
		"DATABASE_URL_MIGRATOR=postgresql://od_migrator@localhost:5432/od_amrita_DEV",
	}
	stdout, stderr, code := runScript(t, environ)

	if code == 0 {
		t.Fatalf("[P0] an EMPTY BACKUP_DATABASE_URL was accepted — it must be a hard error, not a fallback\n  stdout: %s", stdout)
	}
	if got := invocations(t, argvLog); strings.Contains(got, "od_amrita_DEV") {
		t.Errorf("[P0] an empty BACKUP_DATABASE_URL fell back to DATABASE_URL_MIGRATOR and dumped the WRONG DATABASE.\n  A failed secret lookup must abort, not quietly back up whatever else is in the environment.\n  invocations:\n%s", got)
	}
	if !containsAny(stdout+stderr, "empty") {
		t.Errorf("[P1] the error should say the variable is set but empty (a failed secret lookup), not merely 'unset'\n  stderr: %s", stderr)
	}
}

// A14 [P0] The DATABASE_URL_MIGRATOR fallback still works when BACKUP_DATABASE_URL is
// genuinely UNSET — the frozen CLI contract.
func TestBackupScript_UnsetDSN_FallsBackToMigrator(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	environ := []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"ARGV_LOG=" + argvLog,
		"BACKUP_DIR=" + backupDir,
		"BACKUP_UPLOAD_CMD=aws s3 cp \"$1\" s3://od-backups/",
		"DATABASE_URL_MIGRATOR=postgresql://od_migrator@localhost:5432/od_amrita",
	}
	_, stderr, code := runScript(t, environ)

	if code != 0 {
		t.Fatalf("[P1] an UNSET BACKUP_DATABASE_URL must fall back to DATABASE_URL_MIGRATOR, got exit %d\n  stderr: %s", code, stderr)
	}
	if !strings.Contains(invocations(t, argvLog), "PGDATABASE=postgresql://od_migrator@localhost:5432/od_amrita") {
		t.Errorf("[P1] the DATABASE_URL_MIGRATOR fallback did not reach pg_dump")
	}
}

// A15 [P0] With no uploader configured the script fails closed.
//
// A dump that never leaves the machine is not a backup — the script says so itself. The
// likeliest cron misconfiguration is the uploader variable not surviving into cron's
// stripped environment; if that exits 0, the cron is green and the bucket is empty for
// ninety days, and it is discovered during an incident.
func TestBackupScript_MissingUploader_FailsClosed(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	environ := []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"ARGV_LOG=" + argvLog,
		"BACKUP_DIR=" + backupDir,
		"BACKUP_DATABASE_URL=postgresql://od_migrator@localhost:5432/od_amrita",
		// deliberately: no BACKUP_UPLOAD_CMD, and no --local-only
	}
	stdout, stderr, code := runScript(t, environ)

	if code == 0 {
		t.Errorf("[P0] no BACKUP_UPLOAD_CMD and no --local-only, yet the script exited 0 — the dump never left the machine and nothing alerted\n  stdout: %s", stdout)
	}
	if !containsAny(stdout+stderr, "BACKUP_UPLOAD_CMD") {
		t.Errorf("[P0] the error must name BACKUP_UPLOAD_CMD\n  stderr: %s", stderr)
	}
	if !containsAny(stdout+stderr, "--local-only") {
		t.Errorf("[P1] the error should mention the --local-only escape hatch for deliberate local dumps\n  stderr: %s", stderr)
	}
}

// A16 [P1] --local-only is the explicit opt-out, and it really does skip the upload.
func TestBackupScript_LocalOnly_SkipsUploadAndSucceeds(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	environ := []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"ARGV_LOG=" + argvLog,
		"BACKUP_DIR=" + backupDir,
		"BACKUP_DATABASE_URL=postgresql://od_migrator@localhost:5432/od_amrita",
	}
	_, stderr, code := runScript(t, environ, "--local-only")

	if code != 0 {
		t.Fatalf("[P1] --local-only must permit a run with no uploader, got exit %d\n  stderr: %s", code, stderr)
	}
	if got := invocations(t, argvLog); strings.Contains(got, "upload ") {
		t.Errorf("[P1] --local-only still invoked an uploader\n  invocations:\n%s", got)
	}
	if len(dumps(t, backupDir)) != 1 {
		t.Errorf("[P1] --local-only should still produce exactly one local artifact, got %v", dumps(t, backupDir))
	}
}

// ── Retention (Decision D2) ──────────────────────────────────────────────────

// A17 [P0] Retention defaults to 90 days — Decision D2, the reinterpretation of NFR29.
//
// "90-day PITR" is unachievable on Neon or Supabase (both cap at 30d). The story's whole
// resolution is PITR-at-host-max PLUS 90-day pg_dump snapshots. If this constant drifts,
// that resolution is void and the NFR is silently unmet.
//
// The old version of this test asserted `strings.Contains(plan, "90")` — and the plan's
// prose said "no managed host offers 90-day PITR" unconditionally, so it passed with the
// retention set to 7. Assert on the value the script will actually use, and prove the
// printed number tracks it.
func TestBackupScript_RetentionDefaultsTo90Days(t *testing.T) {
	stubs, argvLog := stubDir(t)

	plan, stderr, code := runScript(t, env(t, stubs, argvLog, t.TempDir()), "--dry-run")
	if code != 0 {
		t.Fatalf("--dry-run exited %d\n  stderr: %s", code, stderr)
	}

	if !regexp.MustCompile(`retention\s*:\s*90 days`).MatchString(plan) {
		t.Errorf("[P0] the default retention is not 90 days (D2: PITR@host-max + pg_dump@90d is what satisfies NFR29).\n  plan: %s", plan)
	}
	if !regexp.MustCompile(`older than 90 days`).MatchString(plan) {
		t.Errorf("[P0] the prune must be described in terms of the retention actually in force\n  plan: %s", plan)
	}
}

// A18 [P0] The retention the plan PRINTS is the retention the script USES.
//
// This is what makes A17 non-vacuous: if the printed number were a hard-coded literal
// (as it once was), overriding RETENTION_DAYS would not change it — and then A17 would
// be asserting on a string constant rather than on behavior.
func TestBackupScript_RetentionPlanTracksTheRealValue(t *testing.T) {
	stubs, argvLog := stubDir(t)

	plan, stderr, code := runScript(t, env(t, stubs, argvLog, t.TempDir(), "RETENTION_DAYS=7"), "--dry-run")
	if code != 0 {
		t.Fatalf("--dry-run exited %d\n  stderr: %s", code, stderr)
	}

	if !regexp.MustCompile(`retention\s*:\s*7 days`).MatchString(plan) {
		t.Errorf("[P0] with RETENTION_DAYS=7 the plan must say 7 days — the printed number must be the one the prune will use, not a literal\n  plan: %s", plan)
	}
	if regexp.MustCompile(`retention\s*:\s*90 days`).MatchString(plan) {
		t.Errorf("[P0] the plan still claims 90-day retention while RETENTION_DAYS=7 — the plan is decoupled from the behavior it describes, which is exactly how a 7-day-retention script once passed a test named Retains90Days\n  plan: %s", plan)
	}
}

// A19 [P1] RETENTION_DAYS is validated: non-numeric and 0 are refused.
//
// 0 is the value someone reaches for meaning "never prune". To find(1), `-mtime +0`
// means "delete everything older than 24 hours" — the entire snapshot history, gone.
func TestBackupScript_RetentionDays_Validated(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"non-numeric", "abc", "whole number"},
		{"zero means delete everything older than a day", "0", "at least 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubs, argvLog := stubDir(t)
			backupDir := t.TempDir()
			stale := seedOldDump(t, backupDir, 120)

			stdout, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir, "RETENTION_DAYS="+tc.value))

			if code == 0 {
				t.Errorf("[P1] RETENTION_DAYS=%q was accepted\n  stdout: %s", tc.value, stdout)
			}
			if !containsAny(stdout+stderr, tc.want) {
				t.Errorf("[P1] the error should explain why RETENTION_DAYS=%q is refused (want %q)\n  stderr: %s", tc.value, tc.want, stderr)
			}
			if !containsName(dumps(t, backupDir), stale) {
				t.Errorf("[P0] RETENTION_DAYS=%q still pruned a good snapshot before failing", tc.value)
			}
		})
	}
}

// ── Flags ────────────────────────────────────────────────────────────────────

// A20 [P0] --dry-run exits 0 and invokes NOTHING: no pg_dump, no pg_restore, no
// uploader, and no writes into the backup directory.
//
// The uploader is configured in this environment (see env()), so the upload stubs are
// ARMED — an earlier version of this test left BACKUP_UPLOAD_CMD unset, which made
// "the dry run invoked no uploader" trivially true even for a script that uploaded
// eagerly. A dry-run flag that still shells out is worse than no flag at all, because
// operators reach for it to rehearse against production.
func TestBackupScript_DryRun_InvokesNothingAndWritesNothing(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	stdout, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir), "--dry-run")

	if code != 0 {
		t.Errorf("[P0] --dry-run must exit 0, got %d\n  stdout: %s\n  stderr: %s", code, stdout, stderr)
	}
	if got := invocations(t, argvLog); got != "" {
		t.Errorf("[P0] --dry-run invoked an external command — it must only print the plan.\n  invoked:\n%s", got)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read BACKUP_DIR: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("[P0] --dry-run wrote into BACKUP_DIR; it must write nothing (not even a mkdir): %v", entries)
	}
}

// A21 [P1] The dry-run plan shows the command it would actually run.
//
// It prints the same argv array the real run executes, so the plan cannot drift from the
// behavior. It must NOT print the DSN's value — this output lands in a cron log.
func TestBackupScript_DryRunPlan_ShowsTheRealCommand_WithoutTheSecret(t *testing.T) {
	stubs, argvLog := stubDir(t)

	plan, _, _ := runScript(t, env(t, stubs, argvLog, t.TempDir()), "--dry-run")

	if !containsAny(plan, "pg_dump") {
		t.Errorf("[P1] --dry-run must show the pg_dump command it would run\n  plan: %s", plan)
	}
	if !containsAny(plan, "-Fc", "--format=custom") {
		t.Errorf("[P1] --dry-run plan must show the custom-format dump (-Fc)\n  plan: %s", plan)
	}
	if regexp.MustCompile(`(?i)postgres(ql)?://`).MatchString(plan) {
		t.Errorf("[P0] the --dry-run plan printed the DSN's VALUE — this output goes to a cron log. Print the variable name, not the secret.\n  plan: %s", plan)
	}
}

// A22 [P1] An unrecognized flag fails closed.
//
// Silently ignoring an unknown flag means a typo'd `--dry-run` becomes a REAL run
// against production.
func TestBackupScript_UnknownFlag_FailsClosed(t *testing.T) {
	stubs, argvLog := stubDir(t)
	backupDir := t.TempDir()

	stdout, stderr, code := runScript(t, env(t, stubs, argvLog, backupDir), "--dry-rnu")

	if code == 0 {
		t.Errorf("[P0] an unknown flag must be rejected with a non-zero exit — a mistyped --dry-run silently becoming a real run is exactly the accident this guards\n  stdout: %s", stdout)
	}
	if got := invocations(t, argvLog); got != "" {
		t.Errorf("[P0] the script shelled out while rejecting an unknown flag; it must refuse before doing any work\n  invoked:\n%s", got)
	}
	if !containsAny(stdout+stderr, "usage", "unknown", "unrecognized", "invalid") {
		t.Errorf("[P1] rejecting an unknown flag should print usage or an explanatory error\n  stderr: %s", stderr)
	}
}

// A23 [P2] --help works and documents the operator-facing surface.
func TestBackupScript_Help_DocumentsUsage(t *testing.T) {
	stubs, argvLog := stubDir(t)
	stdout, stderr, code := runScript(t, env(t, stubs, argvLog, t.TempDir()), "--help")
	help := stdout + stderr

	if code != 0 {
		t.Errorf("[P2] --help must exit 0, got %d", code)
	}
	for _, want := range []string{"BACKUP_DATABASE_URL", "BACKUP_UPLOAD_CMD", "RETENTION_DAYS", "--dry-run", "--local-only"} {
		if !containsAny(help, want) {
			t.Errorf("[P2] --help must document %s\n  help: %s", want, help)
		}
	}
	if !containsAny(help, "cron", "schedule") {
		t.Errorf("[P2] --help must say how to schedule the script (cron/CI) — the runbook points operators here\n  help: %s", help)
	}
}
