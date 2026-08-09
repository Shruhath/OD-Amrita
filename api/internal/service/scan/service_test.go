package scan

import (
	"context"
	"testing"

	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

type markerMustNotRun struct{}

func (markerMustNotRun) Mark(context.Context, Actor, odsvc.MarkInput) (odsvc.MarkResult, error) {
	panic("marker called for invalid scan id")
}

func TestSubmitRejectsNonUUIDv7BeforeDatabaseWork(t *testing.T) {
	svc := NewService(markerMustNotRun{}, nil, nil)
	outcome, err := svc.Submit(context.Background(), Actor{UserID: "not-used"}, Item{ID: "550e8400-e29b-41d4-a716-446655440000"})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if outcome.Status != "rejected" || outcome.Reason != "INVALID_SCAN_ID" {
		t.Fatalf("outcome = %+v, want INVALID_SCAN_ID rejection", outcome)
	}
}
