package userrole

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	adminID  = "10000000-0000-4000-8000-000000000001"
	targetID = "10000000-0000-4000-8000-000000000002"
)

type fakeAudit struct {
	calls   int
	changed bool
	event   store.AuditEvent
}

func (f *fakeAudit) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	f.calls++
	f.event = evt
	changed, err := write(ctx, nil)
	if err != nil {
		return err
	}
	f.changed = changed
	if changed && evt.PayloadFunc != nil {
		payload, err := evt.PayloadFunc()
		if err != nil {
			return err
		}
		f.event.Payload = payload
	}
	return nil
}

type fakeWriter struct {
	result Result
	err    error
	calls  int
	input  DemoteWrite
}

func (f *fakeWriter) Demote(_ context.Context, _ pgx.Tx, input DemoteWrite) (Result, error) {
	f.calls++
	f.input = input
	return f.result, f.err
}

func admin() Actor { return Actor{UserID: adminID, Email: "Admin@Amrita.edu", Role: RoleAdmin} }

func wantCode(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
}

func TestDemote_RefusesBeforeTheTransaction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		actor  Actor
		target string
		want   contracts.ErrorCode
	}{
		{"malformed id", admin(), "not-a-uuid", contracts.ErrorCodeVALIDATIONFAILED},
		{"non-admin actor", Actor{UserID: adminID, Role: "teacher"}, targetID, contracts.ErrorCodeRBACFORBIDDEN},
		{"self demotion", admin(), adminID, contracts.ErrorCodeSELFDEMOTIONREFUSED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeWriter{}
			audit := &fakeAudit{}
			_, err := NewService(audit, writer).Demote(context.Background(), tc.actor, tc.target)
			wantCode(t, err, tc.want)
			if writer.calls != 0 || audit.calls != 0 {
				t.Fatalf("writer=%d audit=%d calls, want none", writer.calls, audit.calls)
			}
		})
	}
}

func TestDemote_MapsWriterRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want contracts.ErrorCode
	}{
		{"unknown user", ErrUserNotFound, contracts.ErrorCodeUSERNOTFOUND},
		{"last admin", ErrLastAdmin, contracts.ErrorCodeLASTADMINDEMOTIONREFUSED},
		// The actor was demoted by someone else while this request waited on the lock.
		{"actor lost admin", ErrActorNotAdmin, contracts.ErrorCodeRBACFORBIDDEN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewService(&fakeAudit{}, &fakeWriter{err: tc.err}).Demote(context.Background(), admin(), targetID)
			wantCode(t, err, tc.want)
		})
	}
}

func TestDemote_WritesOneAuditRowWithTheSnapshot(t *testing.T) {
	writer := &fakeWriter{result: Result{
		UserID: targetID, Email: "t@amrita.edu", PreviousRole: "coordinator", Role: RoleStudent, Changed: true,
		RemovedCoordinatorEventIDs: []string{"e1", "e2"}, RemovedCategoryAssignments: 3, RemovedTeacherBranches: 0,
		RevocationID: "rev-1", SessionsRevoked: true,
	}}
	audit := &fakeAudit{}
	result, err := NewService(audit, writer).Demote(context.Background(), admin(), targetID)
	if err != nil {
		t.Fatalf("Demote: %v", err)
	}
	if writer.input.TargetID.String() != targetID || writer.input.ActorID.String() != adminID {
		t.Fatalf("writer input = %#v", writer.input)
	}
	if result.RemovedCoordinatorAssignments() != 2 || !result.SessionsRevoked {
		t.Fatalf("result = %#v", result)
	}
	if audit.calls != 1 || !audit.changed || audit.event.Action != domain.ActionUserDemoted {
		t.Fatalf("audit calls=%d changed=%v action=%q", audit.calls, audit.changed, audit.event.Action)
	}
	if audit.event.ActorAccount == nil || *audit.event.ActorAccount != "admin@amrita.edu" {
		t.Fatalf("actor account = %#v, want lowercased", audit.event.ActorAccount)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["previous_role"] != "coordinator" || payload["revocation_id"] != "rev-1" {
		t.Fatalf("payload = %#v", payload)
	}
}

// Demoting a student is a no-op: no change reported, so WithAudit writes no row.
func TestDemote_AlreadyAStudentIsANoOp(t *testing.T) {
	writer := &fakeWriter{result: Result{UserID: targetID, PreviousRole: RoleStudent, Role: RoleStudent}}
	audit := &fakeAudit{}
	result, err := NewService(audit, writer).Demote(context.Background(), admin(), targetID)
	if err != nil {
		t.Fatalf("Demote: %v", err)
	}
	if result.Changed || audit.changed {
		t.Fatalf("changed result=%v audit=%v, want a no-op", result.Changed, audit.changed)
	}
}
