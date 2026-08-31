package main

// observability_test.go — the composition guard for request correlation.
//
// requestid.Middleware() must be the FIRST middleware on the router. Registered
// anywhere after gin.Recovery() or errormw.Handler(), it has not run by the time
// either of those logs, so the two requests that most need an id — the one that
// panicked and the one that failed — are the two that would not have one.
//
// That is a one-line ordering slip in main.go: it compiles, every route test
// passes, every request still succeeds, and the only symptom is an empty
// request_id="" in a log line nobody reads until an incident. Checked against the
// source for the same reason routes_test.go and event_stream_test.go are.

import (
	"os"
	"strings"
	"testing"
)

func mainSource(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	return string(source)
}

func TestRequestIDMiddlewareRunsBeforeRecoveryAndTheErrorRenderer(t *testing.T) {
	source := mainSource(t)

	id := strings.Index(source, "router.Use(requestid.Middleware())")
	recovery := strings.Index(source, "router.Use(gin.Recovery())")
	errors := strings.Index(source, "router.Use(errormw.Handler())")

	if id < 0 {
		t.Fatal("main.go does not register requestid.Middleware(); every 500 is anonymous again")
	}
	if recovery < 0 || errors < 0 {
		t.Fatal("main.go no longer registers gin.Recovery() and errormw.Handler() in the expected form")
	}
	if id > recovery {
		t.Error("requestid.Middleware() is registered AFTER gin.Recovery(); a panicking request will log no id")
	}
	if id > errors {
		t.Error("requestid.Middleware() is registered AFTER errormw.Handler(); a failing request will log no id")
	}
}

// The renderer must log through the middleware, not by reading Fly's header
// directly. Reading the header works today and produces an empty string the day
// this runs anywhere but Fly — and it would not match the X-Request-Id the same
// response hands the user.
func TestErrorRendererLogsTheAssignedIDNotTheEdgeHeader(t *testing.T) {
	source, err := os.ReadFile("../../internal/middleware/error/error.go")
	if err != nil {
		t.Fatalf("read error.go: %v", err)
	}
	if !strings.Contains(string(source), "requestid.FromGin(c)") {
		t.Error("the error renderer does not log requestid.FromGin(c); a 500's log line will not match the id the caller was given")
	}
	if strings.Contains(string(source), `c.GetHeader("Fly-Request-Id")`) {
		t.Error("the error renderer reads Fly's header directly; that is the edge's id, empty off Fly, and not what the response echoed")
	}
}
