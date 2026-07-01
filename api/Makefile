# api/Makefile — backend build & codegen entrypoints.
# Most targets are stubs on the skeleton; real bodies land as each surface gains
# content (sqlc: Story 1.3; migrations: Story 1.3; lint wired in api-ci.yml).

.PHONY: build test lint sqlc migrate-up migrate-down run-api run-worker

build: ## Compile all packages
	go build ./...

test: ## Run Go tests
	go test ./...

lint: ## gofmt + golangci-lint (golangci-lint added in CI)
	gofmt -l .

sqlc: ## Generate typed Go from db/queries (no-op until Story 1.3 adds queries)
	@command -v sqlc >/dev/null 2>&1 && sqlc generate || echo "sqlc: skipped (not installed / no queries yet)"

migrate-up: ## Apply goose migrations (no-op until Story 1.3)
	@echo "goose migrate up — wired in Story 1.3"

migrate-down: ## Roll back goose migrations (no-op until Story 1.3)
	@echo "goose migrate down — wired in Story 1.3"

run-api: ## Run the HTTP server entrypoint
	go run ./cmd/api

run-worker: ## Run the persistent River worker entrypoint
	go run ./cmd/worker
