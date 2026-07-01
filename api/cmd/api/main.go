// Command api is the OD-Amrita HTTP server entrypoint (gin). One of two api/
// entrypoints; cmd/worker runs the persistent River worker.
//
// Transport security (NFR15): the server is deployed behind TLS 1.2+ ONLY.
// Plain HTTP is REJECTED, not redirected — there is deliberately NO HTTP→HTTPS
// redirect listener: no plain-port listener and no 301 redirect handler. Do not
// add one. The enforcing bootstrap (TLS config / handler wiring) lands with the
// first HTTP handler in Story 1.2/1.3; this skeleton only reserves the
// entrypoint and documents the constraint.
package main

import "fmt"

func main() {
	// Skeleton no-op: the gin engine, middleware chain, and TLS-only bootstrap
	// are wired in Story 1.2/1.3. Kept minimal so `go build ./...` succeeds.
	fmt.Println("od-amrita api: skeleton entrypoint (TLS-only, no HTTP fallback — NFR15)")
}
