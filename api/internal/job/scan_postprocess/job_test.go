package scanpostprocess

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
)

func TestArgsKindIsStable(t *testing.T) {
	if got, want := (Args{}).Kind(), "scan_post_processing"; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
}

func TestWorkerRejectsIncompleteRuntime(t *testing.T) {
	var worker Worker
	if err := worker.Work(context.Background(), &river.Job[Args]{Args: Args{ScanID: "not-a-uuid"}}); err == nil {
		t.Fatal("nil pool should be rejected")
	}
}
