// Acceptance harness for the OpenAPI contract, codegen & drift-gate.
//
// Its own module (like test/skeleton) so its one non-stdlib dependency —
// gopkg.in/yaml.v3, used to assert the *shape* of contracts/openapi.yaml
// semantically (version, paths, schemas, enums) rather than by brittle string
// matching — stays out of the api/ module's dependency graph.
//
// It IS in go.work, and must be. TestIntegration_ApiBuildsWithGeneratedGo shells
// out to `go build ./...` inside api/, and api/ resolves
// contracts/generated/go through the workspace. Running this harness with
// GOWORK=off passes that variable down to the subprocess, so api/ can no longer
// find the generated module and the build fails — the harness cannot verify the
// contract wiring by severing it. Run it plainly:
//   cd test/contract && go test ./...
module github.com/Shruhath/OD-Amrita/test/contract

go 1.26

require gopkg.in/yaml.v3 v3.0.1

require (
	github.com/kr/pretty v0.3.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)
