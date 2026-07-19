# OD-Amrita

Monorepo for OD-Amrita — the OD (On-Duty) attendance/scan platform for Amrita.
A single atomic base for four coupled surfaces, each with its own path-filtered
CI pipeline (AR1).

## Surfaces

| Path         | Surface | Stack                                                        |
|--------------|---------|-------------------------------------------------------------|
| `contracts/` | Contract SSOT | OpenAPI spec → generated Go + TS clients (path/workspace reference, **not** a published package) |
| `api/`       | Backend | Go + **gin** + **sqlc** + **goose** (no ORM). Two entrypoints: `cmd/api` (HTTP server) + `cmd/worker` (persistent River worker). Always-on container — **no serverless**. |
| `mobile/`    | Mobile  | Expo dev client + prebuild (React Native). Coordinator scanner **and** the admin-gated NFC **Populator mode** (AR16 — no separate repo/PWA). |
| `web/`       | Web     | React + Vite SPA (portal + public route trees, AR15).       |

> **"Drizzle" in this project = drizzle-gateway / Drizzle Studio** (a visual DB
> browser on `:4983`), **NOT** an ORM. There is no Drizzle ORM dependency.

## Workspace wiring (no publishing)

- **Go:** `go.work` ties `./api` ↔ `./contracts/generated/go` (direct import; no `go get`/tag).
- **TS:** `pnpm-workspace.yaml` + `"@od-amrita/contracts": "workspace:*"` in `mobile/` and `web/`.

```bash
go work sync                 # resolve the Go workspace
pnpm install                 # resolve the JS/TS workspace
cd api && go build ./...     # compile the backend
```

## Transport security (NFR15) — TLS-only, no HTTP fallback

The deployment binds **TLS 1.2+ only**. Plain HTTP is **rejected, not redirected** —
the server does **not** run an `HTTP→HTTPS` redirect listener (no `:80` listener,
no `301`/`StatusMovedPermanently`). This is a documented config-level assertion in
this skeleton story; the enforcing bootstrap lands with the first HTTP handler
(Story 1.2/1.3). See `api/.env.example` and `api/cmd/api/main.go`.

## CI (per-surface, path-filtered)

Four workflows under `.github/workflows/`:
`contract-drift.yml`, `api-ci.yml`, `mobile-ci.yml`, `web-deploy.yml` — each with
`paths:` filters so a surface's pipeline runs only when it (or `contracts/`) changes.

## Conventions

See [`CONVENTIONS.md`](./CONVENTIONS.md) for the locked naming and layout rules.

## Walking skeleton — implemented and CI-gated, device closure pending

The Epic 1 exit thread — **device → contract → API → `store/tx.go` → Postgres →
immutable audit row** — is implemented and CI-enforced, but not yet *closed*: a
walking skeleton isn't proven until it has actually walked on a real device.

**Proven so far:**
- The backend half was proven live on 2026-07-04 (`GET /api/v1/auth/me` via `curl`
  holding a browser-obtained token — see the gitignored auth setup doc §10).
- The mobile half now exists: PKCE Microsoft sign-in, a session gate, and a
  round-trip screen rendering the server-resolved role, all pure TS over injected
  ports and unit-tested headless.
- `api/test/roundtrip` (`-tags integration`, real Postgres in CI) machine-asserts
  the epic's literal exit criterion — an audit-row **conservation** property: a
  returning login writes exactly one `audit_log` row and zero `users` rows; a
  first-contact login writes exactly one `users` row and two `audit_log` rows; a
  forced audit-insert failure rolls the `users` write back and fails closed as 500,
  never 200.
- `api-ci.yml`'s `acceptance` job now actually runs the `test/skeleton` and
  `test/contract` harnesses (previously wired into no workflow, on any path filter —
  an acceptance test nothing runs cannot block a merge).

**Not yet proven:** no device has completed the round trip. This is blocked on a
one-time Supabase console action (adding the mobile deep-link scheme to the
Redirect URLs allowlist) that only a human can perform, followed by an actual
sign-in + round trip on a real phone with the resulting audit row read back from
the database. **Epics 3–6 remain gated on this closing, not on the code existing.**
This section will be updated with the device smoke-run date once that happens.
