// Command worker is the OD-Amrita persistent background worker entrypoint. It
// runs the River job queue (sync-apply, end-of-day audit, etc.) as an always-on
// process — River needs a persistent worker, so the deploy target is a
// long-running container, NOT serverless.
package main

import "fmt"

func main() {
	// Skeleton no-op: River client + job registration land in Epic 3+. Kept
	// minimal so `go build ./...` succeeds.
	fmt.Println("od-amrita worker: skeleton entrypoint (River worker — persistent container)")
}
