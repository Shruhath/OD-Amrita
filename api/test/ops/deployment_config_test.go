package ops

// deployment_config_test.go — deployed-host / HTTPS / runtime-foundation
// acceptance coverage for the artifacts a dev can actually deliver without
// spending money: api/fly.toml, web/vercel.json, api/scripts/smoke.sh and the
// credential scan over docs/deployment.md.
//
// Invoked by: api-ci -> build job (`go test ./...`). Deliberately UNTAGGED and
// dependency-free — no database, no network, no new module requirement — so it
// runs inside the existing gate with NO new CI wiring, exactly like the
// backup-script suite next door.
//
// ── RED PHASE ───────────────────────────────────────────────────────────────
// These are ACTIVE, red-by-assertion tests, not t.Skip()'d scaffolds. That is
// the established shape for artifact-shape suites in this package: readArtifact
// in helpers_test.go is the RED-phase entry point: a missing artifact fails
// there, naming what it is for, rather than skipping.
//
// ── WHAT THIS SUITE CAN AND CANNOT PROVE ────────────────────────────────────
// It proves the committed artifacts EXIST, have the shape the acceptance
// criteria name, and leak no secrets. It CANNOT prove the service is actually
// deployed, that TLS really terminates at the Fly edge, that /health really
// reports db:up, or that the OAuth round trip works — all of those are gated on
// the paid Fly/Neon/Vercel/Supabase console steps (H5) and are verified by an
// operator running api/scripts/smoke.sh against the live origin. The spec is
// explicit that CI must not pretend to test a host it has no URL for, so this
// suite deliberately stops at artifact shape. See the "Testing standards (be
// honest)" section of the spec.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Paths of the deployed-host artifacts under test, relative to the repo root.
const (
	flyConfigRel     = "api/fly.toml"
	vercelConfigRel  = "web/vercel.json"
	smokeScriptRel   = "api/scripts/smoke.sh"
	deploymentDocRel = "docs/deployment.md"
	workflowsRel     = ".github/workflows"
	flyConfigWhy     = "AC 1/2 need the Fly host config that terminates TLS at the edge and refuses plain HTTP (Task 3)"
	vercelConfigWhy  = "AC 3 needs the SPA rewrite so client-side deep links resolve instead of 404ing (Task 4)"
	smokeScriptWhy   = "AC 1/2 need the operator-run post-deploy check for NFR15 and /health (Task 6)"
	deploymentDocWhy = "the self-hosting guide is the published deployment reference"
)

// secretShaped is the credential scan shared by every committed deployed-host
// artifact (AC 7). It bans the value, never the reference: naming
// $DATABASE_URL_RUNTIME or <vercel-origin> is correct; pasting a DSN with a
// password, a real anon key or a PEM block is not.
var secretShaped = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a DSN with embedded credentials (reference the env var, never paste a connection string)", regexp.MustCompile(`(?i)postgres(ql)?://[^\s/@]*:[^\s/@]*@`)},
	{"a PEM private key block", regexp.MustCompile(`BEGIN [A-Z ]*PRIVATE KEY`)},
	{"what looks like a JWT or Supabase anon key", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{20,}`)},
	{"an AWS access key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	// The dev-only role passwords legitimately live in api/db/init/00_roles.sql
	// and must never be copied into a host config or a runbook.
	{"the dev-only role password placeholder (it belongs only in api/db/init/00_roles.sql)", regexp.MustCompile(`_dev_only_change_me`)},
}

func assertNoSecrets(t *testing.T, rel, content string) {
	t.Helper()
	for _, f := range secretShaped {
		if found := f.re.FindString(content); found != "" {
			t.Errorf("[P0] %s contains %s — found %q.\n  AC 7: committed artifacts carry config SHAPE and placeholders only; every real credential lives in Fly secrets / Vercel env / the operator's shell.", rel, f.name, found)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// A minimal TOML sectioniser.
//
// fly.toml is NESTED, and line-anchored regexes over the whole file cannot tell
// `port = 443` inside [[services.ports]] from the same string sitting in an
// unrelated table — nor can they tell a real assignment from one trailing a
// comment. Both gaps matter here: the NFR15 assertions are the reason this file
// exists, and "no port 80" is the single most important one.
//
// This is NOT a TOML parser and does not pretend to be one — it only recognises
// table headers and attributes subsequent key/value lines to the enclosing
// table, which is exactly the structure these assertions need. Validity remains
// Task 8's `flyctl config validate`. A real parser would mean importing
// pelletier/go-toml (currently an INDIRECT dependency), and promoting it would
// edit go.mod, which the spec's Project Structure Notes forbid.
// ─────────────────────────────────────────────────────────────────────────────

type tomlBlock struct {
	header string // "" for the root table, else e.g. "[[services.ports]]"
	body   string
}

// stripTOMLComments removes trailing `#` comments that fall outside a quoted
// string, so `port = 80  # legacy` is seen as the assignment it is rather than
// hidden behind the comment. Both TOML string forms are honoured — basic ("...")
// and literal ('...') — so a `#` inside either (e.g. a single-quoted value that
// contains one) does not truncate the value. Escaped quotes inside a basic
// string are not handled; the artifacts under test do not use them.
func stripTOMLComments(s string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		var quote rune // 0 when outside a string, else the opening quote char
		cut := len(line)
		for i, r := range line {
			switch {
			case quote != 0:
				if r == quote {
					quote = 0
				}
			case r == '"' || r == '\'':
				quote = r
			case r == '#':
				cut = i
			}
			if cut != len(line) && i >= cut {
				break
			}
		}
		out.WriteString(strings.TrimRight(line[:cut], " \t"))
		out.WriteString("\n")
	}
	return out.String()
}

var tomlHeaderRe = regexp.MustCompile(`^\s*(\[\[[^\]]+\]\]|\[[^\]]+\])\s*$`)

// tomlBlocks splits a comment-stripped document into its table blocks, in order.
func tomlBlocks(cfg string) []tomlBlock {
	blocks := []tomlBlock{{}}
	var body strings.Builder
	flush := func() {
		blocks[len(blocks)-1].body = body.String()
		body.Reset()
	}
	for _, line := range strings.Split(stripTOMLComments(cfg), "\n") {
		if m := tomlHeaderRe.FindStringSubmatch(line); m != nil {
			flush()
			blocks = append(blocks, tomlBlock{header: strings.TrimSpace(m[1])})
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	return blocks
}

// blocksNamed returns every block declared with the given header.
func blocksNamed(blocks []tomlBlock, header string) []tomlBlock {
	var out []tomlBlock
	for _, b := range blocks {
		if b.header == header {
			out = append(out, b)
		}
	}
	return out
}

// tomlValue returns the raw right-hand side of `key = …` within one block body.
// The key match is case-SENSITIVE, as TOML keys are — TLS_ENABLED and
// tls_enabled are distinct keys, and folding them would let a mis-cased
// duplicate satisfy an assertion it should not.
func tomlValue(body, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*(.+?)\s*$`)
	if m := re.FindStringSubmatch(body); m != nil {
		return strings.Trim(m[1], `"'`), true
	}
	return "", false
}

// anyBlockHasKey reports whether any block declares key at all — used for the
// "this must not appear anywhere in committed config" assertions.
func anyBlockHasKey(blocks []tomlBlock, key string) bool {
	for _, b := range blocks {
		if _, ok := tomlValue(b.body, key); ok {
			return true
		}
	}
	return false
}

// stripFullLineShellComments drops lines whose first non-space character is `#`,
// so an assertion cannot be satisfied by a script that merely TALKS about the
// thing it is supposed to check.
//
// Only FULL-line comments are removed, never trailing ones: `${BASE_URL#https://}`
// is parameter expansion, not a comment, and a naive trailing-`#` strip would
// mangle exactly the line that derives the plain-HTTP host.
func stripFullLineShellComments(s string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			out.WriteString("\n")
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite C — api/fly.toml (AC 1, 2)
//
// The NFR15 posture is the whole point of this file. Fly's ergonomic default,
// [http_service] with force_https, issues a 301 HTTP->HTTPS REDIRECT. NFR15
// requires plain HTTP to be REJECTED, not redirected. The only shape that
// achieves that is a [[services]] block whose ports list contains 443 and does
// NOT contain 80, so port 80 has no listener at all.
// ─────────────────────────────────────────────────────────────────────────────

func TestFlyConfig_ExistsAndNamesTheAppAndALowLatencyIndiaRegion(t *testing.T) {
	cfg := readArtifact(t, flyConfigRel, flyConfigWhy)

	if strings.TrimSpace(cfg) == "" {
		t.Fatalf("[P0] %s exists but is empty", flyConfigRel)
	}
	if !regexp.MustCompile(`(?m)^\s*app\s*=\s*["']od-amrita-api["']`).MatchString(cfg) {
		t.Errorf("[P0] %s must set app = \"od-amrita-api\" (H1)", flyConfigRel)
	}
	// The region is load-bearing, not cosmetic: the MVP has no offline queue, so
	// every POST /mark is synchronous and round-trip latency is felt directly.
	// bom (Mumbai, ~15ms) is the PREFERRED home. sin (Singapore, ~40-60ms) is the
	// accepted FALLBACK for when Fly reports no capacity in bom — it is the
	// next-nearest region to India. Any region outside that pair (a US/EU host)
	// still fails: what H1 guards is proximity to India, not the literal "bom".
	if !regexp.MustCompile(`(?m)^\s*primary_region\s*=\s*["'](bom|sin)["']`).MatchString(cfg) {
		t.Errorf("[P0] %s must set primary_region = \"bom\" (preferred) or \"sin\" (fallback when Mumbai capacity is unavailable) — the synchronous mark path has no offline queue to mask latency, so a region far from India is rejected (H1)", flyConfigRel)
	}
	if !containsAny(cfg, "shared-cpu-1x") {
		t.Errorf("[P1] %s must size the machine shared-cpu-1x (H1)", flyConfigRel)
	}
}

func TestFlyConfig_RefusesPlainHTTPRatherThanRedirectingIt(t *testing.T) {
	cfg := readArtifact(t, flyConfigRel, flyConfigWhy)
	blocks := tomlBlocks(cfg)

	// The forbidden shape.
	if len(blocksNamed(blocks, "[http_service]")) > 0 {
		t.Errorf("[P0] %s uses [http_service], which implies a port-80 listener and whose force_https issues a 301 REDIRECT — NFR15 requires plain HTTP to be REFUSED, not redirected. Use a [[services]] block with a single 443 port instead (Task 3).", flyConfigRel)
	}
	// An actual assignment, not a bare mention: Task 3 explicitly tells the dev
	// to leave a COMMENT naming force_https so a future "helpful" edit does not
	// re-add it. A test that failed on that comment would punish the dev for
	// following the spec — comments are stripped before this check.
	if anyBlockHasKey(blocks, "force_https") {
		t.Errorf("[P0] %s sets force_https — that is exactly the 301 redirect NFR15 forbids. Remove it; the absence of a port-80 handler is what refuses HTTP.", flyConfigRel)
	}

	// The required shape.
	services := blocksNamed(blocks, "[[services]]")
	if len(services) == 0 {
		t.Fatalf("[P0] %s must declare a [[services]] block — it is the only Fly shape that can expose 443 WITHOUT also opening port 80 (NFR15)", flyConfigRel)
	}
	internalPortOK := false
	for _, s := range services {
		if v, ok := tomlValue(s.body, "internal_port"); ok && v == "8443" {
			internalPortOK = true
		}
	}
	if !internalPortOK {
		t.Errorf("[P0] %s must forward to internal_port = 8443 INSIDE a [[services]] block — it matches the Dockerfile's EXPOSE and the API_PORT default", flyConfigRel)
	}

	// Attribute every declared port to its own [[services.ports]] block, so a
	// stray `port = 443` elsewhere cannot satisfy this and a real port-80 entry
	// cannot hide behind a trailing comment.
	ports := blocksNamed(blocks, "[[services.ports]]")
	if len(ports) == 0 {
		t.Fatalf("[P0] %s must declare at least one [[services.ports]] block (Task 3)", flyConfigRel)
	}
	tlsOn443 := false
	for _, p := range ports {
		value, ok := tomlValue(p.body, "port")
		if !ok {
			t.Errorf("[P0] %s has a [[services.ports]] block with no port — every exposed port must be explicit (Task 3)", flyConfigRel)
			continue
		}
		// The assertion that actually pins NFR15: no listener on 80 means the
		// connection is refused rather than redirected.
		if value == "80" {
			t.Errorf("[P0] %s declares a [[services.ports]] entry for port 80 — NFR15 requires plain HTTP to hit NO listener and be refused. Delete the port-80 block entirely (Task 3).", flyConfigRel)
			continue
		}
		if value != "443" {
			continue
		}
		handlers, _ := tomlValue(p.body, "handlers")
		if strings.Contains(handlers, "tls") {
			tlsOn443 = true
		} else {
			t.Errorf("[P0] %s's port-443 block declares handlers = %s — it must include \"tls\" so the Fly edge terminates TLS and the container can run TLS_ENABLED=false (H4)", flyConfigRel, handlers)
		}
	}
	if !tlsOn443 {
		t.Errorf("[P0] %s must expose port = 443 with a \"tls\" handler in a [[services.ports]] block", flyConfigRel)
	}
}

func TestFlyConfig_RunsInIngressTerminatedPlaintextModeWithoutARedirectListener(t *testing.T) {
	cfg := readArtifact(t, flyConfigRel, flyConfigWhy)

	env := blocksNamed(tomlBlocks(cfg), "[env]")
	if len(env) == 0 {
		t.Fatalf("[P0] %s must declare an [env] block for the NON-SECRET runtime settings (Task 3)", flyConfigRel)
	}
	body := env[0].body

	// TLS_ENABLED=false is correct here and is NOT a weakening: Fly's edge holds
	// the certificate, so the container serves plaintext behind it. main.go logs
	// a loud warning in this mode, which is expected on Fly.
	if v, ok := tomlValue(body, "TLS_ENABLED"); !ok || !strings.EqualFold(v, "false") {
		t.Errorf("[P0] %s must set TLS_ENABLED = \"false\" in [env] (found %q) — the Fly edge terminates TLS and the container serves plaintext behind it (H4)", flyConfigRel, v)
	}
	if v, ok := tomlValue(body, "API_PORT"); !ok || v != "8443" {
		t.Errorf("[P1] %s should set API_PORT = \"8443\" in [env] (found %q) to match internal_port and the Dockerfile", flyConfigRel, v)
	}
	// config.Load() hard-refuses HTTP_REDIRECT_LISTENER=true, naming NFR15. If it
	// is present at all it must be false.
	if v, ok := tomlValue(body, "HTTP_REDIRECT_LISTENER"); ok && strings.EqualFold(v, "true") {
		t.Errorf("[P0] %s sets HTTP_REDIRECT_LISTENER = true — config.Load() refuses to boot with this, by design, naming NFR15", flyConfigRel)
	}
}

func TestFlyConfig_HealthChecksTheAPIWithAGracePeriod(t *testing.T) {
	cfg := readArtifact(t, flyConfigRel, flyConfigWhy)

	// Scoped to an actual check block, so a "/health" mentioned anywhere else in
	// the file cannot satisfy the criterion.
	var checks []tomlBlock
	for _, b := range tomlBlocks(cfg) {
		if strings.Contains(b.header, "services.") && strings.Contains(b.header, "checks") {
			checks = append(checks, b)
		}
	}
	if len(checks) == 0 {
		t.Fatalf("[P0] %s must declare a health check block (e.g. [[services.http_checks]]) — it is what takes a machine whose process stopped answering out of rotation", flyConfigRel)
	}
	healthChecked := false
	gracePeriod := false
	for _, c := range checks {
		if path, ok := tomlValue(c.body, "path"); ok && strings.Contains(path, "/health") {
			healthChecked = true
			if _, ok := tomlValue(c.body, "grace_period"); ok {
				gracePeriod = true
			}
		}
	}
	if !healthChecked {
		t.Errorf("[P0] %s must health-check path = \"/health\" — the liveness endpoint the API serves (the database probe is the separate /health?db=1)", flyConfigRel)
	}
	if healthChecked && !gracePeriod {
		t.Errorf("[P0] %s's /health check must set a grace_period, so a machine that is still booting is not killed before it can answer", flyConfigRel)
	}
}

func TestFlyConfig_CarriesNoSecrets(t *testing.T) {
	cfg := readArtifact(t, flyConfigRel, flyConfigWhy)
	assertNoSecrets(t, flyConfigRel, cfg)

	// The specific trap: [env] is committed, Fly *secrets* are not. A DSN or a
	// JWKS URL assigned anywhere in this file is a credential in git. Checked as
	// real assignments (comments stripped), so a comment listing which secrets to
	// set with `flyctl secrets set` — which the runbook legitimately wants — does
	// not trip it.
	blocks := tomlBlocks(cfg)
	for _, banned := range []string{"DATABASE_URL_RUNTIME", "DATABASE_URL_MIGRATOR", "AUTH_JWKS_URL", "AUTH_BOOTSTRAP_ADMINS"} {
		if anyBlockHasKey(blocks, banned) {
			t.Errorf("[P0] %s assigns %s in committed config — that value is a Fly SECRET (flyctl secrets set), never an [env] entry (Task 3/7)", flyConfigRel, banned)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite D — web/vercel.json (AC 3)
// ─────────────────────────────────────────────────────────────────────────────

func TestVercelConfig_IsValidJSONAndRewritesEveryPathToTheSPAEntrypoint(t *testing.T) {
	raw := readArtifact(t, vercelConfigRel, vercelConfigWhy)

	var parsed struct {
		Rewrites []struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"rewrites"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("[P0] %s is not valid JSON: %v", vercelConfigRel, err)
	}

	// Without the fallback, opening https://<web>/portal/events directly returns
	// a Vercel 404 — the classic "works from the home page, 404 on refresh".
	// React Router v7 routes on the client; the host must serve index.html.
	found := false
	for _, r := range parsed.Rewrites {
		if strings.Contains(r.Destination, "/index.html") && (r.Source == "/(.*)" || strings.Contains(r.Source, "(.*)")) {
			found = true
		}
	}
	if !found {
		t.Errorf("[P0] %s must rewrite every path to /index.html (e.g. {\"source\": \"/(.*)\", \"destination\": \"/index.html\"}) — without it a client-side deep link like /portal/events 404s at the host (AC 3)", vercelConfigRel)
	}
}

func TestVercelConfig_HardCodesNoOriginOrKey(t *testing.T) {
	raw := readArtifact(t, vercelConfigRel, vercelConfigWhy)
	assertNoSecrets(t, vercelConfigRel, raw)

	// VITE_API_BASE_URL / VITE_SUPABASE_* are Vercel Project Environment
	// Variables read by Vite at BUILD time — not vercel.json entries.
	for _, banned := range []string{"VITE_API_BASE_URL", "VITE_SUPABASE_URL", "VITE_SUPABASE_ANON_KEY"} {
		if strings.Contains(raw, banned) {
			t.Errorf("[P0] %s references %s — that is a Vercel Project Environment Variable set in the dashboard (Task 7), not committed config (Task 4)", vercelConfigRel, banned)
		}
	}
	if regexp.MustCompile(`https://[a-z0-9-]+\.(fly\.dev|vercel\.app|supabase\.co)`).MatchString(raw) {
		t.Errorf("[P0] %s hard-codes a real origin — origins are environment configuration, recorded in the runbook and set in the dashboard, never baked into committed host config", vercelConfigRel)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite E — api/scripts/smoke.sh (AC 1, 2)
//
// This is the operator's post-deploy check, the ONLY thing that can prove ACs
// 1 and 2 for real. It is deliberately NOT a CI job (CI has no deployed URL),
// and TestSmokeScript_IsNotWiredIntoCI below enforces that it stays that way.
// ─────────────────────────────────────────────────────────────────────────────

func TestSmokeScript_ExistsAndIsExecutable(t *testing.T) {
	script := readArtifact(t, smokeScriptRel, smokeScriptWhy)
	if strings.TrimSpace(script) == "" {
		t.Fatalf("[P0] %s exists but is empty", smokeScriptRel)
	}

	info, err := os.Stat(filepath.Join(repoRoot(t), smokeScriptRel))
	if err != nil {
		t.Fatalf("[P0] stat %s: %v", smokeScriptRel, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("[P0] %s is not executable (chmod +x) — an operator runbook step that needs `bash path/to/script` instead of `path/to/script` is a step that gets run wrong under pressure", smokeScriptRel)
	}
}

func TestSmokeScript_UsesStrictBashModeAndTakesTheOriginFromTheEnvironment(t *testing.T) {
	script := readArtifact(t, smokeScriptRel, smokeScriptWhy)

	if !regexp.MustCompile(`set -euo pipefail`).MatchString(script) {
		t.Errorf("[P0] %s must `set -euo pipefail` — matching api/scripts/pg_backup.sh. A smoke check that continues past a failed assertion reports success for a broken deploy.", smokeScriptRel)
	}
	if !containsAny(script, "BASE_URL") {
		t.Errorf("[P0] %s must take the deployed origin from $BASE_URL — hard-coding it makes the script useless against a preview or a re-created app (Task 6)", smokeScriptRel)
	}
	assertNoSecrets(t, smokeScriptRel, script)
}

func TestSmokeScript_AssertsHealthReportsTheDatabaseIsUp(t *testing.T) {
	script := stripFullLineShellComments(readArtifact(t, smokeScriptRel, smokeScriptWhy))

	if !containsAny(script, "/health") {
		t.Errorf("[P0] %s must probe /health (AC 2)", smokeScriptRel)
	}
	// A 200 alone is not the criterion. `{"status":"ok","db":"up"}` is: a 503
	// degraded/down response means the runtime pool never reached Neon, which is
	// precisely the failure this check exists to catch.
	//
	// Matched as a REGEX allowing incidental whitespace, and accepting the jq
	// form as well as a literal grep — an earlier version passed `db.:.up` to
	// containsAny, which does substring matching, so that alternative could never
	// have fired and the check silently rested on one spelling.
	dbUp := regexp.MustCompile(`"db"\s*:\s*"up"|\.db\s*==\s*"up"`)
	if !dbUp.MatchString(script) {
		t.Errorf("[P0] %s must assert the body reports db up, not merely a 200 — a 503 {\"status\":\"degraded\",\"db\":\"down\"} is exactly the failure AC 2 is about", smokeScriptRel)
	}
	statusOK := regexp.MustCompile(`"status"\s*:\s*"ok"|\.status\s*==\s*"ok"`)
	if !statusOK.MatchString(script) {
		t.Errorf("[P1] %s must assert the body reports status ok (AC 2)", smokeScriptRel)
	}
}

func TestSmokeScript_FailsWhenPlainHTTPIsAnsweredNotJustWhenRedirected(t *testing.T) {
	// Comment lines are stripped first: a script that only MENTIONS these cases
	// in a comment has not tested anything, and the un-stripped version of this
	// check would have accepted exactly that.
	script := stripFullLineShellComments(readArtifact(t, smokeScriptRel, smokeScriptWhy))

	// NFR15 is a claim about port 80, and only an http:// request can test it.
	if !containsAny(script, "http://") {
		t.Errorf("[P0] %s must probe the plain-http:// origin — NFR15 is a claim about what happens on port 80, and only an http:// request can test it (AC 1)", smokeScriptRel)
	}
	// A redirect (any 3xx) must be a failure: it still accepts the insecure
	// connection first, then points at the secure one.
	if !regexp.MustCompile(`(?i)fail\b[^\n]*redirect`).MatchString(script) {
		t.Errorf("[P0] %s must FAIL on a redirected plain-HTTP response — a 3xx still accepts the insecure connection before answering it (Task 6)", smokeScriptRel)
	}
	// The soundness fix: a plain-HTTP request ANSWERED with a non-3xx status
	// (200/404/502 from a port-80 handler that serves the app in cleartext) must
	// ALSO fail. An earlier version passed the catch-all "answered <code> — not a
	// redirect", green-lighting exactly that regression.
	if regexp.MustCompile(`(?i)\bpass\b[^\n]*answered[^\n]*not a redirect`).MatchString(script) {
		t.Errorf("[P0] %s still PASSes when plain HTTP is answered with a non-3xx status — NFR15 requires plain HTTP to be refused/unanswered, so ANY answered status must fail, not just a 3xx redirect (Task 6)", smokeScriptRel)
	}
	if !regexp.MustCompile(`(?i)fail\b[^\n]*answered`).MatchString(script) {
		t.Errorf("[P0] %s must FAIL when plain HTTP is answered with any status — something listening on port 80 accepts the insecure connection whatever code it returns (Task 6)", smokeScriptRel)
	}
	// A script that detects a violation and then exits 0 has reported success for
	// it. Requiring a non-zero exit path is the cheapest proof the detection is
	// wired to an actual failure.
	if !regexp.MustCompile(`exit\s+[1-9]`).MatchString(script) {
		t.Errorf("[P0] %s must exit non-zero when an assertion fails — a smoke check that detects a violation and still exits 0 reports success for exactly what it exists to catch (Task 6)", smokeScriptRel)
	}
}

// TestSmokeScript_IsNotWiredIntoCI is the inverse assertion the spec explicitly
// asks for: "do not wire it into .github/workflows/** or claim it as automated
// coverage". CI has no deployed URL, so a job invoking this script could only
// ever pass vacuously or fail for the wrong reason — the "hollow harness"
// failure mode a prior retrospective named.
func TestSmokeScript_IsNotWiredIntoCI(t *testing.T) {
	dir := filepath.Join(repoRoot(t), workflowsRel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", workflowsRel, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(body), "smoke.sh") {
			t.Errorf("[P0] .github/workflows/%s invokes smoke.sh — this script is operator-run against the LIVE origin, which CI has no URL for. A CI job around it would pass vacuously or fail for the wrong reason, and the spec forbids claiming it as automated coverage.", entry.Name())
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite F — docs/deployment.md
//
// The guide is generic on purpose: it says WHICH values to set, never the
// values. This is the same scan every committed host config gets, and it is why
// api-ci's path filter includes docs/**.
// ─────────────────────────────────────────────────────────────────────────────

func TestDeploymentGuide_CarriesNoCredentials(t *testing.T) {
	doc := readArtifact(t, deploymentDocRel, deploymentDocWhy)
	if strings.TrimSpace(doc) == "" {
		t.Fatalf("[P0] %s exists but is empty", deploymentDocRel)
	}
	assertNoSecrets(t, deploymentDocRel, doc)
}
