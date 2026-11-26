package event

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// removingWriter adds the optional removal capability to the coordinator fake.
type removingWriter struct {
	fakeCoordinatorWriter
	removal     CoordinatorRemoval
	removeErr   error
	removeCalls int
	input       RemoveCoordinatorAssignmentWrite
}

func (w *removingWriter) RemoveCoordinatorAssignment(_ context.Context, _ pgx.Tx, input RemoveCoordinatorAssignmentWrite) (CoordinatorRemoval, error) {
	w.removeCalls++
	w.input = input
	return w.removal, w.removeErr
}

func removalInput() RemoveCoordinatorAssignmentInput {
	return RemoveCoordinatorAssignmentInput{EventID: activeEventID, UserID: coordinatorUserID}
}

func wantDomainCode(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
}

func TestCoordinatorRemovalRefusesBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		actor  Actor
		input  RemoveCoordinatorAssignmentInput
		reader *fakeCoordinatorReader
		want   contracts.ErrorCode
	}{
		{"malformed event id", adminActor(), RemoveCoordinatorAssignmentInput{EventID: "nope", UserID: coordinatorUserID}, &fakeCoordinatorReader{exists: true}, contracts.ErrorCodeVALIDATIONFAILED},
		{"malformed user id", adminActor(), RemoveCoordinatorAssignmentInput{EventID: activeEventID, UserID: "nope"}, &fakeCoordinatorReader{exists: true}, contracts.ErrorCodeVALIDATIONFAILED},
		{"non-admin", Actor{UserID: coordinatorUserID, Email: "c@amrita.edu", Role: "coordinator"}, removalInput(), &fakeCoordinatorReader{exists: true}, contracts.ErrorCodeRBACFORBIDDEN},
		{"unknown event", adminActor(), removalInput(), &fakeCoordinatorReader{statusErr: ErrEventNotFound}, contracts.ErrorCodeEVENTNOTFOUND},
		// The same rule assignment follows: an archived event is immutable.
		{"archived event", adminActor(), removalInput(), &fakeCoordinatorReader{status: StatusArchived, exists: true}, contracts.ErrorCodeEVENTARCHIVED},
		// A repeat removal is a 404, as the category-access update is for a missing assignment.
		{"not assigned", adminActor(), removalInput(), &fakeCoordinatorReader{exists: false}, contracts.ErrorCodeUSERNOTFOUND},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &removingWriter{}
			audit := &fakeAuditStore{}
			err := NewService(tc.reader, audit, writer).RemoveCoordinatorAssignment(context.Background(), tc.actor, tc.input)
			wantDomainCode(t, err, tc.want)
			if writer.removeCalls != 0 || audit.calls != 0 {
				t.Fatalf("writer calls=%d audit calls=%d, want no write before the refusal", writer.removeCalls, audit.calls)
			}
		})
	}
}

func TestCoordinatorRemovalCommitsOneAuditedSnapshot(t *testing.T) {
	writer := &removingWriter{removal: CoordinatorRemoval{
		AssignmentID: "assignment-1", UserID: coordinatorUserID, EventID: activeEventID,
		Email: "coord@amrita.edu", FallbackCreditLimit: 5,
		RemovedCategoryIDs: []string{coordinatorCategoryID}, RemovedCreditRows: 2, RemovedCreditConsumed: 3,
	}}
	audit := &fakeAuditStore{}
	if err := NewService(&fakeCoordinatorReader{exists: true}, audit, writer).
		RemoveCoordinatorAssignment(context.Background(), adminActor(), removalInput()); err != nil {
		t.Fatalf("RemoveCoordinatorAssignment: %v", err)
	}
	if writer.removeCalls != 1 || writer.input.EventID.String() != activeEventID || writer.input.UserID.String() != coordinatorUserID {
		t.Fatalf("writer calls=%d input=%#v", writer.removeCalls, writer.input)
	}
	if audit.calls != 1 || !audit.changed || audit.event.Action != domain.ActionCoordinatorRemoved {
		t.Fatalf("audit calls=%d changed=%v action=%q", audit.calls, audit.changed, audit.event.Action)
	}
	if audit.event.TargetID == nil || *audit.event.TargetID != "assignment-1" {
		t.Fatalf("target id = %#v, want the removed assignment id", audit.event.TargetID)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["email"] != "coord@amrita.edu" || payload["removed_credit_usage_consumed"] != float64(3) {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestCoordinatorRemovalMapsARacedRemovalTo404(t *testing.T) {
	writer := &removingWriter{removeErr: ErrCoordinatorAssignmentNotFound}
	err := NewService(&fakeCoordinatorReader{exists: true}, &fakeAuditStore{}, writer).
		RemoveCoordinatorAssignment(context.Background(), adminActor(), removalInput())
	wantDomainCode(t, err, contracts.ErrorCodeUSERNOTFOUND)
}

func TestCoordinatorRemovalFailsClosedWithoutTheWriterCapability(t *testing.T) {
	err := NewService(&fakeCoordinatorReader{exists: true}, &fakeAuditStore{}, &fakeCoordinatorWriter{}).
		RemoveCoordinatorAssignment(context.Background(), adminActor(), removalInput())
	if err == nil {
		t.Fatal("want an error when the writer cannot remove")
	}
}
