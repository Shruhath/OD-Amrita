# api/Makefile — backend build & codegen entrypoints.

# Pinned goose version — NEVER @latest (CI reproducibility). Bump deliberately.
GOOSE_VERSION := v3.27.2
# `go run pkg@ver` is unreliable under an active go.work — force it off,
# matching the contracts/Makefile codegen pattern.
GOOSE := GOWORK=off go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

# Pinned sqlc version — NEVER @latest (CI reproducibility). Bump deliberately;
# keep in sync with the sqlc-check step in .github/workflows/api-ci.yml.
SQLC_VERSION := v1.31.1
SQLC := GOWORK=off go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

.PHONY: build test lint sqlc sqlc-check migrate-up migrate-down run-api run-worker

build: ## Compile all packages
	go build ./...

test: ## Run Go tests
	go test ./...

lint: ## gofmt + golangci-lint (golangci-lint added in CI)
	gofmt -l .

sqlc: ## Generate typed Go into internal/store/sqlc from db/queries (pinned)
	$(SQLC) generate

sqlc-check: ## CI compile-check: regenerate and fail if committed output is stale
	$(SQLC) generate
	git diff --exit-code -- internal/store/sqlc

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
