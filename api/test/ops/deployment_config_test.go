package ops

// deployment_config_test.go — deployed-host / HTTPS / runtime-foundation
// acceptance coverage for the artifacts a dev can actually deliver without
// spending money: api/fly.toml, web/vercel.json, api/scripts/smoke.sh and
// docs/deployment-runbook.md, plus the backup runbook's unblocking.
//
// Invoked by: api-ci -> build job (`go test ./...`). Deliberately UNTAGGED and
// dependency-free — no database, no network, no new module requirement — so it
// runs inside the existing gate with NO new CI wiring, exactly like the
// backup-and-monitoring suites next door.
//
// ── RED PHASE ───────────────────────────────────────────────────────────────
// These are ACTIVE, red-by-assertion tests, not t.Skip()'d scaffolds. That is
// the established shape for artifact-shape suites in this package: readArtifact
// in helpers_test.go is documented as "the RED-phase entry point: today both
// artifacts are absent and every test in this package fails here, by design",
// which is precisely how the backup-and-monitoring ops suite began. All four
// artifacts under test are absent at the start, so this whole file is RED until
// Tasks 3-6 create them, and it turns green with no test edit.
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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Paths of the deployed-host artifacts under test, relative to the repo root.
const (
	flyConfigRel     = "api/fly.toml"
	vercelConfigRel  = "web/vercel.json"
	smokeScriptRel   = "api/scripts/smoke.sh"
	deployRunbookRel = "docs/deployment-runbook.md"
	workflowsRel     = ".github/workflows"
	flyConfigWhy     = "AC 1/2 need the Fly host config that terminates TLS at the edge and refuses plain HTTP (Task 3)"
	vercelConfigWhy  = "AC 3 needs the SPA rewrite so client-side deep links resolve instead of 404ing (Task 4)"
	smokeScriptWhy   = "AC 1/2 need the operator-run post-deploy check for NFR15 and /health (Task 6)"
	deployRunbookWhy = "AC 5 needs the deployment runbook recording origins, wiring values and the operator checklist (Task 5)"
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

func TestFlyConfig_HealthChecksTheDatabaseBackedEndpointWithAGracePeriod(t *testing.T) {
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
		t.Fatalf("[P0] %s must declare a health check block (e.g. [[services.http_checks]]) — it is what keeps a machine whose pool never reached Neon out of rotation (AC 2, Task 3)", flyConfigRel)
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
		t.Errorf("[P0] %s must health-check path = \"/health\" — it is the endpoint that proves the od_runtime pool actually reached Neon (AC 2)", flyConfigRel)
	}
	if healthChecked && !gracePeriod {
		t.Errorf("[P0] %s's /health check must set a grace_period — /health is fail-closed (503 when the DB is down), so a machine whose pool has not connected yet would be killed before it could ever become healthy (Task 3)", flyConfigRel)
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
// Suite F — docs/deployment-runbook.md (AC 5, 6)
// ─────────────────────────────────────────────────────────────────────────────

func TestDeploymentRunbook_ExistsAndHasTheSectionsThatMakeItOperable(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)
	if strings.TrimSpace(doc) == "" {
		t.Fatalf("[P0] %s exists but is empty", deployRunbookRel)
	}

	required := []struct {
		what     string
		keywords []string
		why      string
	}{
		{"topology", []string{"topology"}, "an operator must know which host holds what before touching anything"},
		{"deploy procedure", []string{"deploy"}, "the exact flyctl/Vercel commands are the point of the document"},
		{"operator checklist", []string{"checklist"}, "the paid/console steps (H5) must be tracked, not implied"},
	}
	for _, r := range required {
		if !hasHeadingWith(doc, r.keywords...) {
			t.Errorf("[P0] %s is missing a %q section — %s\n  (looked for a heading containing: %v)", deployRunbookRel, r.what, r.why, r.keywords)
		}
	}
}

func TestDeploymentRunbook_DocumentsTheCurrentMigrationVersion(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)
	latest := latestMigrationVersion(t)
	padded := fmt.Sprintf("%04d", latest)

	if !strings.Contains(doc, fmt.Sprintf("Expect **%d**", latest)) {
		t.Errorf("[P0] %s must tell operators to expect goose version %d, matching the highest committed migration %s", deployRunbookRel, latest, padded)
	}
	if !strings.Contains(doc, fmt.Sprintf("`0001`–`%s`", padded)) {
		t.Errorf("[P1] %s must name the applied migration range through %s", deployRunbookRel, padded)
	}
}

func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "api/db/migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	versionRe := regexp.MustCompile(`^([0-9]+)_.*\.sql$`)
	latest := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := versionRe.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse migration version from %s: %v", entry.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		t.Fatal("no numbered migrations found")
	}
	return latest
}

func TestDeploymentRunbook_RecordsTheExactDeployCommandAndItsRepoRootContext(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)

	// The Dockerfile COPYs go.work, api/, contracts/ and test/, so its build
	// context is the REPO ROOT. A deploy run from inside api/ cannot load the Go
	// workspace and fails confusingly — which is why the runbook must carry the
	// --config form rather than a bare `flyctl deploy`.
	if !containsAny(doc, "flyctl deploy") {
		t.Errorf("[P0] %s must record the flyctl deploy command (Task 5)", deployRunbookRel)
	}
	if !containsAny(doc, "--config api/fly.toml") {
		t.Errorf("[P0] %s must record `--config api/fly.toml` — the Dockerfile's build context is the REPO ROOT (it COPYs go.work and contracts/), so a deploy from inside api/ fails to load the workspace (Task 3/5)", deployRunbookRel)
	}
	if !containsAny(doc, "scale count 2") {
		t.Errorf("[P1] %s must record `flyctl scale count 2 --region <fly.toml region>` (bom preferred, sin fallback) — the 2-machine count is a deploy-time concern that fly.toml cannot express, and one machine is the reliability posture H1 explicitly rejected", deployRunbookRel)
	}
}

func TestDeploymentRunbook_RecordsTheCrossOriginWiringTheAuthRoundTripNeeds(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)

	// AC 6. Each of these is a separate way the deployed app silently breaks:
	// a missing VITE_API_BASE_URL makes the SPA call its own Vercel origin for
	// the API; a missing PORTAL_ALLOWED_ORIGINS blocks every browser call on
	// CORS; a missing Supabase redirect URL breaks the OAuth return leg.
	for _, key := range []string{"PORTAL_ALLOWED_ORIGINS", "VITE_API_BASE_URL"} {
		if !containsAny(doc, key) {
			t.Errorf("[P0] %s must record the exact %s used (AC 6)", deployRunbookRel, key)
		}
	}
	if !containsAny(doc, "redirect url", "redirect urls") {
		t.Errorf("[P0] %s must record the Supabase Redirect URL addition — without it the OAuth round trip cannot return to the deployed web origin (AC 6, Task 7 step 4)", deployRunbookRel)
	}
	// config.validateOrigin rejects a trailing slash, a path, a query or a
	// wildcard, so the runbook has to say "exact origin" or the operator will
	// paste a URL that makes the API refuse to boot.
	if !containsAny(doc, "exact origin", "no trailing slash", "trailing slash") {
		t.Errorf("[P1] %s must warn that PORTAL_ALLOWED_ORIGINS entries are EXACT origins (no trailing slash, path, query or wildcard) — config.validateOrigin refuses to boot otherwise", deployRunbookRel)
	}
}

func TestDeploymentRunbook_CoversTheSecureContextAndFailClosedHealthConsequences(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)

	// The coordinator device note is why this is the first foundation slice: Web
	// NFC and getUserMedia only exist in a secure context, so the later NFC and
	// camera surfaces are impossible without the HTTPS origin recorded here.
	if !containsAny(doc, "chrome") || !containsAny(doc, "android") {
		t.Errorf("[P0] %s must carry the coordinator device-setup note (open the web origin in Chrome on Android over HTTPS) — Web NFC and the camera require a secure context, which is what makes the NFC and camera surfaces possible at all (AC 5)", deployRunbookRel)
	}
	if !containsAny(doc, "nfr15") && !containsAny(doc, "not redirected", "rejected, not") {
		t.Errorf("[P0] %s must document the NFR15 verification step (plain HTTP refused, NOT redirected) (AC 1, Task 5)", deployRunbookRel)
	}
	// An operator who does not know /health is fail-closed will misread a
	// machine dropping out of rotation as a Fly problem rather than a Neon one.
	if !containsAny(doc, "503", "degraded", "fail-closed", "fail closed") {
		t.Errorf("[P0] %s must note that /health is fail-closed (503 when Neon is unreachable) and that Fly therefore pulls the machine from rotation — correct behaviour, but it makes Neon a hard dependency of availability (Task 3/5)", deployRunbookRel)
	}
	if !containsAny(doc, "neon") {
		t.Errorf("[P0] %s must name Neon as the application database (H3)", deployRunbookRel)
	}
	// Carried forward from the backup runbook §2.2/§7 so it is not lost: Free
	// tier PITR is 6h, which does not cover an event plus its dispute window.
	if !containsAny(doc, "plan", "pitr", "upgrade") {
		t.Errorf("[P1] %s must restate the pre-event Neon plan upgrade (Free = 6h PITR is not enough for the event + dispute window) so it is not lost between runbooks (Task 5)", deployRunbookRel)
	}
}

func TestDeploymentRunbook_ReferencesTheSmokeScriptAndTracksThePaidStepsAsUncheckedBoxes(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)

	if !containsAny(doc, "smoke.sh") {
		t.Errorf("[P0] %s must reference api/scripts/smoke.sh as the \"did the deploy actually work\" check (Task 6)", deployRunbookRel)
	}

	// H5: this slice lands `blocked`, and the remaining ACs are gated on console
	// steps that cost money. Tracking them as unchecked boxes is what lets a
	// human see at a glance what is still owed — the same convention the backup
	// runbook uses and TestRunbook_TracksBlockedOperatorStepsAsUncheckedBoxes
	// enforces next door.
	boxes := uncheckedBoxes(doc)
	if len(boxes) == 0 {
		t.Errorf("[P0] %s tracks no unchecked operator steps — the Neon role bootstrap, the Fly deploy, the Vercel env vars, the Supabase redirect URL and the plan upgrade are all human/paid steps (H5) and must be visible as `- [ ]` items, not buried in prose", deployRunbookRel)
	}
	joined := strings.ToLower(strings.Join(boxes, "\n"))
	for _, step := range []struct{ key, why string }{
		{"neon", "the od_runtime/od_migrator bootstrap and migrations on the live DB (AC 4)"},
		{"fly", "the secrets + deploy + scale steps (AC 1/2)"},
		{"vercel", "the root directory and the three VITE_* env vars (AC 3/6)"},
		{"supabase", "the redirect URL addition without which OAuth cannot return (AC 6)"},
	} {
		if !strings.Contains(joined, step.key) {
			t.Errorf("[P0] %s's operator checklist has no unchecked step mentioning %q — %s", deployRunbookRel, step.key, step.why)
		}
	}
}

func TestDeploymentRunbook_CarriesNoCredentials(t *testing.T) {
	doc := readArtifact(t, deployRunbookRel, deployRunbookWhy)
	assertNoSecrets(t, deployRunbookRel, doc)
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite G — the backup runbook's blocked items become actionable (AC 5)
// ─────────────────────────────────────────────────────────────────────────────

func TestBackupRunbook_DeployedURLAndHostingItemsAreNoLongerBlocked(t *testing.T) {
	doc := readArtifact(t, runbookRel, runbookWhy)

	// The backup-and-monitoring work could not name a URL or a host, so it parked
	// these. This slice's whole job is to supply both; leaving the markers in
	// place would mean the monitoring gap silently survives the change that
	// closed it.
	for _, marker := range []string{"[blocked: deployed URL]", "[blocked: hosting]"} {
		if strings.Contains(doc, marker) {
			t.Errorf("[P0] %s still carries %q — the deployed URL and the host are now known, so these items must become actionable (point the uptime monitor at the /health origin) rather than staying parked (AC 5, Task 5)", runbookRel, marker)
		}
	}
	if !containsAny(doc, "deployment-runbook") {
		t.Errorf("[P0] %s must cross-link docs/deployment-runbook.md — the two runbooks share the restore/role-bootstrap procedure and must not duplicate it (Task 5)", runbookRel)
	}

	// GUARD AGAINST A CROSS-SUITE CONFLICT.
	//
	// "Unblock these items" is one edit away from "tick these boxes", and ticking
	// them breaks the backup runbook's TestRunbook_TracksBlockedOperatorStepsAsUncheckedBoxes,
	// which requires at least three UNCHECKED `- [ ]` items including an
	// Uptime-Robot one. That failure would surface in a different file, for a
	// different suite, with no hint that this change caused it.
	//
	// Unblocked is not the same as done: the URL is supplied, but a human still
	// has to register the monitor, which is why this slice itself lands `blocked`.
	// So the box must survive — with a real endpoint in it instead of a marker.
	boxes := uncheckedBoxes(doc)
	if len(boxes) < 3 {
		t.Errorf("[P0] %s now has %d unchecked operator items; the backup runbook's suite requires at least 3.\n  Making an item ACTIONABLE means replacing the [blocked: …] marker with the real endpoint — NOT checking the box or deleting the item. The human still has to register the monitor, which is why this slice lands `blocked`.", runbookRel, len(boxes))
	}
	monitorBox := false
	for _, box := range boxes {
		if containsAny(box, "uptime robot", "uptimerobot") && containsAny(box, "monitor") {
			monitorBox = true
		}
	}
	if !monitorBox {
		t.Errorf("[P0] %s no longer has an UNCHECKED Uptime Robot monitor item — the backup runbook's TestRunbook_TracksBlockedOperatorStepsAsUncheckedBoxes requires one, and registering the monitor is still a human step even now that the URL is known (AC 5)", runbookRel)
	}
}
