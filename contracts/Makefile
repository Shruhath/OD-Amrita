# contracts/Makefile — codegen entrypoint for the OpenAPI SSOT.
#
# `make generate` regenerates the Go + TS types from openapi.yaml. Everything
# under generated/** is DO NOT EDIT (regenerated from the spec). The drift-gate
# is `make generate && git diff --exit-code`, enforced in CI by
# .github/workflows/contract-drift.yml.

# Pinned generator versions — NEVER @latest (non-reproducible output would make
# the drift-gate flake). Bump deliberately and regenerate.
OAPI_CODEGEN_VERSION := v2.7.1

# Repo root (contracts/ is one directory down). Used to reach the root-installed,
# pnpm-lock-pinned openapi-typescript binary.
REPO_ROOT := $(abspath $(CURDIR)/..)
OPENAPI_TS := $(REPO_ROOT)/node_modules/.bin/openapi-typescript

.PHONY: generate generate-go generate-ts clean

generate: generate-go generate-ts ## Regenerate Go + TS types from openapi.yaml

generate-go: ## Generate Go models via oapi-codegen (pinned) → generated/go/types.gen.go
	GOWORK=off go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) -config oapi-codegen.yaml openapi.yaml

generate-ts: ## Generate TS types via openapi-typescript (pinned in root package.json) → generated/ts/schema.ts
	$(OPENAPI_TS) openapi.yaml -o generated/ts/schema.ts
	@{ printf '// DO NOT EDIT — generated from contracts/openapi.yaml by `make -C contracts generate`.\n// Edit the spec, not this file. Re-exported via @od-amrita/contracts (workspace:*).\n\n'; cat generated/ts/schema.ts; } > generated/ts/schema.ts.tmp
	@mv generated/ts/schema.ts.tmp generated/ts/schema.ts

clean: ## Remove generated output
	rm -f generated/go/types.gen.go generated/ts/schema.ts
