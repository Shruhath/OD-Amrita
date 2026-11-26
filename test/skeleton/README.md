# Skeleton Acceptance Tests

Acceptance tests for the **monorepo skeleton, workspace wiring and CI scaffold**.

They are **structural + toolchain** assertions (no business logic): each one checks part of the expected layout of the repository — surface directories, Go and pnpm workspaces, the api module's entrypoints and layout, the CI workflows and their path filters, and the documented TLS-only posture.

## Run

```bash
cd test/skeleton
go test -v ./...
```

Standalone stdlib-only module — no dependency on `api/`. Optionally wire into the workspace: `go work use ./test/skeleton`. CI runs it in the `api-ci` acceptance job.

## Scenario → Acceptance Criterion map

| Test func | AC | Prio |
|-----------|----|----|
| `TestAC1_SurfaceDirectoriesExist` | AC1 | P0 |
| `TestAC1_GoWorkResolves` | AC1 | P0 |
| `TestAC1_PnpmWorkspaceResolves` | AC1 | P0 |
| `TestAC1_RootGoSampleReconciled` | AC1/DoD | P0 |
| `TestAC2_ApiModulePath` | AC2 | P0 |
| `TestAC2_TwoEntrypoints` | AC2 | P0 |
| `TestAC2_LayeredInternalLayout` | AC2 | P0 |
| `TestAC2_MarkerPlaceholderFiles` | AC2 | P1 |
| `TestAC2_ApiBuilds` | AC2 | P0 |
| `TestAC3_PopulatorFeatureReserved` | AC3 | P1 |
| `TestAC3_SharedNfcEncodeReserved` | AC3 | P1 |
| `TestAC3_PopulatorIsModeNotRepo` | AC3 | P2 |
| `TestAC3_NoAnokhaIdentityInMobile` | AC3/DoD | P1 |
| `TestAC4_FourWorkflowsExist` | AC4 | P0 |
| `TestAC4_PathFiltersCorrect` | AC4 | P0 |
| `TestAC4_LegacyTestYmlRemoved` | AC4/DoD | P1 |
| `TestAC4_WorkflowsWellFormed` | AC4 | P0 |
| `TestAC5_TlsOnlyDocumented` | AC5/NFR15 | P1 |
| `TestAC5_NoHttpRedirectListener` | AC5 | P2 |
| `TestDoD_GitignoreTracksSurfaceMakefiles` | DoD | P1 |

## Known limitations (documented, not defects)

- **YAML validity** (`TestAC4_WorkflowsWellFormed`) is a lightweight substring check — stdlib has no YAML parser. True validity + "green on empty skeleton" is enforced by GitHub Actions / `actionlint` in CI.
- **git-ignore semantics** (`TestDoD_GitignoreTracksSurfaceMakefiles`) are approximated by checking the over-broad `Makefile` line is gone + the Makefiles exist on disk. Exact behavior is verified by `git check-ignore` in CI.
- `TestAC1_PnpmWorkspaceResolves` runs `pnpm install --frozen-lockfile --prefer-offline`; ensure pnpm is available on the runner.
