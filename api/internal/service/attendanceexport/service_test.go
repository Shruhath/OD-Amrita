package attendanceexport

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCreateRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	service := NewService(nil, nil)
	if _, err := service.Create(context.Background(), Actor{Role: "teacher"}, uuid.NewString()); err == nil {
		t.Fatal("non-admin export was accepted")
	}
	if _, err := service.Create(context.Background(), Actor{Role: "admin", UserID: "not-a-uuid"}, "not-an-id"); err == nil {
		t.Fatal("invalid export identifiers were accepted")
	}
}

func TestParseIDsRejectsMalformedIDs(t *testing.T) {
	if _, _, err := parseIDs("not-an-event", uuid.NewString()); err == nil {
		t.Fatal("malformed event id was accepted")
	}
	if _, _, err := parseIDs(uuid.NewString(), "not-an-export"); err == nil {
		t.Fatal("malformed export id was accepted")
	}
}

func TestArgsKindIsStable(t *testing.T) {
	if (Args{}).Kind() != "attendance_export" {
		t.Fatal("attendance export job kind changed")
	}
}
