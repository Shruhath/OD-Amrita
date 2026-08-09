package ops

import (
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Suite B — docs/backup-and-monitoring-runbook.md
//
// A runbook with no test is prose, and prose rots silently. This suite is the
// machine guard that keeps the document honest about the four things it would
// otherwise be easiest to get wrong — each of which fails *invisibly*:
//
//   1. Naming the wrong database as the backup target (B4). The acceptance criteria
//      say "Supabase snapshots", written before the hybrid-auth split. Supabase is
//      now the auth broker only; the app data AND the immutable audit ledger live on
//      Neon. Backing up Supabase would produce a confident, regular, useless backup.
//   2. Promising 90-day PITR (B5), which neither Neon nor Supabase can do (both cap
//      at 30 days). A doc that claims it is a lie that only surfaces at restore time.
//   3. Omitting the dual-role bootstrap from the restore procedure (B6). Restore
//      succeeds, the app then cannot connect, and Amrita IT concludes the backup was
//      corrupt. One missing sentence turns a working restore into a false negative.
//   4. Quietly dropping the hosting-blocked steps (B8) instead of tracking them.
//
// These are structure and content-anchor checks. They prove the runbook exists, is
// organized, names the right things and leaks no credentials. They cannot prove the
// prose is correct — the restore drill (itself one of B8's unchecked boxes) is the
// only thing that can.
// ─────────────────────────────────────────────────────────────────────────────

const runbookWhy = "it is the story's primary deliverable — the operator runbook that makes the DB recoverable by standard Amrita IT without tribal knowledge"

// headings extracts the text of every markdown SECTION heading (## and deeper).
//
// The document title (a single `#` H1) is deliberately excluded. It reads "Backup &
// monitoring runbook", which contains both "backup" and "monitoring" — so counting it
// as a heading made the required-section checks below satisfiable by the title alone:
// deleting §2 (Backup posture) and §4 (Monitoring) outright still passed.
func headings(doc string) []string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "##") {
			out = append(out, strings.ToLower(strings.TrimLeft(trimmed, "# ")))
		}
	}
	return out
}

// hasHeadingWith reports whether any heading mentions all the given keywords.
func hasHeadingWith(doc string, keywords ...string) bool {
	for _, h := range headings(doc) {
		all := true
		for _, k := range keywords {
			if !strings.Contains(h, strings.ToLower(k)) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// uncheckedBoxes returns every unchecked markdown task-list item in the document.
func uncheckedBoxes(doc string) []string {
	re := regexp.MustCompile(`(?m)^\s*[-*]\s+\[ \]\s+(.*)$`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(doc, -1) {
		out = append(out, m[1])
	}
	return out
}

// trackedBoxes returns every task-list item, DONE OR NOT.
//
// The distinction from uncheckedBoxes carries the whole point of the checklist
// assertions: whether a step is TRACKED is a permanent property, while whether
// it is still OWED changes the day someone does it. Asserting the first with a
// helper that only sees the second makes a completed checklist indistinguishable
// from a missing one.
func trackedBoxes(doc string) []string {
	re := regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(.*)$`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(doc, -1) {
		out = append(out, m[1])
	}
	return out
}

// B1 [P0] The runbook exists.
func TestRunbook_Exists(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	if strings.TrimSpace(doc) == "" {
		t.Fatalf("[P0] %s exists but is empty", runbookRel)
	}
}

// B2 [P0] SECURITY: the runbook carries no credentials.
//
// Runbooks are precisely where DSNs get pasted "just for now". The rule: reference
// the env var ($DATABASE_URL_MIGRATOR), never a connection string with a password in
// it. Credential-free DSNs and pg_restore examples are fine — this bans userinfo.
func TestRunbook_ContainsNoCredentials(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	forbidden := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"a DSN with embedded credentials (reference $DATABASE_URL_MIGRATOR instead of pasting a connection string)", regexp.MustCompile(`(?i)postgres(ql)?://[^\s/@]*:[^\s/@]*@`)},
		{"an AWS access key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
		{"a PEM private key block", regexp.MustCompile(`BEGIN [A-Z ]*PRIVATE KEY`)},
		{"what looks like a JWT", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{20,}`)},
	}
	for _, f := range forbidden {
		if found := f.re.FindString(doc); found != "" {
			t.Errorf("[P0] %s contains %s — found %q.\n  The runbook records WHICH values to set, never the values themselves (same rule as docs/auth-microsoft-supabase-setup.md).", runbookRel, f.name, found)
		}
	}
}

// B3 [P0] The runbook has the sections that make it a runbook rather than a memo.
func TestRunbook_HasRequiredSections(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	required := []struct {
		what     string
		keywords []string
		why      string
	}{
		{"topology / where the data lives", []string{"topology"}, "an operator must know which database to back up before anything else"},
		{"backup posture", []string{"backup"}, "the PITR + pg_dump strategy (D2) has to be written down"},
		{"monitoring", []string{"monitoring"}, "the /health + Uptime Robot half of the AC"},
		{"restore procedure", []string{"restore"}, "an un-documented restore is not a restore"},
		{"operator checklist", []string{"operator", "checklist"}, "the human-only steps must be tracked, not implied"},
	}
	for _, r := range required {
		if !hasHeadingWith(doc, r.keywords...) {
			t.Errorf("[P0] %s is missing a %q section — %s\n  (looked for a heading containing: %v)", runbookRel, r.what, r.why, r.keywords)
		}
	}
}

// B4 [P0] The backup target is named as Neon, and Supabase is stated to be the auth
// broker only.
//
// THIS IS THE SPEC-VS-REALITY GAP THE STORY EXISTS TO CLOSE. NFR28/29 say "Supabase
// snapshots" — pre-hybrid wording. Snapshotting Supabase would back up GoTrue's
// identity tables and MISS the entire immutable audit ledger, which is the one thing
// the backup is for (Story 7.2's replay oracle depends on it being recoverable).
func TestRunbook_NamesNeonAsBackupTarget_AndSupabaseAsBrokerOnly(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	if !containsAny(doc, "neon") {
		t.Errorf("[P0] %s never mentions Neon — the application database and the immutable audit_log live on the Neon runtime host, and that is what must be backed up (D1)", runbookRel)
	}
	if !containsAny(doc, "supabase") {
		t.Errorf("[P0] %s never mentions Supabase — it must state explicitly that Supabase is the auth broker only, so nobody 'restores' the stale NFR29 wording and starts snapshotting the wrong database", runbookRel)
	}
	if !containsAny(doc, "broker") {
		t.Errorf("[P0] %s must describe Supabase as the auth BROKER (identity/session tables only). Without that sentence the next reader backs up Supabase and silently loses the audit ledger.", runbookRel)
	}
	if !containsAny(doc, "audit_log", "audit log", "audit ledger") {
		t.Errorf("[P0] %s must name the immutable audit ledger as the backup-critical table — it is the reason the 90-day retention exists", runbookRel)
	}
}

// B5 [P1] The 90-day posture is documented as PITR + pg_dump, not as pure PITR.
func TestRunbook_Documents90DayStrategyAsPITRPlusDump(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	if !containsAny(doc, "pitr", "point-in-time") {
		t.Errorf("[P1] %s must document the PITR window (layer 1: fine-grained, short horizon)", runbookRel)
	}
	if !containsAny(doc, "pg_dump") {
		t.Errorf("[P1] %s must document the pg_dump logical snapshots (layer 2: coarse, 90-day horizon). No managed PITR reaches 90 days — the two layers TOGETHER are what satisfy NFR29 (D2).", runbookRel)
	}
	if !containsAny(doc, "90") {
		t.Errorf("[P1] %s must state the 90-day retention target", runbookRel)
	}
}

// B6 [P0] The restore procedure includes the dual-role bootstrap.
//
// A restored database has no od_migrator/od_runtime roles until db/init/00_roles.sql
// is re-applied. Skip it and the app hits permission-denied on connect — and IT
// reasonably concludes the restore failed. This single omission is the difference
// between a runbook and a trap.
func TestRunbook_RestoreCoversDualRoleBootstrap(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	namesRoleBootstrap := containsAny(doc, "00_roles.sql") ||
		(containsAny(doc, "od_migrator") && containsAny(doc, "od_runtime"))

	if !namesRoleBootstrap {
		t.Errorf("[P0] %s restore procedure never mentions the dual-role bootstrap (db/init/00_roles.sql, od_migrator/od_runtime).\n  A restored DB without those roles refuses every application connection — Amrita IT will believe the restore was corrupt. This step is not optional.", runbookRel)
	}
}

// B7 [P1] Monitoring documents the /health contract and the Uptime Robot config.
//
// Note the 503-on-DB-down is DELIBERATE (a readiness probe, not liveness). Pinning it
// here stops a future reader "fixing" /health to always-200 and thereby making the
// monitor blind to exactly the outage it exists to catch.
func TestRunbook_DocumentsHealthContractAndMonitor(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	if !containsAny(doc, "/health") {
		t.Errorf("[P1] %s must document the existing GET /health endpoint (shipped in the DB foundation; unchanged here per D3)", runbookRel)
	}
	if !containsAny(doc, "uptime robot", "uptimerobot") {
		t.Errorf("[P1] %s must specify the Uptime Robot monitor configuration (type, URL, expected keyword/status, interval, alert contacts)", runbookRel)
	}
	if !containsAny(doc, "503") {
		t.Errorf("[P1] %s must record that /health returns 503 when the DB is down — that is the readiness signal Uptime Robot alerts on, and it must not be 'fixed' to always-200", runbookRel)
	}
	if !containsAny(doc, "degraded") || !containsAny(doc, "\"ok\"", "`ok`", "status:\"ok\"", "status: ok") {
		t.Errorf("[P1] %s must record the /health response shape (200 {status:\"ok\",db:\"up\"} / 503 {status:\"degraded\",db:\"down\"}) so the monitor's keyword check is written against the real contract", runbookRel)
	}
}

// B8 [P1] The hosting-blocked work is TRACKED as unchecked operator checklist items,
// not quietly dropped (Decision D4).
//
// Uptime Robot registration and the production PITR confirmation cannot be done until
// hosting is chosen. The honest close is that they remain visibly open — and the
// restore drill stays open until a human actually runs it. A documented-but-never-
// tested restore is not a restore.
func TestRunbook_TracksBlockedOperatorStepsAsUncheckedBoxes(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	boxes := uncheckedBoxes(doc)
	if len(boxes) < 3 {
		t.Fatalf("[P1] %s must carry an operator checklist of unchecked `- [ ]` items for the human-only steps; found %d\n  (D4: work blocked on deferred hosting is tracked, never silently dropped)", runbookRel, len(boxes))
	}

	// Each requirement must be met by its OWN box. Matching a concatenation of every
	// box with a list of OR'd needles was far too weak: "confirm PITR on the production
	// DB" was satisfied by the word "retention" appearing in the unrelated bucket-
	// lifecycle item, so deleting the PITR box entirely still passed.
	required := []struct {
		what string
		// all of `must` (each an OR-group) have to appear in a single box.
		must [][]string
	}{
		{
			"registering the Uptime Robot monitor (blocked on a deployed public URL)",
			[][]string{{"uptime robot", "uptimerobot"}, {"monitor"}},
		},
		{
			"confirming PITR on the PRODUCTION DB (blocked on the hosting decision)",
			[][]string{{"pitr", "point-in-time"}, {"production", "prod"}},
		},
		{
			"running one real restore drill — the story's actual acceptance test",
			[][]string{{"drill"}, {"restore"}},
		},
	}

	claimed := make(map[int]string) // box index -> requirement it satisfies
	for _, r := range required {
		matched := -1
		for i, box := range boxes {
			if _, taken := claimed[i]; taken {
				continue
			}
			all := true
			for _, group := range r.must {
				if !containsAny(box, group...) {
					all = false
					break
				}
			}
			if all {
				matched = i
				break
			}
		}
		if matched < 0 {
			t.Errorf("[P1] the operator checklist has no unchecked item of its own for %s\n  (each tracked step needs a distinct `- [ ]` box — D4)\n  unchecked items found: %v", r.what, boxes)
			continue
		}
		claimed[matched] = r.what
	}
}

// B9 [P1] The runbook points at the backup script, so the two cannot drift apart.
func TestRunbook_ReferencesTheBackupScript(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	if !containsAny(doc, "pg_backup.sh") {
		t.Errorf("[P1] %s must reference the backup script (%s) — a runbook whose commands live nowhere, and a script no runbook mentions, drift apart within one sprint", runbookRel, backupScriptRel)
	}
}

// B10 [P2] Both restore paths are documented, because both layers exist.
func TestRunbook_DocumentsBothRestorePaths(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	if !containsAny(doc, "pg_restore") {
		t.Errorf("[P2] %s must document the logical restore path (pg_restore from a 90-day pg_dump snapshot)", runbookRel)
	}
	if !containsAny(doc, "pitr", "point-in-time") {
		t.Errorf("[P2] %s must document the PITR restore path (console: pick a timestamp, restore to a branch, re-point the DSN)", runbookRel)
	}
	if !containsAny(doc, "goose") {
		t.Errorf("[P2] %s restore procedure should have IT confirm schema parity with goose after restoring", runbookRel)
	}
}
