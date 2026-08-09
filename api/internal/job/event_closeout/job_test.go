package eventcloseout

import (
	"context"
	"testing"

	closeoutsvc "github.com/Shruhath/OD-Amrita/api/internal/service/eventcloseout"
	"github.com/riverqueue/river"
)

func TestWorkerRejectsIncompleteRuntime(t *testing.T) {
	var worker Worker
	if err := worker.Work(context.Background(), &river.Job[closeoutsvc.Args]{}); err == nil {
		t.Fatal("nil service should be rejected")
	}
}
