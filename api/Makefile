# api/Makefile — backend build & codegen entrypoints.

# Pinned goose version — NEVER @latest (CI reproducibility). Bump deliberately.
GOOSE_VERSION := v3.27.2
# `go run pkg@ver` is unreliable under an active go.work — force it off,
# matching the contracts/Makefile codegen pattern.
GOOSE := GOWORK=off go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

.PHONY: build test lint sqlc migrate-up migrate-down run-api run-worker

build: ## Compile all packages
	go build ./...

test: ## Run Go tests
	go test ./...

lint: ## gofmt + golangci-lint (golangci-lint added in CI)
	gofmt -l .

sqlc: ## Generate typed Go from db/queries (no-op until queries exist)
	@command -v sqlc >/dev/null 2>&1 && sqlc generate || echo "sqlc: skipped (not installed / no queries yet)"

migrate-up: ## Apply goose migrations as od_migrator (needs DATABASE_URL_MIGRATOR)
	@test -n "$(DATABASE_URL_MIGRATOR)" || { echo "DATABASE_URL_MIGRATOR is not set"; exit 1; }
	$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL_MIGRATOR)" up

migrate-down: ## Roll back goose migrations as od_migrator (needs DATABASE_URL_MIGRATOR)
	@test -n "$(DATABASE_URL_MIGRATOR)" || { echo "DATABASE_URL_MIGRATOR is not set"; exit 1; }
	$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL_MIGRATOR)" down

run-api: ## Run the HTTP server entrypoint
	go run ./cmd/api

run-worker: ## Run the persistent River worker entrypoint
	go run ./cmd/worker
