# Contract acceptance harness

Executable acceptance tests for the **spec-first OpenAPI contract, codegen &
drift-gate**. Sibling to `test/skeleton/`. Each test asserts the expected end
state of the contract wiring, so the suite doubles as the definition-of-done for
the `contracts/` surface.

## Run

```bash
cd test/contract
go mod tidy               # first run only — fetches gopkg.in/yaml.v3
GOWORK=off go test -v ./...
```

Run a single scenario:

```bash
GOWORK=off go test -run TestAC1_SpecIsOpenAPI303 -v ./...
```

> This module is **not** in the repo `go.work`, so `GOWORK=off` is required (the
> workspace would otherwise refuse a module it doesn't list).

## What it asserts (3 levels)

| Level | Covers |
|-------|--------|
| **Contract** (semantic YAML parse) | `openapi: 3.0.3`, `/health` static shape, the `Error` envelope + code-enum registry, typed `ValidationErrorDetail[]`, the 202 batch scan request/response, UUIDv7 semantics on `id` |
| **Static/Config** | `oapi-codegen.yaml`, the real pinned Makefile, root `openapi-typescript` devDep, `contract-drift.yml` toolchain setup, `.gitattributes`, `DO NOT EDIT` headers, preserved module/package identity |
| **Integration** (exec + git) | real codegen, byte-idempotency, drift-gate **both directions** (stale spec + hand-edited output), `api/` build, `web/` typecheck |

## Design notes

- **`assertRealCodegen` guard** — the drift-gate / build tests only carry signal
  once real generated output exists (a no-op stub is trivially idempotent and its
  placeholder output builds). Each such test calls this guard first.
- **Negative drift tests are self-restoring** — `TestIntegration_DriftGateFailsOn*`
  mutate a file, assert `git diff --exit-code` fails, then restore via `t.Cleanup`.
  They **skip** (not fail) if the target path is already dirty, so they never
  clobber local work.
- **Environmental skips** — integration tests `t.Skip` (not fail) when `make`,
  `git`, `go`, or `pnpm` are absent.
- **No new source-of-truth artifacts** — the gate *is* the test.
