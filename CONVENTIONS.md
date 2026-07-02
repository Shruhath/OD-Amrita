# Conventions (LOCKED)

Hand-written, short, enforced by CI where machine-checkable. Source:
architecture#Naming-Patterns and architecture#Documentation-&-Enforcement.

## Naming

| Domain | Rule |
|--------|------|
| Database | `snake_case`, **plural** table names |
| JSON (wire) | `snake_case` **end-to-end** (request + response) |
| Go files | `snake_case.go`; exported identifiers `PascalCase` |
| Go tests | co-located `*_test.go` |
| TS components | `PascalCase` filenames; hooks named `useThing` |
| TS tests | co-located `*.test.ts(x)` |
| goose migrations | `db/migrations/NNNN_desc.sql` (zero-padded sequence) |

## Repository & module identity

- Remote: `github.com/Shruhath/OD-Amrita`.
- `api/` Go module path: `github.com/Shruhath/OD-Amrita/api`.
- Single **monorepo** — no polyrepo, **no separate `od-amrita-nfc-populator` repo**.
  The populator is an admin-gated **mode** inside `mobile/` (AR16).
- **One branch per epic** — all of an epic's work lands on that single branch
  (Epic 1 → `1-monorepo`). No per-feature sub-branches.

## Stack (fixed)

- Backend: Go + **gin** + **sqlc** + **goose**, **no ORM**.
  ("Drizzle" here = drizzle-gateway DB browser, not an ORM.)
- `api/` has two entrypoints: `cmd/api` (server) + `cmd/worker` (persistent River worker).
  Deploy target is an **always-on container** — no serverless.
- Mobile: Expo **dev client + prebuild** (native modules require it).
- Web: React + **Vite**.

## Contract wiring (no publishing)

- Go: `go.work` direct reference — `api/` ↔ `contracts/generated/go`.
- TS: pnpm workspace — `"@od-amrita/contracts": "workspace:*"`.
- The **drift-gate** (`make generate && git diff --exit-code`) is the single
  source-of-truth enforcer; codegen output under `contracts/generated/**` is
  **`DO NOT EDIT`**.
- `contracts/openapi.yaml` is **OpenAPI 3.0.3 on purpose — do NOT upgrade to
  3.1**: `oapi-codegen` v2 (kin-openapi) cannot parse 3.1, and 3.0.3 is the only
  version both it and `openapi-typescript` consume from one SSOT. Use
  `nullable: true`, never the 3.1 `type: [T, "null"]` union.

## CI is where machine-checkable truth lives

- Contract codegen drift-gate.
- `golangci-lint` + `gofmt` (api).
- `sqlc` compile-check (api).
- ESLint + Prettier + `tsc --noEmit` (mobile/web).

Individual checks are added to each surface's workflow as that surface gains
content; the skeleton wires the workflow shells.
