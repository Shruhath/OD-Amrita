# contracts/Makefile — codegen entrypoint for the OpenAPI SSOT.
#
# `make generate` regenerates the Go + TS clients from openapi.yaml. Real codegen
# (oapi-codegen for Go, openapi-typescript / orval for TS) is wired in Story 1.2;
# this is a green no-op stub so the contract-drift CI gate runs on the skeleton.
#
# The drift-gate is: `make generate && git diff --exit-code` — regenerated output
# under generated/** is DO NOT EDIT.

.PHONY: generate generate-go generate-ts clean

generate: generate-go generate-ts ## Regenerate Go + TS clients from openapi.yaml
	@echo "contracts: generate (stub) — real codegen lands in Story 1.2"

generate-go: ## Generate the Go client (stub)
	@echo "contracts: generate-go (stub)"

generate-ts: ## Generate the TS client (stub)
	@echo "contracts: generate-ts (stub)"

clean: ## Remove generated output (stub)
	@echo "contracts: clean (stub)"
