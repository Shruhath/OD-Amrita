# Skeleton ATDD Acceptance Harness — Story 1.1

Red-phase acceptance tests for **Story 1.1: Monorepo skeleton & workspace wiring with CI scaffold**.

These are the executable form of the story's acceptance criteria. They are **structural + toolchain** assertions (no business logic — that arrives in Story 1.2+). Every test starts `SKIP`ped (`t.Skip("RED: ...")`) and asserts the *expected* end state of the skeleton, so it fails against the current experimental tree and passes once the skeleton is built.

## Run

```bash
cd test/skeleton
go test -v ./...      # all SKIP in red phase
```

Standalone stdlib-only module — no dependency on `api/`, and it survives the root-Go-module reconciliation. Optionally wire into the workspace in the green phase: `go work use ./test/skeleton`.

## Activation (green phase) — one scenario at a time

1. Implement the Story 1.1 task that satisfies a scenario.
2. Delete the single `t.Skip("RED: ...")` line at the top of that scenario's `Test...` function.
3. `go test -v ./...` — confirm it was RED before your change and is GREEN after.
4. Commit.

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
- `TestAC1_PnpmWorkspaceResolves` runs `pnpm install --frozen-lockfile --prefer-offline`; ensure pnpm is available on the runner when activated.
