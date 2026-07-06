// revoke_service_test.go — Story 1.5c (DoD 5, 8, 9): the forced-logout /
// session-revocation service. UNIT test, no DB (fakes for the tx-runner, the DB
// writer seam, the Supabase adapter, and the user directory). It asserts: the
// pure RevokeRequest.Validate rules; co-commit through store.WithAudit carrying
// the settled action + audit fields; best-effort Supabase failure not blocking
// the authoritative local revoke; the narrow admin gate; and idempotent
// (changed=false) duplicate session revokes writing no audit row.
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// --- fakes for the service collaborators -----------------------------------

// fakeRevStore stands in for *store.Store. It runs the write closure with a nil
// tx (the fakes below ignore tx) so the test observes the captured AuditEvent
// and the changed signal WithAudit would use to decide whether an audit row is
// written.
type fakeRevStore struct {
	called   bool
	evt      store.AuditEvent
	changed  bool
	writeErr error
}

func (f *fakeRevStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	f.called = true
	f.evt = evt
	if !evt.Action.Registered() {
		return store.ErrUnregisteredAction
	}
	changed, err := write(ctx, nil)
	if err != nil {
		return err
	}
	f.changed = changed
	return f.writeErr
}

// fakeRevWriter is the DB insert seam (sqlc-backed in production). Inserted
// reports first-insert vs idempotent no-op.
type fakeRevWriter struct {
	rev   Revocation
	err   error
	calls int
}

func (f *fakeRevWriter) InsertRevocation(ctx context.Context, tx pgx.Tx, arg InsertRevocationParams) (Revocation, error) {
	f.calls++
	return f.rev, f.err
}

// fakeSupabaseRevoker is the best-effort Supabase-side adapter.
type fakeSupabaseRevoker struct {
	err   error
	calls int
}

func (f *fakeSupabaseRevoker) RevokeSessions(ctx context.Context, subject string, sessionID *string, scope RevokeScope) error {
	f.calls++
	return f.err
}

// fakeDirectory resolves the actor's provisioned id + role (for the FK and the
// admin gate) and the target user's Supabase subject (revocation is keyed by sub,
// never email).
type fakeDirectory struct {
	actorID   string
	actorRole string
	actorErr  error
	targetSub string
	targetErr error
}

func (f *fakeDirectory) ResolveActor(ctx context.Context, subject string) (string, string, error) {
	return f.actorID, f.actorRole, f.actorErr
}

func (f *fakeDirectory) TargetSubject(ctx context.Context, userID string) (string, error) {
	return f.targetSub, f.targetErr
}

func newTestService(store *fakeRevStore, writer *fakeRevWriter, revoker *fakeSupabaseRevoker, dir *fakeDirectory) *RevocationService {
	return NewRevocationService(store, writer, revoker, dir)
}

func adminActor() VerifiedIdentity {
	return VerifiedIdentity{Email: "admin@amrita.edu", Subject: "actor-sub", SessionID: "sess-a"}
}

func validAllRequest() RevokeRequest {
	return RevokeRequest{Scope: RevokeScopeAll, Reason: "offboarding a leaver"}
}

// --- validation (pure, stable core) ----------------------------------------

// TestRevokeRequest_Validate — DoD 7 field rules, exercised without any
// collaborators. scope is required and enum-bounded; session_id is required +
// UUID iff scope=session and MUST be absent iff scope=all; reason is trimmed and
// 3..500 chars.
func TestRevokeRequest_Validate(t *testing.T) {
	uuid := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name string
		req  RevokeRequest
		ok   bool
	}{
		{"valid all", RevokeRequest{Scope: RevokeScopeAll, Reason: "leaver"}, true},
		{"valid session", RevokeRequest{Scope: RevokeScopeSession, SessionID: uuid, Reason: "lost device"}, true},
		{"missing scope", RevokeRequest{Reason: "leaver"}, false},
		{"unknown scope", RevokeRequest{Scope: "everything", Reason: "leaver"}, false},
		{"session without session_id", RevokeRequest{Scope: RevokeScopeSession, Reason: "leaver"}, false},
		{"session non-uuid session_id", RevokeRequest{Scope: RevokeScopeSession, SessionID: "not-a-uuid", Reason: "leaver"}, false},
		{"all with session_id", RevokeRequest{Scope: RevokeScopeAll, SessionID: uuid, Reason: "leaver"}, false},
		{"reason blank", RevokeRequest{Scope: RevokeScopeAll, Reason: "   "}, false},
		{"reason too short", RevokeRequest{Scope: RevokeScopeAll, Reason: "ab"}, false},
		{"reason too long", RevokeRequest{Scope: RevokeScopeAll, Reason: string(make([]byte, 501))}, false},
		// reason length is counted in runes, not bytes (contract minLength/maxLength
		// are character counts): a 500-rune multibyte reason (1500 bytes) is VALID
		// and a 2-rune multibyte reason (6 bytes) is too short.
		{"reason 500 multibyte runes ok", RevokeRequest{Scope: RevokeScopeAll, Reason: strings.Repeat("あ", 500)}, true},
		{"reason 2 multibyte runes too short", RevokeRequest{Scope: RevokeScopeAll, Reason: "あい"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.ok && err != nil {
				t.Errorf("[P0] want valid, got error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("[P0] want VALIDATION_FAILED, got nil")
				}
				var de domain.DomainError
				if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
					t.Errorf("[P0] want VALIDATION_FAILED domain error, got %v", err)
				}
			}
		})
	}
}

// --- service behavior ------------------------------------------------------

// TestRevokeUserSessions_AuditsWithSettledEvent — a valid admin revocation
// co-commits through store.WithAudit carrying the settled action and the DoD 9
// audit fields (actor account/role, target table auth_session_revocations,
// target id = revocation id). A genuine first insert reports changed=true.
func TestRevokeUserSessions_AuditsWithSettledEvent(t *testing.T) {
	st := &fakeRevStore{}
	writer := &fakeRevWriter{rev: Revocation{ID: "rev-1", Inserted: true}}
	dir := &fakeDirectory{actorRole: "admin", targetSub: "target-sub"}
	svc := newTestService(st, writer, &fakeSupabaseRevoker{}, dir)

	_, err := svc.RevokeUserSessions(context.Background(), adminActor(), "target-user-id", validAllRequest())
	if err != nil {
		t.Fatalf("[P0] admin revoke should succeed: %v", err)
	}
	if !st.called {
		t.Fatal("[P0] revocation must be written through store.WithAudit")
	}
	if st.evt.Action != domain.ActionAuthSessionRevoked {
		t.Errorf("[P0] audit action: want %q, got %q", domain.ActionAuthSessionRevoked, st.evt.Action)
	}
	if st.evt.TargetTable == nil || *st.evt.TargetTable != "auth_session_revocations" {
		t.Errorf("[P0] audit target table must be auth_session_revocations, got %v", st.evt.TargetTable)
	}
	if st.evt.TargetID == nil || *st.evt.TargetID != "rev-1" {
		t.Errorf("[P0] audit target id must be the revocation id, got %v", st.evt.TargetID)
	}
	if st.evt.ActorAccount == nil || *st.evt.ActorAccount != "admin@amrita.edu" {
		t.Errorf("[P1] audit actor account must be the admin email, got %v", st.evt.ActorAccount)
	}
	if !st.changed {
		t.Error("[P0] a genuine first insert must report changed=true (earns an audit row)")
	}
}

// TestRevokeUserSessions_SupabaseFailureDoesNotBlockLocalRevoke — the local
// deny-list is authoritative (DoD 5). A best-effort Supabase adapter failure
// must NOT roll back or fail the local revocation + audit.
func TestRevokeUserSessions_SupabaseFailureDoesNotBlockLocalRevoke(t *testing.T) {
	st := &fakeRevStore{}
	writer := &fakeRevWriter{rev: Revocation{ID: "rev-2", Inserted: true}}
	revoker := &fakeSupabaseRevoker{err: errors.New("supabase 500")}
	dir := &fakeDirectory{actorRole: "admin", targetSub: "target-sub"}
	svc := newTestService(st, writer, revoker, dir)

	if _, err := svc.RevokeUserSessions(context.Background(), adminActor(), "target-user-id", validAllRequest()); err != nil {
		t.Fatalf("[P0] Supabase adapter failure must not fail the authoritative local revoke: %v", err)
	}
	if !st.called {
		t.Error("[P0] local revocation + audit must still be committed when Supabase revoke fails")
	}
}

// TestRevokeUserSessions_NonAdminActorForbidden — the narrow admin gate (DoD 8):
// a provisioned non-admin actor is RBAC_FORBIDDEN and no revocation is written.
func TestRevokeUserSessions_NonAdminActorForbidden(t *testing.T) {
	st := &fakeRevStore{}
	dir := &fakeDirectory{actorRole: "student", targetSub: "target-sub"}
	svc := newTestService(st, &fakeRevWriter{}, &fakeSupabaseRevoker{}, dir)

	_, err := svc.RevokeUserSessions(context.Background(), adminActor(), "target-user-id", validAllRequest())
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("[P0] non-admin actor must be RBAC_FORBIDDEN, got %v", err)
	}
	if st.called {
		t.Error("[P0] no revocation must be written when the actor is not an admin")
	}
}

// TestRevokeUserSessions_IdempotentSessionRevokeWritesNoAuditRow — repeating a
// scope=session revoke is a no-op (DoD 3): the DB writer reports Inserted=false
// via the UNIQUE(supabase_subject, session_id) guard, so WithAudit's write
// returns changed=false and NO audit row is written.
func TestRevokeUserSessions_IdempotentSessionRevokeWritesNoAuditRow(t *testing.T) {
	st := &fakeRevStore{}
	writer := &fakeRevWriter{rev: Revocation{ID: "rev-existing", Inserted: false}}
	dir := &fakeDirectory{actorRole: "admin", targetSub: "target-sub"}
	svc := newTestService(st, writer, &fakeSupabaseRevoker{}, dir)

	req := RevokeRequest{Scope: RevokeScopeSession, SessionID: "33333333-3333-3333-3333-333333333333", Reason: "duplicate revoke"}
	if _, err := svc.RevokeUserSessions(context.Background(), adminActor(), "target-user-id", req); err != nil {
		t.Fatalf("[P1] idempotent duplicate revoke should not error: %v", err)
	}
	if st.changed {
		t.Error("[P1] a duplicate session revoke must report changed=false (no duplicate audit row)")
	}
}
