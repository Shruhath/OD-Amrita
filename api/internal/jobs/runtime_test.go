package jobs

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
)

type testArgs struct{}

func (testArgs) Kind() string { return "test_job" }

type testWorker struct {
	river.WorkerDefaults[testArgs]
}

func (testWorker) Work(context.Context, *river.Job[testArgs]) error { return nil }

func TestNewRegistryStartsEmptyAndAcceptsTypedWorkers(t *testing.T) {
	registry := NewRegistry()
	if registry == nil || registry.Workers() == nil {
		t.Fatal("NewRegistry returned no worker registry")
	}

	river.AddWorker(registry.Workers(), &testWorker{})
}

func TestNewClientRejectsNilInputs(t *testing.T) {
	if _, err := NewClient(nil, NewRegistry()); err == nil {
		t.Fatal("NewClient accepted nil pool")
	}
	if _, err := NewClient(nil, nil); err == nil {
		t.Fatal("NewClient accepted nil pool and registry")
	}
}
