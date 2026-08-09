package attendanceimport

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMapHeadersRequiresStableImportColumns(t *testing.T) {
	if _, err := mapHeaders([]string{"roll_no", "category_id", "slot", "od_date", "capture_method"}); err != nil {
		t.Fatalf("roll_no headers should be accepted: %v", err)
	}
	if _, err := mapHeaders([]string{"student_id", "category_id", "slot"}); err == nil {
		t.Fatal("incomplete headers were accepted")
	}
	if _, err := mapHeaders([]string{"student_id", "student_id", "category_id", "slot", "od_date", "capture_method"}); err == nil {
		t.Fatal("duplicate headers were accepted")
	}
}

func TestCreateRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	service := NewService(nil, nil)
	_, err := service.Create(context.Background(), Actor{Role: "teacher"}, CreateInput{EventID: uuid.NewString(), Filename: "marks.csv", CSV: []byte("x")})
	if err == nil {
		t.Fatal("non-admin import was accepted")
	}
	_, err = service.Create(context.Background(), Actor{Role: "admin", UserID: "not-a-uuid"}, CreateInput{EventID: "not-an-id", Filename: "marks.csv", CSV: []byte("x")})
	if err == nil {
		t.Fatal("invalid import identifiers were accepted")
	}
}

func TestJobArgsKindIsStable(t *testing.T) {
	if (Args{}).Kind() != "attendance_import" {
		t.Fatal("attendance import job kind changed")
	}
}
