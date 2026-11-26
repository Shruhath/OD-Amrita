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

# Local integration database. Pinned to the SAME image tag the api-ci
# integration job uses — a local Postgres on a different major version can go
# green while CI goes red, which defeats the purpose of running it locally.
# Keep POSTGRES_IMAGE in sync with .github/workflows/api-ci.yml.
POSTGRES_IMAGE   := docker.io/library/postgres:17
DB_CONTAINER     ?= od-postgres
CONTAINER_ENGINE ?= podman
# A user-defined network so the DB browser can resolve the database by container
# name. The database ALSO publishes its port to the host, because the Go test
# suites run on the host, not in a container.
DB_NETWORK       := od-amrita

# drizzle-gateway — a visual DB browser (engineering-overview.md pins :4983).
# This is a VIEWER only: there is no Drizzle ORM anywhere in this project, and
# schema changes still go exclusively through goose migrations.
# Pinned by DIGEST, like every other tool version in this repo (postgres:17,
# goose v3.27.2, sqlc v1.31.1). `:latest` is the one moving target the toolchain
# had left: a viewer that silently changes under you is a debugging trap, and
# upstream publishes no stable semver tag for this image (only :latest), so the
# digest is the only thing that can be pinned. Resolved from :latest at
# 1.3.14-distroless; re-resolve deliberately when you want a newer viewer.
STUDIO_IMAGE       := ghcr.io/drizzle-team/gateway@sha256:8bb6c6361745cf97f2564676783f948487d2f05ba60d3612b7f14b8ebc18ab19
STUDIO_CONTAINER   := od-studio
STUDIO_PORT        ?= 4983
# Dev-only, same posture as the role passwords below. Never reuse it anywhere real.
STUDIO_MASTER_PASS ?= studio_dev_only_change_me

# Dev-only credentials. These are the same values already committed in
# db/init/00_roles.sql and the CI workflow; they must never appear in a real
# environment. Override any of them from the environment if a port clashes.
DB_HOST ?= 127.0.0.1
DB_PORT ?= 5432
SUPERUSER_DSN         ?= postgres://od_amrita:ci_only_password@$(DB_HOST):$(DB_PORT)/od_amrita?sslmode=disable
DATABASE_URL_MIGRATOR ?= postgres://od_migrator:migrator_dev_only_change_me@$(DB_HOST):$(DB_PORT)/od_amrita?sslmode=disable
DATABASE_URL_RUNTIME  ?= postgres://od_runtime:runtime_dev_only_change_me@$(DB_HOST):$(DB_PORT)/od_amrita?sslmode=disable

# Local Kafka for development, as a single-node Redpanda. Redpanda rather than
# Kafka+ZooKeeper/KRaft because it is ONE container with no coordinator to keep
# alive, speaks the same protocol franz-go talks to a real cluster, and starts in
# a couple of seconds — the local broker exists so a developer can watch events
# flow, and anything heavier than that would simply not get run.
#
# Pinned by tag for the same reason every other tool here is: a broker that
# changes under you is a debugging trap.
KAFKA_IMAGE     := docker.io/redpandadata/redpanda:v24.2.18
KAFKA_CONTAINER ?= od-redpanda
# 19092 rather than 9092 on the host, so this cannot collide with any other
# broker a developer happens to be running.
KAFKA_PORT      ?= 19092
KAFKA_ADMIN_PORT ?= 19644

# The integration packages share one database and are serialized with -p 1, the
# same way api-ci runs them.
INTEGRATION_PKGS := ./test/db/... ./test/store/... ./test/event/... ./test/roundtrip/... ./test/teacher/... ./test/student/... ./test/cardtoken/... ./test/odrecord/...

.PHONY: build test lint sqlc sqlc-check migrate-up migrate-down run-api run-worker \
        db-up db-down db-reset db-psql db-studio db-studio-down test-integration \
        kafka-up kafka-down kafka-topics kafka-tail test-stream ci-local

build: ## Compile all packages
	go build ./...

test: ## Run Go tests
	go test ./...

lint: ## gofmt + golangci-lint (golangci-lint added in CI)
	gofmt -l .

sqlc: ## Generate typed Go into internal/store/sqlc from db/queries (pinned)
	$(SQLC) generate

ci-local: ## Run the api-ci workflow locally with act (Actions is billing-blocked)
	@./scripts/ci_local.sh $(JOBS)

sqlc-check: ## CI compile-check: regenerate and fail if committed output is stale
	$(SQLC) generate
	git diff --exit-code -- internal/store/sqlc

migrate-up: ## Apply goose migrations as od_migrator (needs DATABASE_URL_MIGRATOR)
	@test -n "$(DATABASE_URL_MIGRATOR)" || { echo "DATABASE_URL_MIGRATOR is not set"; exit 1; }
	$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL_MIGRATOR)" up

migrate-down: ## Roll back goose migrations as od_migrator (needs DATABASE_URL_MIGRATOR)
	@test -n "$(DATABASE_URL_MIGRATOR)" || { echo "DATABASE_URL_MIGRATOR is not set"; exit 1; }
	$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL_MIGRATOR)" down

db-up: ## Start the local integration Postgres (CI-matching image) and migrate it
	@$(CONTAINER_ENGINE) network inspect $(DB_NETWORK) >/dev/null 2>&1 \
		|| $(CONTAINER_ENGINE) network create $(DB_NETWORK) >/dev/null
	@$(CONTAINER_ENGINE) rm -f $(DB_CONTAINER) >/dev/null 2>&1 || true
	@$(CONTAINER_ENGINE) run -d --name $(DB_CONTAINER) \
		--network $(DB_NETWORK) \
		-e POSTGRES_DB=od_amrita \
		-e POSTGRES_USER=od_amrita \
		-e POSTGRES_PASSWORD=ci_only_password \
		-p $(DB_HOST):$(DB_PORT):5432 \
		-v "$(CURDIR)/db/init:/docker-entrypoint-initdb.d:ro" \
		$(POSTGRES_IMAGE) >/dev/null
	@# The entrypoint runs a TEMPORARY server to execute /docker-entrypoint-initdb.d,
	@# then shuts it down and starts the real one. Probing for connectivity alone
	@# races that restart, so wait for the init banner first.
	@printf 'waiting for postgres init'
	@for i in $$(seq 1 60); do \
		if $(CONTAINER_ENGINE) logs $(DB_CONTAINER) 2>&1 | grep -q 'init process complete'; then break; fi; \
		printf '.'; sleep 1; \
	done
	@ready=0; for i in $$(seq 1 60); do \
		if $(CONTAINER_ENGINE) exec $(DB_CONTAINER) pg_isready -U od_amrita -d od_amrita >/dev/null 2>&1; then ready=1; break; fi; \
		printf '.'; sleep 1; \
	done; \
	if [ "$$ready" = "1" ]; then echo ' ready'; else \
		echo ''; echo 'ERROR: postgres did not become ready in 60s. Recent logs:' >&2; \
		$(CONTAINER_ENGINE) logs --tail 20 $(DB_CONTAINER) >&2; exit 1; fi
	@$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL_MIGRATOR)" up

db-down: ## Stop and remove the local integration Postgres (data is discarded)
	@$(CONTAINER_ENGINE) rm -f $(DB_CONTAINER) >/dev/null 2>&1 || true
	@echo "removed $(DB_CONTAINER)"

db-reset: db-down db-up ## Recreate the local integration Postgres from scratch

db-psql: ## Open a psql shell on the local integration database as the migrator
	@# Connects over the CONTAINER's own loopback on 5432, not $(DATABASE_URL_MIGRATOR).
	@# That DSN is a HOST-side address: with DB_PORT overridden (say 5433, which the
	@# variables above explicitly invite when a port clashes) it publishes to
	@# 127.0.0.1:5433 on the host while the server inside still listens on 5432, so
	@# handing the host DSN to a psql running inside the container is refused.
	@$(CONTAINER_ENGINE) exec -it $(DB_CONTAINER) \
		psql "postgres://od_migrator:migrator_dev_only_change_me@127.0.0.1:5432/od_amrita?sslmode=disable"

db-studio: ## Browse the local database in drizzle-gateway (a VIEWER — never an ORM)
	@$(CONTAINER_ENGINE) network inspect $(DB_NETWORK) >/dev/null 2>&1 \
		|| $(CONTAINER_ENGINE) network create $(DB_NETWORK) >/dev/null
	@$(CONTAINER_ENGINE) rm -f $(STUDIO_CONTAINER) >/dev/null 2>&1 || true
	@$(CONTAINER_ENGINE) run -d --name $(STUDIO_CONTAINER) \
		--network $(DB_NETWORK) \
		-p $(DB_HOST):$(STUDIO_PORT):4983 \
		-e PORT=4983 \
		-e STORE_PATH=/data \
		-e MASTER_PASS=$(STUDIO_MASTER_PASS) \
		-v od-studio-data:/data \
		$(STUDIO_IMAGE) >/dev/null
	@printf 'waiting for drizzle-gateway'
	@ready=0; for i in $$(seq 1 40); do \
		if curl -sf -o /dev/null --max-time 2 http://$(DB_HOST):$(STUDIO_PORT)/; then ready=1; break; fi; \
		printf '.'; sleep 1; \
	done; \
	if [ "$$ready" = "1" ]; then echo ' ready'; else \
		echo ''; echo 'ERROR: drizzle-gateway did not answer on $(STUDIO_PORT) in 40s.' >&2; \
		$(CONTAINER_ENGINE) logs --tail 20 $(STUDIO_CONTAINER) >&2; exit 1; fi
	@echo ''
	@echo '  ┌─ STEP 1 ── log in to the gateway itself ─────────────────────────┐'
	@echo '     open              http://$(DB_HOST):$(STUDIO_PORT)'
	@echo '     master password   $(STUDIO_MASTER_PASS)'
	@echo '  └──────────────────────────────────────────────────────────────────┘'
	@echo ''
	@echo '  ┌─ STEP 2 ── add a PostgreSQL connection ──────────────────────────┐'
	@echo '     Host/Port   $(DB_CONTAINER)   /   5432'
	@echo '     User        od_migrator'
	@echo '     Password    migrator_dev_only_change_me'
	@echo '     Database    od_amrita'
	@echo '     SSL mode    disable'
	@echo '  └──────────────────────────────────────────────────────────────────┘'
	@echo ''
	@echo '  The two passwords above are DIFFERENT and are the easiest thing to'
	@echo '  get wrong. STEP 1 unlocks the gateway UI; STEP 2 is the database'
	@echo '  credential. Pasting the master password into the connection form'
	@echo '  fails with: password authentication failed for user "od_migrator".'
	@echo ''
	@echo '  Host is the CONTAINER NAME, not localhost — the gateway runs inside'
	@echo '  the container network, where localhost is its own container.'
	@echo '  Connect as od_migrator, not od_runtime: the runtime role is'
	@echo '  deliberately DML-only and would misrepresent the schema.'
	@echo ''
	@echo '  READ-ONLY. Edits here bypass store.WithAudit and break the'
	@echo '  audit-conservation invariant the integration suite asserts.'
	@echo ''

db-studio-down: ## Stop and remove the drizzle-gateway browser
	@$(CONTAINER_ENGINE) rm -f $(STUDIO_CONTAINER) >/dev/null 2>&1 || true
	@echo "removed $(STUDIO_CONTAINER)"

test-integration: ## Run the real-Postgres suites exactly as api-ci does (needs db-up)
	@command -v goose >/dev/null 2>&1 || { \
		echo "goose is not on PATH — the migration round-trip test would SKIP silently."; \
		echo "install it with: GOWORK=off go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)"; \
		echo "then ensure \$$(go env GOPATH)/bin is on PATH."; \
		exit 1; \
	}
	SUPERUSER_DSN="$(SUPERUSER_DSN)" \
	DATABASE_URL_MIGRATOR="$(DATABASE_URL_MIGRATOR)" \
	DATABASE_URL_RUNTIME="$(DATABASE_URL_RUNTIME)" \
	go test -count=1 -tags integration -p 1 $(INTEGRATION_PKGS)

run-api: ## Run the HTTP server entrypoint
	go run ./cmd/api

run-worker: ## Run the persistent River worker entrypoint
	go run ./cmd/worker

# ── Local event stream (Kafka / Redpanda) ────────────────────────────────────
#
# NONE of this is needed to run the API. With KAFKA_BROKERS unset the stream is
# off and every publish is a no-op, which is how production runs today. These
# targets exist for developing against it.

kafka-up: ## Start the local single-node Redpanda broker
	@if [ -n "$$($(CONTAINER_ENGINE) ps -aq --filter name=^$(KAFKA_CONTAINER)$$)" ]; then \
		$(CONTAINER_ENGINE) start $(KAFKA_CONTAINER) >/dev/null; \
	else \
		$(CONTAINER_ENGINE) run -d --name $(KAFKA_CONTAINER) \
			-p 127.0.0.1:$(KAFKA_PORT):19092 -p 127.0.0.1:$(KAFKA_ADMIN_PORT):9644 \
			$(KAFKA_IMAGE) redpanda start \
			--overprovisioned --smp 1 --memory 1G --reserve-memory 0M \
			--node-id 0 --check=false \
			--kafka-addr internal://0.0.0.0:9092,external://0.0.0.0:19092 \
			--advertise-kafka-addr internal://$(KAFKA_CONTAINER):9092,external://127.0.0.1:$(KAFKA_PORT) >/dev/null; \
	fi
	@echo "waiting for the broker..."
	@for i in $$(seq 1 60); do \
		if $(CONTAINER_ENGINE) exec $(KAFKA_CONTAINER) rpk cluster info >/dev/null 2>&1; then \
			echo "broker ready on 127.0.0.1:$(KAFKA_PORT)"; exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "broker did not become ready" >&2; exit 1

kafka-down: ## Stop and remove the local broker (all messages are discarded)
	-$(CONTAINER_ENGINE) rm -f $(KAFKA_CONTAINER)

kafka-topics: ## Create the topics with their intended retention (idempotent)
	KAFKA_BROKERS=127.0.0.1:$(KAFKA_PORT) KAFKA_TLS=false \
		go run ./cmd/streamctl -provision

kafka-tail: ## Follow the event stream in this terminal
	KAFKA_BROKERS=127.0.0.1:$(KAFKA_PORT) KAFKA_TLS=false \
		go run ./cmd/streamctl -tail

test-stream: ## Run the broker-backed round trip (needs kafka-up)
	KAFKA_BROKERS=127.0.0.1:$(KAFKA_PORT) KAFKA_TLS=false \
		go test -count=1 -tags stream ./test/stream/...
