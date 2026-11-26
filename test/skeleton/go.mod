// Monorepo skeleton acceptance tests.
// Standalone stdlib-only module so it runs independent of the api/ module (the
// repo root is not a Go module). Optionally wire into go.work with
// `go work use ./test/skeleton`.
module github.com/Shruhath/OD-Amrita/test/skeleton

go 1.25.5
