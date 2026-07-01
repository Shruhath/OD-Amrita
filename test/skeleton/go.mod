// ATDD skeleton acceptance harness for Story 1.1 (monorepo skeleton).
// Standalone stdlib-only module so it runs before/independent of the api/ module,
// and survives the root-Go-module reconciliation (root stops being a Go module).
// Wire into go.work during the green phase if desired: `go work use ./test/skeleton`.
module github.com/Shruhath/OD-Amrita/test/skeleton

go 1.25.5
