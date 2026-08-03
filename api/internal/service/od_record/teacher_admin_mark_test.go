package odrecord

import (
	"context"
	"os"
	"testing"
)

func requireTeacherAdminMarkATDD(t *testing.T) {
	t.Helper()
	if os.Getenv("ATDD_ACTIVE_09") != "1" {
		t.Skip("red-phase scaffold for 0.9; set ATDD_ACTIVE_09=1 while implementing")
	}
}

func TestTeacherAdminMark_TeacherSearchMarkRequiresBranchAuthorization(t *testing.T) {
	requireTeacherAdminMarkATDD(t)

	writer := &countingWriter{}
	credits := stubCreditLedger()
	studentID := scaffoldStudentID
	slot := 4
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, credits)

	_, err := svc.Mark(context.Background(), Actor{
		UserID: scaffoldActorID,
		Email:  "teacher@amrita.edu",
		Role:   "teacher",
	}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodSearch,
		StudentID:     &studentID,
		Slot:          &slot,
	})

	if err != nil {
		t.Fatalf("[P0] teacher search mark = %v, want nil when branch/event/student scope matches", err)
	}
	if writer.inserts != 1 {
		t.Fatalf("[P0] teacher search mark wrote %d rows, want exactly 1", writer.inserts)
	}
	if credits.debits != 0 {
		t.Fatalf("[P0] teacher search mark debited fallback credits %d times; only coordinator camera/search spends credits", credits.debits)
	}
}

func TestTeacherAdminMark_RejectsTeacherOutsideStudentBranchBeforeWrite(t *testing.T) {
	requireTeacherAdminMarkATDD(t)

	writer := &countingWriter{}
	studentID := scaffoldStudentID
	slot := 4
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, stubCreditLedger())

	_, err := svc.Mark(context.Background(), Actor{
		UserID: scaffoldActorID,
		Email:  "teacher-outside-branch@amrita.edu",
		Role:   "teacher",
	}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodSearch,
		StudentID:     &studentID,
		Slot:          &slot,
	})

	if err == nil {
		t.Fatal("[P0] teacher outside the student's branch was accepted; want RBAC_FORBIDDEN and no write")
	}
	if writer.inserts != 0 {
		t.Fatalf("[P0] unauthorized teacher branch mismatch wrote %d rows, want 0", writer.inserts)
	}
}

func TestTeacherAdminMark_PreservesCoordinatorCaptureRules(t *testing.T) {
	requireTeacherAdminMarkATDD(t)

	studentID := scaffoldStudentID
	slot := 3
	svc := NewService(stubReader(), stubCardResolver(), &countingWriter{}, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, stubCreditLedger())

	_, err := svc.Mark(context.Background(), Actor{
		UserID: scaffoldActorID,
		Email:  "coordinator@amrita.edu",
		Role:   "coordinator",
	}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodSearch,
		StudentID:     &studentID,
		Slot:          &slot,
	})

	if err == nil {
		t.Fatal("[P0] coordinator search mark with explicit slot was accepted; /mark must keep server-derived slot semantics")
	}
}
