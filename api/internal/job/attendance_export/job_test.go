package attendanceexport

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
	exportsvc "github.com/Shruhath/OD-Amrita/api/internal/service/attendanceexport"
)

func TestWorkerRejectsIncompleteRuntime(t *testing.T) {
	worker := &Worker{}
	if err := worker.Work(context.Background(), &river.Job[exportsvc.Args]{Args: exportsvc.Args{ExportID: "not-a-uuid"}}); err == nil {
		t.Fatal("worker accepted a nil service")
	}
}
