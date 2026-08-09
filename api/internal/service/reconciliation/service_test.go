package reconciliation

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRunRejectsInvalidInputsBeforeDatabaseAccess(t *testing.T) {
	service := NewService(nil)
	cases := []struct {
		name     string
		eventID  uuid.UUID
		revision int
	}{
		{name: "nil pool", eventID: uuid.New(), revision: 1},
		{name: "empty event", eventID: uuid.Nil, revision: 1},
		{name: "zero revision", eventID: uuid.New(), revision: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Run(context.Background(), tc.eventID, tc.revision); err == nil {
				t.Fatal("Run accepted invalid input")
			}
		})
	}
}

func TestDueEventsRejectsNilPool(t *testing.T) {
	if _, err := NewService(nil).DueEvents(context.Background()); err == nil {
		t.Fatal("DueEvents accepted a nil database pool")
	}
}
