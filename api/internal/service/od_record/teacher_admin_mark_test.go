package odrecord

import (
	"context"
	"strings"
	"testing"
)

func requireTeacherAdminMarkATDD(t *testing.T) {
	t.Helper()
}

// branchScopingGuard is the StudentScopeGuard fake for these tests. AllowAnyScope
// cannot exercise branch refusal — it admits every scope unconditionally, which
// is exactly why it is the wrong guard for a test that asserts a REFUSAL. This
// fake mirrors RoleAwareMarkGuard's shape (allow-unless-refused) using the
// actor's email as the discriminator the two test cases already vary on.
type branchScopingGuard struct{}

func (branchScopingGuard) AssertCanMark(context.Context, Actor, string, string) error { return nil }

func (branchScopingGuard) AssertCanMarkStudent(_ context.Context, actor Actor, _, _ string, _ Student) error {
	if actor.Role == "teacher" && strings.Contains(actor.Email, "outside-branch") {
		return forbidden("Teacher may mark only students in a taught branch for an active event branch.")
	}
	return nil
}

func TestTeacherAdminMark_TeacherSearchMarkRequiresBranchAuthorization(t *testing.T) {
	requireTeacherAdminMarkATDD(t)

	writer := &countingWriter{}
	credits := stubCreditLedger()
	studentID := scaffoldStudentID
	slot := 4
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, branchScopingGuard{}, credits)

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
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, branchScopingGuard{}, stubCreditLedger())

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
