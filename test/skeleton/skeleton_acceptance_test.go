// Package skeleton holds the RED-PHASE ATDD acceptance harness for Story 1.1
// "Monorepo skeleton & workspace wiring with CI scaffold".
//
// Go analog of Playwright's test.skip(). The assertions encode the EXPECTED end
// state of the skeleton; they FAIL against today's experimental tree (root Go
// module + greeting sample, frontend/ Expo experiment, single .github/workflows/
// test.yml, and no api/ contracts/ mobile/ web/ tree).
//
// ACTIVATION (green phase), task-by-task:
//  1. Implement the task in Story 1.1 that satisfies a scenario.
//  3. Run: `cd test/skeleton && go test ./...`
//  4. Confirm the just-activated test was RED before the change and GREEN after.
//  5. Commit.
//
// Run all: `cd test/skeleton && go test -v ./...`
// Source of truth: _bmad-output/implementation-artifacts/1-1-monorepo-skeleton-workspace-wiring-with-ci-scaffold.md
package skeleton

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// repoRoot returns the monorepo root. This package lives at <root>/test/skeleton,
// so the root is two directories up from the package working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("cannot resolve repo root: %v", err)
	}
	return root
}

func exists(t *testing.T, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

func isDir(t *testing.T, parts ...string) bool {
	t.Helper()
	info, err := os.Stat(filepath.Join(parts...))
	return err == nil && info.IsDir()
}

func mustReadFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("cannot read %s: %v", filepath.Join(parts...), err)
	}
	return string(b)
}

// requireDir fails the test if the path is not an existing directory.
func requireDir(t *testing.T, root string, rel string) {
	t.Helper()
	if !isDir(t, filepath.Join(root, rel)) {
		t.Errorf("expected directory %q to exist", rel)
	}
}

// requireFile fails the test if the path is not an existing file.
func requireFile(t *testing.T, root string, rel string) {
	t.Helper()
	if !exists(t, filepath.Join(root, rel)) {
		t.Errorf("expected file %q to exist", rel)
	}
}

// requireAbsent fails the test if the path exists (used for reconciliation/negative ACs).
func requireAbsent(t *testing.T, root string, rel string) {
	t.Helper()
	if exists(t, filepath.Join(root, rel)) {
		t.Errorf("expected %q to be absent (reconciled/removed), but it exists", rel)
	}
}

// requireContains fails if the file content does not contain every needle.
func requireContains(t *testing.T, root string, rel string, needles ...string) {
	t.Helper()
	content := mustReadFile(t, filepath.Join(root, rel))
	for _, n := range needles {
		if !strings.Contains(content, n) {
			t.Errorf("expected %q to contain %q", rel, n)
		}
	}
}

// runCmd runs a command in dir and returns combined output + error.
func runCmd(t *testing.T, dir, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---------------------------------------------------------------------------
// AC1 — Skeleton dirs exist and workspaces resolve
// ---------------------------------------------------------------------------

// [P0] AC1: the four surface directories exist at the repo root.
func TestAC1_SurfaceDirectoriesExist(t *testing.T) {
	root := repoRoot(t)
	for _, d := range []string{"contracts", "api", "mobile", "web"} {
		requireDir(t, root, d)
	}
}

// [P0] AC1: go.work exists, ties api/ + contracts/generated/go, and `go work sync` resolves.
func TestAC1_GoWorkResolves(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, "go.work")
	requireContains(t, root, "go.work", "./api", "./contracts/generated/go")

	if out, err := runCmd(t, root, "go", "work", "sync"); err != nil {
		t.Errorf("`go work sync` must succeed, got error: %v\n%s", err, out)
	}
}

// [P0] AC1: pnpm-workspace.yaml declares the JS packages and `pnpm install` resolves.
func TestAC1_PnpmWorkspaceResolves(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, "pnpm-workspace.yaml")
	requireContains(t, root, "pnpm-workspace.yaml", "contracts/generated/ts", "mobile", "web")

	// Offline resolution of the empty workspace; --frozen-lockfile avoids surprise writes.
	if out, err := runCmd(t, root, "pnpm", "install", "--frozen-lockfile", "--prefer-offline"); err != nil {
		t.Errorf("`pnpm install` must resolve the workspace, got error: %v\n%s", err, out)
	}
}

// [P0] AC1/DoD: the repo root is NOT a Go module and the experimental greeting sample is gone.
func TestAC1_RootGoSampleReconciled(t *testing.T) {
	root := repoRoot(t)

	// Root must not be a Go module (it is the pnpm + go.work workspace root).
	if exists(t, filepath.Join(root, "go.mod")) {
		if strings.Contains(mustReadFile(t, filepath.Join(root, "go.mod")), "module OD-amrita") {
			t.Errorf("repo root still declares `module OD-amrita`; root must not be a Go module")
		}
	}
	// The throwaway sample files/dirs must be removed (not preserved as product code).
	for _, rel := range []string{"main.go", "greeting.go", "greeting_test.go", "testutil", "testdata", "coverage.out"} {
		requireAbsent(t, root, rel)
	}
}

// ---------------------------------------------------------------------------
// AC2 — api/ Go module with layered layout
// ---------------------------------------------------------------------------

// [P0] AC2: api/ is a Go module with the repo-correct module path.
func TestAC2_ApiModulePath(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, "api/go.mod")
	requireContains(t, root, "api/go.mod", "module github.com/Shruhath/OD-Amrita/api")
}

// [P0] AC2: two operational entrypoints — the API server and River migration
// command. River workers run inside cmd/api; there is intentionally no separate
// cmd/worker binary after the worker was embedded in the API process.
func TestAC2_TwoEntrypoints(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, "api/cmd/api/main.go")
	requireFile(t, root, "api/cmd/river-migrate/main.go")
}

// [P0] AC2: the layered internal/ package structure exists.
func TestAC2_LayeredInternalLayout(t *testing.T) {
	root := repoRoot(t)
	for _, pkg := range []string{"handler", "service", "store", "middleware", "job", "domain"} {
		requireDir(t, root, filepath.Join("api", "internal", pkg))
	}
}

// [P1] AC2: the ★ co-commit / registry placeholder files exist (homes only, no logic yet).
func TestAC2_MarkerPlaceholderFiles(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, "api/internal/store/tx.go")
	requireFile(t, root, "api/internal/domain/errors.go")
	requireFile(t, root, "api/internal/domain/events.go")
}

// [P0] AC2: the api/ module compiles.
func TestAC2_ApiBuilds(t *testing.T) {
	root := repoRoot(t)
	apiDir := filepath.Join(root, "api")
	if out, err := runCmd(t, apiDir, "go", "build", "./..."); err != nil {
		t.Errorf("`go build ./...` in api/ must succeed, got error: %v\n%s", err, out)
	}
}

// ---------------------------------------------------------------------------
// AC3 — mobile reserves admin-gated NFC Populator mode (no separate repo)
// ---------------------------------------------------------------------------

// [P1] AC3: the populator feature folder is reserved inside the mobile app.
func TestAC3_PopulatorFeatureReserved(t *testing.T) {
	root := repoRoot(t)
	requireDir(t, root, "mobile/src/features/populator")
}

// [P1] AC3: the shared NFC/encode module location is reserved (shared with the coordinator scanner).
func TestAC3_SharedNfcEncodeReserved(t *testing.T) {
	root := repoRoot(t)
	requireDir(t, root, "mobile/src/lib/nfc")
	requireDir(t, root, "mobile/src/lib/encode")
}

// [P2] AC3: populator is a MODE not a repo — README states admin-RBAC-gating; no separate deployable exists.
func TestAC3_PopulatorIsModeNotRepo(t *testing.T) {
	root := repoRoot(t)
	readme := filepath.Join("mobile", "src", "features", "populator", "README.md")
	requireFile(t, root, readme)
	content := strings.ToLower(mustReadFile(t, filepath.Join(root, readme)))
	if !strings.Contains(content, "admin") || (!strings.Contains(content, "rbac") && !strings.Contains(content, "gated")) {
		t.Errorf("features/populator/README.md must state the mode is admin-RBAC-gated")
	}
	// There must be NO separate od-amrita-nfc-populator deployable/repo folder (AR16).
	requireAbsent(t, root, "od-amrita-nfc-populator")
}

// [P1] AC3/DoD: no `anokha` identity is carried from the throwaway frontend/ into mobile/.
func TestAC3_NoAnokhaIdentityInMobile(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"mobile/package.json", "mobile/app.json", "mobile/app.config.js", "mobile/app.config.ts"} {
		p := filepath.Join(root, rel)
		if !exists(t, p) {
			continue
		}
		if strings.Contains(strings.ToLower(mustReadFile(t, p)), "anokha") {
			t.Errorf("%s must not carry `anokha` identity; app identity is OD-Amrita", rel)
		}
	}
}

// ---------------------------------------------------------------------------
// AC4 — per-surface, path-filtered CI
// ---------------------------------------------------------------------------

// [P0] AC4: the four per-surface workflow files exist.
func TestAC4_FourWorkflowsExist(t *testing.T) {
	root := repoRoot(t)
	for _, wf := range []string{"contract-drift.yml", "api-ci.yml", "mobile-ci.yml", "web-deploy.yml"} {
		requireFile(t, root, filepath.Join(".github", "workflows", wf))
	}
}

// [P0] AC4: each surface workflow has the correct path filter.
func TestAC4_PathFiltersCorrect(t *testing.T) {
	root := repoRoot(t)
	requireContains(t, root, ".github/workflows/api-ci.yml", "paths:", "api/**", "contracts/**")
	requireContains(t, root, ".github/workflows/mobile-ci.yml", "paths:", "mobile/**", "contracts/**")
	requireContains(t, root, ".github/workflows/web-deploy.yml", "paths:", "web/**", "contracts/**")
}

// [P1] AC4/DoD: the experimental single test.yml is reconciled away.
func TestAC4_LegacyTestYmlRemoved(t *testing.T) {
	root := repoRoot(t)
	requireAbsent(t, root, ".github/workflows/test.yml")
}

// [P0] AC4: each workflow looks like a well-formed workflow (name/on/jobs present).
// NOTE: stdlib has no YAML parser; this is a lightweight structural check. Full YAML
// validity + "green on empty skeleton" is enforced by GitHub Actions / actionlint in CI.
func TestAC4_WorkflowsWellFormed(t *testing.T) {
	root := repoRoot(t)
	for _, wf := range []string{"contract-drift.yml", "api-ci.yml", "mobile-ci.yml", "web-deploy.yml"} {
		requireContains(t, root, filepath.Join(".github", "workflows", wf), "on:", "jobs:")
	}
}

// ---------------------------------------------------------------------------
// AC5 — TLS 1.2+, no HTTP fallback (documented config-level assertion in this story)
// ---------------------------------------------------------------------------

// [P1] AC5/NFR15: a documented TLS-only / no-HTTP-fallback assertion exists somewhere authoritative.
func TestAC5_TlsOnlyDocumented(t *testing.T) {
	root := repoRoot(t)
	candidates := []string{"README.md", "api/.env.example", "api/cmd/api/main.go"}
	found := false
	for _, rel := range candidates {
		p := filepath.Join(root, rel)
		if !exists(t, p) {
			continue
		}
		c := strings.ToLower(mustReadFile(t, p))
		if strings.Contains(c, "tls") && (strings.Contains(c, "no http") || strings.Contains(c, "no-http") || strings.Contains(c, "tls-only") || strings.Contains(c, "tls only")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a documented TLS 1.2+/no-HTTP-fallback assertion in one of %v (NFR15)", candidates)
	}
}

// [P2] AC5: guardrail — the skeleton must NOT implement an HTTP->HTTPS redirect listener (plain HTTP is rejected, not redirected).
func TestAC5_NoHttpRedirectListener(t *testing.T) {
	root := repoRoot(t)
	apiDir := filepath.Join(root, "api")
	if !isDir(t, apiDir) {
		t.Errorf("api/ must exist before this guard is meaningful")
		return
	}
	_ = filepath.Walk(apiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src := string(mustReadFile(t, path))
		// NFR15: plain HTTP is rejected, not redirected. Catch the common
		// HTTP->HTTPS redirect idioms — any redirect status constant, an
		// http.Redirect call, or a plain-HTTP :80 listener (however the Addr
		// is spelled). These constants/tokens do not appear in the skeleton's
		// TLS-only prose comments, so they only match real redirect code.
		forbidden := []string{
			"StatusMovedPermanently",  // 301
			"StatusFound",             // 302
			"StatusSeeOther",          // 303
			"StatusTemporaryRedirect", // 307
			"StatusPermanentRedirect", // 308
			"http.Redirect(",
			`ListenAndServe(":80"`,
			`Addr: ":80"`,
			`Addr:":80"`,
		}
		for _, tok := range forbidden {
			if strings.Contains(src, tok) {
				t.Errorf("%s appears to implement an HTTP->HTTPS redirect listener (matched %q); NFR15 rejects HTTP, does not redirect", path, tok)
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// DoD — .gitignore reconciliation (surface Makefiles tracked)
// ---------------------------------------------------------------------------

// [P1] DoD: .gitignore must not blanket-ignore Makefile so surface Makefiles are tracked.
// NOTE: this checks the current over-broad `Makefile` ignore line is gone and the surface
// Makefiles are present on disk; true git-ignore semantics are verified by CI/`git check-ignore`.
func TestDoD_GitignoreTracksSurfaceMakefiles(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, ".gitignore")
	for _, line := range strings.Split(mustReadFile(t, filepath.Join(root, ".gitignore")), "\n") {
		if strings.TrimSpace(line) == "Makefile" {
			t.Errorf(".gitignore blanket-ignores `Makefile`; surface Makefiles (contracts/Makefile, api/Makefile) must be tracked")
		}
	}
	requireFile(t, root, "contracts/Makefile")
	requireFile(t, root, "api/Makefile")
}
