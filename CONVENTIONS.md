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

## Database access (dual roles)

- Two login roles, two DSNs: **`od_migrator`** (DDL — owns the schema, runs
  goose via `DATABASE_URL_MIGRATOR`) and **`od_runtime`** (the app pool via
  `DATABASE_URL_RUNTIME`, **DML only — no `CREATE`/`ALTER`/`DROP`, owns
  nothing**). Enforced by grants (`db/init/00_roles.sql` + migrations), verified
  by the integration role-denial tests (`api/test/db`).
- The app **never** connects as `od_migrator`. New tables reach `od_runtime`
  via `ALTER DEFAULT PRIVILEGES` — do not hand-grant per table.
- Login roles are bootstrapped by a superuser (`db/init/00_roles.sql` — docker
  initdb locally, documented operator step in prod); goose owns schema + grants.

## Audit & write boundary

- **`audit_log` is append-only.** Immutability is enforced by an `ENABLE ALWAYS`
  DB trigger that RAISEs on `UPDATE`/`DELETE`/`TRUNCATE` — **not** by grants (a
  superuser/service role bypasses grants; the trigger fires for every role,
  including the owner). `od_runtime` holds only `SELECT, INSERT` on it — never
  `UPDATE`/`DELETE`/`TRUNCATE`. Genuine break-glass (a superuser disabling the
  trigger) is explicitly out-of-band.
- **Every state-changing write goes through `store.WithAudit` (`store/tx.go`)** —
  it co-commits the business write and its audit row in ONE transaction, so no
  endpoint or worker job writes without an audit row. A dedup no-op
  (`changed == false`) commits with no audit row. The business write MUST run on
  the passed `tx` (`sqlc.New(tx)`), never on the pool. First real caller:
  `internal/service/user` (first-login provisioning, Story 1.5b).
- **Users are provisioned at login, not per request.** A verified Supabase
  identity is upserted into `users` at the `GET /api/v1/auth/me` round-trip (keyed
  on the stable `supabase_subject`, never email) via `store.WithAudit` — a first
  insert writes a `user.provisioned` audit row; a returning login only bumps
  `last_login_at` (no audit row). The auth middleware stays a pure verify+enforce
  gate; per-request role *loading* is Epic 2. Default role is the least-privileged
  `student`; only the `AUTH_BOOTSTRAP_ADMINS` allowlist grants `admin` at
  provisioning. Role-scoped **session durations are Supabase config, not Go code**.

## Registry discipline (no free-text codes or events)

- **Error codes:** the OpenAPI `Error.code` enum (`contracts/openapi.yaml`) is
  the SSOT. A story adding a code edits `openapi.yaml` → regenerates → adds the
  `codeToStatus` mapping in `api/internal/domain/errors.go` (never re-declares the
  code in Go). The parity/coverage test fails CI on any contract code without a
  mapping, any stale mapping, or a count mismatch. Never a free-text code.
- **Audit actions:** `api/internal/domain/events.go` + its `AllActions` slice is
  the SSOT (audit events are **not** in the contract). Every story that
  introduces a new auditable action adds its `domain.Action` constant there AND
  to `AllActions`. `store.WithAudit` rejects any unregistered action at runtime;
  a unit test fails if a declared constant is missing from `AllActions`. Never a
  free-text event.

## Auth boundary

- Supabase Auth brokers Microsoft OAuth and owns session refresh/revocation. The
  Go backend verifies Supabase's JWT locally through JWKS, checks issuer,
  audience and expiry, then independently enforces the `amrita.edu` domain
  family on every protected request. Do not trust provider/dashboard filtering
  as the only admission gate.
- `api/internal/middleware/error` is the single renderer for generated
  `{code, message, details}` error envelopes. Handlers and middleware push
  `domain.DomainError` with `c.Error(...)` and abort; they do not hand-write
  error response bodies.

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
