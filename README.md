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
