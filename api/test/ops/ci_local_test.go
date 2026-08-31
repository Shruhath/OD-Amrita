package ops

// ci_local_test.go — the guard on the ONE script that can point a destructive
// job at production.
//
// WHAT HAPPENED, so nobody softens these assertions later.
//
// act reads `.env` from its working directory by default and injects every entry
// into the job container, where it OVERRIDES the `env:` block the workflow file
// declares. This repository's gitignored `.env` holds the live Neon
// SUPERUSER_DSN, DATABASE_URL_MIGRATOR and DATABASE_URL_RUNTIME, and api-ci's
// integration job declares those same three names pointing at its own throwaway
// postgres service. Unguarded, a local run therefore aims the superuser role
// bootstrap, `goose up`, and the reversibility gate's `goose down-to 0` at the
// database holding real attendance. `down-to 0` drops every table.
//
// The repository `.actrc` disarms it with --env-file=/dev/null, and .actrc is
// read only from the CURRENT WORKING DIRECTORY. The first version of
// scripts/ci_local.sh cd'd into api/ before calling act, so the guard silently
// did not apply and the run reached `psql "$SUPERUSER_DSN"` aimed at Neon. It
// failed with "Connection refused" only because the network this project is
// developed on blocks outbound 5432. That is not a safeguard — it is a network
// this project does not control, and the same run on a phone hotspot succeeds.
//
// So the safety is asserted here, in a test that runs in the ordinary untagged
// build job, rather than left to a comment in a shell script.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	ciLocalRel = "api/scripts/ci_local.sh"
	ciLocalWhy = "it is the only way to run the CI gate while GitHub Actions is billing-blocked"
)

func TestCILocalScript_ExistsAndIsExecutable(t *testing.T) {
	script := readArtifact(t, ciLocalRel, ciLocalWhy)
	if strings.TrimSpace(script) == "" {
		t.Fatalf("[P0] %s exists but is empty", ciLocalRel)
	}
	info, err := os.Stat(filepath.Join(repoRoot(t), ciLocalRel))
	if err != nil {
		t.Fatalf("[P0] stat %s: %v", ciLocalRel, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("[P0] %s is not executable (chmod +x)", ciLocalRel)
	}
	if !regexp.MustCompile(`set -euo pipefail`).MatchString(script) {
		t.Errorf("[P0] %s must `set -euo pipefail` — a run that continues past a failed job reports a green gate for a broken build", ciLocalRel)
	}
	assertNoSecrets(t, ciLocalRel, script)
}

// The three defences, each asserted separately so a failure names the one that
// was removed.
func TestCILocalScript_CannotHandProductionCredentialsToAct(t *testing.T) {
	// COMMENTS STRIPPED FIRST, and this is not a detail. The header of
	// ci_local.sh explains all three defences and therefore contains every
	// string below. The first version of this test read the raw file and passed
	// with the actual --env-file flag deleted, asserting nothing but that the
	// warning was still written down.
	script := stripFullLineShellComments(readArtifact(t, ciLocalRel, ciLocalWhy))

	if !strings.Contains(script, "--env-file=/dev/null") {
		t.Error("[P0] ci_local.sh must pass --env-file=/dev/null EXPLICITLY. " +
			"Relying on .actrc alone is what aimed `psql $SUPERUSER_DSN` at production Neon: .actrc is read " +
			"from the working directory, and the script had cd'd elsewhere.")
	}
	if !strings.Contains(script, "--secret-file=/dev/null") {
		t.Error("[P0] ci_local.sh must pass --secret-file=/dev/null EXPLICITLY, for the same reason as --env-file")
	}
	if !strings.Contains(script, "env -u SUPERUSER_DSN") {
		t.Error("[P0] ci_local.sh must strip SUPERUSER_DSN from act's environment. " +
			"An operator who ran `. ./.env` in the same shell hands it over otherwise.")
	}
	for _, name := range []string{"DATABASE_URL_MIGRATOR", "DATABASE_URL_RUNTIME"} {
		if !strings.Contains(script, "-u "+name) {
			t.Errorf("[P0] ci_local.sh must strip %s from act's environment; the integration job declares that "+
				"same name for its throwaway postgres, and an inherited value silently overrides it", name)
		}
	}
}

// .actrc is found relative to the working directory, so where act is invoked
// FROM is a safety property, not a stylistic one.
func TestCILocalScript_InvokesActFromTheRepositoryRoot(t *testing.T) {
	script := stripFullLineShellComments(readArtifact(t, ciLocalRel, ciLocalWhy))

	cd := strings.Index(script, `cd "$repo_root"`)
	act := strings.Index(script, "act push")
	if cd < 0 {
		t.Fatal(`[P0] ci_local.sh does not cd to the repository root; .actrc will not be found and .env will be loaded`)
	}
	if act < 0 {
		t.Fatal("[P0] ci_local.sh no longer invokes `act push` in the expected form; re-check this guard")
	}
	if cd > act {
		t.Error(`[P0] ci_local.sh cds to the repository root AFTER invoking act; .actrc is read from the working directory at invocation`)
	}
}

// The .actrc guard itself. Deleting those two lines re-arms the same accident for
// anyone running act by hand, which is how it happened the first time.
func TestActRC_StillDisarmsTheDotEnvLoad(t *testing.T) {
	actrc := stripFullLineShellComments(readArtifact(t, ".actrc",
		"it is what stops a hand-run of act loading the live Neon credentials out of .env"))

	if !strings.Contains(actrc, "--env-file=/dev/null") {
		t.Error("[P0] .actrc no longer passes --env-file=/dev/null. " +
			"act loads .env by default and its values OVERRIDE the workflow's env: block, so api-ci's integration " +
			"job — role bootstrap, goose up, goose down-to 0 — would run against production.")
	}
	if !strings.Contains(actrc, "--secret-file=/dev/null") {
		t.Error("[P0] .actrc no longer passes --secret-file=/dev/null")
	}
}
