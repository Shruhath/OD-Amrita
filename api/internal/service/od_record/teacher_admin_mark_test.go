package odrecord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
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
	reason := "manual attendance correction"
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
		ReasonNote:    &reason,
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

func TestTeacherAdminMark_UsesExplicitODDateInsideEventRange(t *testing.T) {
	writer := &countingWriter{}
	studentID := scaffoldStudentID
	slot := 4
	reason := "manual attendance correction"
	chosenDay := campusTime(t, 2026, time.August, 11, 0, 0)
	svc := NewService(
		scaffoldReader{
			eventDate: campusTime(t, 2026, time.August, 10, 0, 0),
			endDate:   campusTime(t, 2026, time.August, 12, 0, 0),
		},
		stubCardResolver(),
		writer,
		stubAuditStore(),
		scaffoldClock{},
		branchScopingGuard{},
		stubCreditLedger(),
	)

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
		ODDate:        &chosenDay,
		ReasonNote:    &reason,
	})

	if err != nil {
		t.Fatalf("teacher mark with explicit od_date = %v, want nil", err)
	}
	if got := writer.lastRecord.ODDate.Format("2006-01-02"); got != "2026-08-11" {
		t.Fatalf("written od_date = %s, want teacher-selected 2026-08-11", got)
	}
}

func TestAdminLiveMark_UsesServerDerivedSlotWithoutReason(t *testing.T) {
	writer := &countingWriter{}
	guard := &recordingGuard{}
	studentID := scaffoldStudentID
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, guard, stubCreditLedger())

	_, err := svc.Mark(context.Background(), Actor{
		UserID: scaffoldActorID,
		Email:  "admin@amrita.edu",
		Role:   "admin",
	}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodCamera,
		StudentID:     &studentID,
	})

	if err != nil {
		t.Fatalf("admin live NFC mark = %v, want nil", err)
	}
	if writer.inserts != 1 {
		t.Fatalf("admin live mark wrote %d rows, want 1", writer.inserts)
	}
	if writer.lastRecord.Slot != 2 {
		t.Fatalf("admin live mark slot = %d, want server-derived slot 2", writer.lastRecord.Slot)
	}
	if writer.lastRecord.ActorRole != "admin" {
		t.Fatalf("admin live mark actor role = %q, want admin", writer.lastRecord.ActorRole)
	}
	if len(guard.calls) != 1 || guard.calls[0][0] != "admin" {
		t.Fatalf("scope guard calls = %+v, want one admin live scope check", guard.calls)
	}
}

func TestAdminLiveMark_RejectsClientSlotAndDate(t *testing.T) {
	writer := &countingWriter{}
	studentID := scaffoldStudentID
	slot := 4
	day := campusTime(t, 2026, time.September, 4, 0, 0)
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, &recordingGuard{}, stubCreditLedger())

	_, err := svc.Mark(context.Background(), Actor{
		UserID: scaffoldActorID,
		Email:  "admin@amrita.edu",
		Role:   "admin",
	}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodCamera,
		StudentID:     &studentID,
		Slot:          &slot,
		ODDate:        &day,
	})

	var de domain.DomainError
	if !errors.As(err, &de) || len(de.Details) != 1 || de.Details[0].Field != "slot" {
		t.Fatalf("admin live mark with client slot/date = %v, want slot forbidden validation", err)
	}
	if writer.inserts != 0 {
		t.Fatalf("admin live mark with client slot/date wrote %d rows, want 0", writer.inserts)
	}
}

func TestTeacherAdminMark_RejectsExplicitODDateOutsideEventRange(t *testing.T) {
	writer := &countingWriter{}
	studentID := scaffoldStudentID
	slot := 4
	reason := "manual attendance correction"
	chosenDay := campusTime(t, 2026, time.August, 13, 0, 0)
	svc := NewService(
		scaffoldReader{
			eventDate: campusTime(t, 2026, time.August, 10, 0, 0),
			endDate:   campusTime(t, 2026, time.August, 12, 0, 0),
		},
		stubCardResolver(),
		writer,
		stubAuditStore(),
		scaffoldClock{},
		branchScopingGuard{},
		stubCreditLedger(),
	)

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
		ODDate:        &chosenDay,
		ReasonNote:    &reason,
	})

	var de domain.DomainError
	if !errors.As(err, &de) || len(de.Details) != 1 || de.Details[0].Field != "od_date" || de.Details[0].Code != "outside_event_range" {
		t.Fatalf("teacher mark outside range = %v, want od_date outside_event_range validation", err)
	}
	if writer.inserts != 0 {
		t.Fatalf("outside-range mark wrote %d rows, want 0", writer.inserts)
	}
}

func TestTeacherAdminMark_RejectsTeacherOutsideStudentBranchBeforeWrite(t *testing.T) {
	requireTeacherAdminMarkATDD(t)

	writer := &countingWriter{}
	studentID := scaffoldStudentID
	slot := 4
	reason := "manual attendance correction"
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
		ReasonNote:    &reason,
	})

	if err == nil {
		t.Fatal("[P0] teacher outside the student's branch was accepted; want RBAC_FORBIDDEN and no write")
	}
	if writer.inserts != 0 {
		t.Fatalf("[P0] unauthorized teacher branch mismatch wrote %d rows, want 0", writer.inserts)
	}
}

func TestTeacherAdminMark_RequiresReasonBeforeReadingOrWriting(t *testing.T) {
	writer := &countingWriter{}
	studentID := scaffoldStudentID
	slot := 4
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, branchScopingGuard{}, stubCreditLedger())

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
	if err == nil || !strings.Contains(err.Error(), "VALIDATION_FAILED") {
		t.Fatalf("teacher mark without reason = %v, want VALIDATION_FAILED", err)
	}
	if writer.inserts != 0 {
		t.Fatalf("teacher mark without reason wrote %d rows, want 0", writer.inserts)
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
