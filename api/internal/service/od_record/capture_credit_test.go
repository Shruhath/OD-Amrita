package odrecord

import (
	"context"
	"errors"
	"testing"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

func TestCaptureCreditDebitScaffold_CameraAndSearchConsumeSharedCredit(t *testing.T) {
	for _, method := range []CaptureMethod{CaptureMethodCamera, CaptureMethodSearch} {
		t.Run(string(method), func(t *testing.T) {
			credits := stubCreditLedger()
			writer := &countingWriter{}
			svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, credits)
			studentID := scaffoldStudentID

			_, err := svc.Mark(context.Background(), Actor{UserID: scaffoldActorID, Email: "coord@amrita.edu", Role: "coordinator"}, MarkInput{
				EventID:       scaffoldEventID,
				CategoryID:    scaffoldCategoryID,
				CaptureMethod: method,
				StudentID:     &studentID,
			})
			if err != nil {
				t.Fatalf("Mark(%s) = %v", method, err)
			}
			if credits.debits != 1 || writer.inserts != 1 {
				t.Fatalf("debits/inserts = %d/%d, want 1/1", credits.debits, writer.inserts)
			}
		})
	}
}

func TestCaptureCreditDebitScaffold_NFCNeverConsumesOrBlocks(t *testing.T) {
	credits := &fixedCreditLedger{
		state: FallbackCreditState{Limit: 5, Consumed: 5, Remaining: 0, Exhausted: true, WarningThreshold: 1},
		err:   CaptureCreditExhaustedError{State: FallbackCreditState{Limit: 5, Consumed: 5, Remaining: 0, Exhausted: true}},
	}
	writer := &countingWriter{}
	svc := NewService(stubReader(), acceptingCardResolver{}, writer, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, credits)

	_, err := svc.Mark(context.Background(), Actor{UserID: scaffoldActorID, Email: "coord@amrita.edu", Role: "coordinator"}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodNFC,
		Card:          &CardInput{Version: "v1", Token: "token"},
	})
	if err != nil {
		t.Fatalf("NFC mark at exhausted fallback cap = %v, want nil", err)
	}
	if credits.debits != 0 || writer.inserts != 1 {
		t.Fatalf("debits/inserts = %d/%d, want 0/1", credits.debits, writer.inserts)
	}
}

type acceptingCardResolver struct{}

func (acceptingCardResolver) Resolve(context.Context, Actor, CardInput) (Student, error) {
	return Student{
		StudentID:   scaffoldStudentID,
		RollNo:      "CB.SC.U4CSE24001",
		DisplayName: "Scaffold Student",
		Branch:      "CSE",
		Section:     "A",
		Batch:       "2024",
	}, nil
}

func TestCaptureCreditDebitScaffold_ExhaustedWritesBlockedAuditOnly(t *testing.T) {
	credits := &fixedCreditLedger{err: CaptureCreditExhaustedError{
		State: FallbackCreditState{Limit: 5, Consumed: 5, Remaining: 0, Exhausted: true, WarningThreshold: 1},
	}}
	writer := &countingWriter{}
	audit := &recordingAuditStore{}
	svc := NewService(stubReader(), stubCardResolver(), writer, audit, scaffoldClock{}, AllowAnyScope{}, credits)
	studentID := scaffoldStudentID

	_, err := svc.Mark(context.Background(), Actor{UserID: scaffoldActorID, Email: "coord@amrita.edu", Role: "coordinator"}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodSearch,
		StudentID:     &studentID,
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeCAPTURECREDITLIMITREACHED {
		t.Fatalf("error = %#v, want CAPTURE_CREDIT_LIMIT_REACHED", err)
	}
	if writer.inserts != 0 {
		t.Fatalf("writer inserts = %d, want 0", writer.inserts)
	}
	if len(audit.actions) != 1 || audit.actions[0] != domain.ActionScanBlocked {
		t.Fatalf("audit actions = %v, want one scan.blocked", audit.actions)
	}
	if !audit.payloadContains("capture_credit_exhausted") {
		t.Fatalf("blocked audit payloads = %s, want reason capture_credit_exhausted", audit.payloads)
	}
}

func TestCaptureCreditDebitScaffold_DuplicateAndFailedWritesRollbackDebit(t *testing.T) {
	credits := stubCreditLedger()
	writer := &countingWriter{err: ErrDuplicatePresentRecord}
	svc := NewService(stubReader(), stubCardResolver(), writer, stubAuditStore(), scaffoldClock{}, AllowAnyScope{}, credits)
	studentID := scaffoldStudentID

	_, err := svc.Mark(context.Background(), Actor{UserID: scaffoldActorID, Email: "coord@amrita.edu", Role: "coordinator"}, MarkInput{
		EventID:       scaffoldEventID,
		CategoryID:    scaffoldCategoryID,
		CaptureMethod: CaptureMethodCamera,
		StudentID:     &studentID,
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeSCANDUPLICATE {
		t.Fatalf("error = %#v, want SCAN_DUPLICATE", err)
	}
	if credits.debits != 1 || writer.inserts != 1 {
		t.Fatalf("debits/inserts = %d/%d, want one attempted debit and one failed insert inside the same callback", credits.debits, writer.inserts)
	}
}

func TestCaptureCreditStatusScaffold_ReturnsDatabaseTruthWithoutCreatingUsageRows(t *testing.T) {
	credits := &fixedCreditLedger{state: FallbackCreditState{Limit: 3, Consumed: 5, Remaining: 0, Exhausted: true, WarningThreshold: 1}}
	svc := NewStatusService(stubReader(), &stubCounts{}, credits, scaffoldClock{})

	status, err := svc.MarkStatus(context.Background(), Actor{UserID: scaffoldActorID, Role: "coordinator"}, scaffoldEventID, scaffoldCategoryID)
	if err != nil {
		t.Fatalf("MarkStatus = %v", err)
	}
	if status.FallbackCredit.Limit != 3 || status.FallbackCredit.Consumed != 5 || status.FallbackCredit.Remaining != 0 || !status.FallbackCredit.Exhausted {
		t.Fatalf("fallback credit = %+v, want lowered-cap exhausted state", status.FallbackCredit)
	}
}
