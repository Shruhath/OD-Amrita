# OD-Amrita Developer Operations Guide (HOWTO)

This document provides step-by-step instructions for running, testing, and developing within the OD-Amrita monorepo codebase.

## Repository Overview

The project is structured as an atomic monorepo:
*   contracts/ - Contains the OpenAPI spec (openapi.yaml) and the configuration to generate both Go server types and TypeScript client schemas.
*   api/ - The Go backend service, containing two entrypoints: cmd/api (the Gin HTTP server) and cmd/worker (the persistent River background worker).
*   web/ - The React and Vite single-page application for teachers and the public portal.
*   mobile/ - The React Native (Expo) mobile application for event coordinators and card populations.
*   test/ - Test suites separated into skeleton configurations and contract validations.

---

## Workspace Setup and Dependencies

Before starting, install the required system prerequisites:
*   Go (version 1.26 or higher)
*   Node.js and pnpm (v11 or higher)
*   Docker and Docker Compose
*   OpenSSL or mkcert (for local TLS cert generation)

Initialize the Node dependencies and synchronize the Go workspace:

```bash
pnpm install
go work sync
```

---

## Step-by-Step Execution Guide

### 1. Generate API Bindings
Since this codebase uses a contract-first approach, you must generate Go and TypeScript types from the OpenAPI specification before running or building the applications:

```bash
make -C contracts generate
```

This compiles:
*   Go models to: contracts/generated/go/types.gen.go
*   TypeScript schemas to: contracts/generated/ts/schema.ts

### 2. Configure Database and Roles
OD-Amrita utilizes PostgreSQL 17 with strict role privileges:
*   od_migrator: Owns the schemas and tables. Performs DDL migrations.
*   od_runtime: Runs database queries from the application. Holds DML-only access.

Start the database container to bootstrap these roles:

```bash
cd api
make db-up
```

`make db-up` starts PostgreSQL in a container, waits for the role bootstrap in
`db/init/` to finish, and applies every migration. It defaults to `podman`; set
`CONTAINER_ENGINE=docker` if you use Docker. The image tag is pinned to the same
major version the `api-ci` integration job runs — a local database on a different
major version can pass while CI fails, which defeats the point of running it
locally.

Companion targets: `make db-down` (remove it; all data is discarded),
`make db-reset` (recreate from scratch), and `make db-psql` (a `psql` shell as
the migrator). The older `docker compose up -d db` still works, but note the two
known gaps recorded in the deferred-work log: a fresh Compose run starts the API
with TLS enabled but no certificate paths, and it does not apply migrations.

#### Browsing the database visually

```bash
cd api
make db-studio        # drizzle-gateway on http://127.0.0.1:4983
make db-studio-down   # stop it
```

Remember that **drizzle-gateway is a VIEWER**. There is no Drizzle ORM in this
project and there never will be — schema changes go exclusively through goose
migrations in `db/migrations/`. Editing rows in the browser bypasses
`store.WithAudit`, so it will silently break the audit-conservation invariant the
integration suite asserts; treat it as read-only unless you are deliberately
constructing a fixture.

`make db-studio` prints the connection values to paste into the UI. Three
non-obvious ones:

*   **There are two different passwords.** `MASTER_PASS`
    (`studio_dev_only_change_me`) unlocks the gateway UI itself. The *database*
    connection form wants `od_migrator`'s password
    (`migrator_dev_only_change_me`). Pasting the master password into the
    connection form fails with `password authentication failed for user
    "od_migrator"`.
*   **Host is `od-postgres`, not `localhost`.** The browser runs inside the
    container network, where `localhost` is its own container.
*   **Connect as `od_migrator`.** `od_runtime` is deliberately DML-only and
    cannot see everything, so browsing as it would misrepresent the schema.

Saved connections live in the `od-studio-data` volume and survive
`make db-studio-down`. Remove them with
`podman volume rm od-studio-data`.

Configure your local environment by copying the template file:

```bash
cp .env.example .env
```

Apply the database migrations using the migrator credentials:

```bash
DATABASE_URL_MIGRATOR="postgres://od_migrator:migrator_dev_only_change_me@localhost:5432/od_amrita?sslmode=disable" make migrate-up
```

### 3. Generate Local TLS Certificates
The backend enforces TLS 1.2+ only. Plain HTTP connections are rejected outright. Create self-signed certificates for local development:

```bash
mkdir -p certs
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key -out certs/server.crt -sha256 -days 365 -nodes -subj "/CN=localhost"
```

Verify your .env file targets these certificate locations:
```env
TLS_ENABLED=true
TLS_CERT_FILE=./certs/server.crt
TLS_KEY_FILE=./certs/server.key
```

### 4. Running the Backend Services
Run the API web server and the River background worker in separate terminal windows:

```bash
# In terminal A (API server)
make -C api run-api

# In terminal B (River worker)
make -C api run-worker
```

### 5. Running the Web App
To run the React web interface:

```bash
pnpm --filter od-amrita-web dev
```

### 6. Running the Mobile App
To start the React Native mobile app in the Expo development server:

```bash
pnpm --filter od-amrita-mobile start
```

---

## Common Development Tasks

### Modifying the API Spec
If you need to add or edit endpoints, do not modify the generated folders manually.
1. Update contracts/openapi.yaml.
2. Run the build generator: `make -C contracts generate`.
3. Check in both the openapi.yaml and generated files.

### Managing Database Migrations
We use goose to handle schema modifications. Do not write migrations manually without naming them through the CLI tool.
To apply migrations or roll back changes locally:
*   Upward migrations: `DATABASE_URL_MIGRATOR="..." make migrate-up`
*   Downward migrations: `DATABASE_URL_MIGRATOR="..." make migrate-down`

### Running the Test Suite
Tests ensure the monorepo structure and contract definitions remain sound.

To run structural assertions:
```bash
cd test/skeleton
go test -v ./...
```

To run contract integration tests:
```bash
cd test/contract
go test -v ./...
```
Do **not** set `GOWORK=off` here — `test/contract` is deliberately wired into `go.work` so its
`TestIntegration_ApiBuildsWithGeneratedGo` check exercises the same workspace resolution a real
`api/` build uses.

#### Real-PostgreSQL suites

The migration, role-privilege, audit-conservation and event/category suites need
a live database. They are behind the `integration` build tag and **skip silently
without one**, so a green `go test ./...` says nothing about them:

```bash
cd api
make db-up            # once per session
make test-integration # runs exactly what api-ci's integration job runs
```

`make test-integration` refuses to start unless the pinned `goose` binary is on
`PATH`, because the migration round-trip test shells out to it and would
otherwise skip without failing — a passing run that proved nothing. Install it
with:

```bash
GOWORK=off go install github.com/pressly/goose/v3/cmd/goose@v3.27.2
export PATH="$(go env GOPATH)/bin:$PATH"
```

#### Browser suites against the real stack

`web/tests/e2e/` mocks the network and needs no database. `web/tests/integration/`
mocks nothing and drives the deterministic portal API in `api/test/portal`
against real PostgreSQL:

```bash
cd api && make db-up
go build -o /tmp/od-portal-api ./test/portal
DATABASE_URL_MIGRATOR="postgres://od_migrator:migrator_dev_only_change_me@127.0.0.1:5432/od_amrita?sslmode=disable" \
DATABASE_URL_RUNTIME="postgres://od_runtime:runtime_dev_only_change_me@127.0.0.1:5432/od_amrita?sslmode=disable" \
  /tmp/od-portal-api &

pnpm --filter od-amrita-web test:e2e:integration
```

The portal harness re-seeds its fixtures on every start, so the journeys are
repeatable without recreating the database.

> **Restart the portal API after `make db-reset`.** `seed()` runs only at
> startup, so a harness left running against a recreated database serves an empty
> one. Every journey then fails on a missing `Events` heading, which looks like a
> frontend regression and is not one.

> **Restart the portal API between runs of the integration suite too.** `seed()`
> only removes the fixtures it knows about at startup, while the category journey
> *creates* its event through the UI — and `events.name` carries no unique
> constraint. Running the suite twice against one long-lived harness therefore
> leaves two events named `Browser Category Event`. The specs pick the newest, so
> they still pass, but the audit-conservation counts are then per-event and a
> stale duplicate will confuse anything that counts by name.
