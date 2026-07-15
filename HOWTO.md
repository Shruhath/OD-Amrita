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
docker compose up -d db
```

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

To run contract integration tests (requires turning off the Go workspace to prevent module errors):
```bash
cd test/contract
GOWORK=off go test -v ./...
```
