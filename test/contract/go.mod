// Acceptance harness for the OpenAPI contract, codegen & drift-gate.
//
// Standalone module (like test/skeleton) so it runs independent of the api/
// module and is NOT wired into go.work. It has ONE non-stdlib dependency —
// gopkg.in/yaml.v3 — used to assert the *shape* of contracts/openapi.yaml
// semantically (version, paths, schemas, enums) rather than by brittle string
// matching.
//
// Because it is NOT in go.work, run it with GOWORK=off:
//   cd test/contract && go mod tidy && GOWORK=off go test -v ./...
module github.com/Shruhath/OD-Amrita/test/contract

go 1.26

require gopkg.in/yaml.v3 v3.0.1
