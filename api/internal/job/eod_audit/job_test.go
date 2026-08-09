package eodaudit

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
)

func TestArgsKindsAreStable(t *testing.T) {
	if (Args{}).Kind() != "eod_reconciliation" {
		t.Fatal("reconciliation job kind changed")
	}
	if (SweepArgs{}).Kind() != "eod_reconciliation_sweep" {
		t.Fatal("reconciliation sweep job kind changed")
	}
}

func TestWorkerRejectsIncompleteRuntime(t *testing.T) {
	worker := &Worker{}
	if err := worker.Work(context.Background(), &river.Job[Args]{Args: Args{EventID: "not-a-uuid", Revision: 1}}); err == nil {
		t.Fatal("worker accepted a nil service")
	}

	sweep := &SweepWorker{}
	if err := sweep.Work(context.Background(), &river.Job[SweepArgs]{}); err == nil {
		t.Fatal("sweep worker accepted incomplete runtime")
	}
}

func TestPeriodicSweepIsConfigured(t *testing.T) {
	if PeriodicSweep() == nil {
		t.Fatal("PeriodicSweep returned nil")
	}
}
