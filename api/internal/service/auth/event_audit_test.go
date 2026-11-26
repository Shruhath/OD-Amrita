// event_audit_test.go — the auth-event audit service, the
// shared write path for the two auth events. UNIT test, no DB — a fake
// TxRunner captures the store.AuditEvent + the `changed` signal WithAudit would
// use, so the test observes the built immutable row without a database.
//
// It asserts, per AC ("an immutable audit row … capturing actor/account, role,
// device, timestamp, and reason" + "the event name comes from the events.go
// registry"):
//   - auth.login_succeeded builds action(from registry)/actor_account(lowercased)/
//     actor_role(provisioned)/payload{device,session_id,subject}, changed=true;
//   - auth.domain_rejected builds action/actor_account(lowercased)/actor_role=nil/
//     payload{device,reason}, changed=true;
//   - a blank device is omitted from the payload;
//   - a WithAudit failure propagates so the call site can fail closed
//     ("no auth event path can complete without its audit row");
//   - both actions are registered (else WithAudit would reject them);
//   - forced logout still audits auth.session_revoked (reused, not duplicated).
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// fakeTxRunner stands in for *store.Store's WithAudit seam (the narrow TxRunner
// the EventAuditor depends on). It runs the write closure with a nil tx — an auth
// event's write is a no-op that touches no business table — so the test sees the
// captured AuditEvent and the `changed` signal. It mirrors the production
// registry gate so an unregistered action is rejected exactly as WithAudit would.
type fakeTxRunner struct {
	called   bool
	evt      store.AuditEvent
	changed  bool
	writeErr error // returned from WithAudit after write runs (simulated audit-insert failure)
}

func (f *fakeTxRunner) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
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

// newAuditor is the seam under test: an EventAuditor over a fake TxRunner. When
// the production constructor lands (NewEventAuditor(store TxRunner) *EventAuditor)
// this compiles and the assertions below drive its behavior.
func newAuditor(r *fakeTxRunner) *EventAuditor {
	return NewEventAuditor(r)
}

func decodeAuditPayload(t *testing.T, b []byte) map[string]any {
	t.Helper()
	if len(b) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("audit payload must be a valid JSON object: %v (%s)", err, string(b))
	}
	return m
}

// TestEventAuditor_LoginSucceeded_BuildsAuditRow — [P0] the login-success write
// produced at first backend contact (GET /auth/me) carries the registered action,
// the lowercased/trimmed login account, the provisioned role, and a payload with
// device + session_id + subject, and reports changed=true (an auth event's audit
// row is its only artifact, so WithAudit must insert it).
func TestEventAuditor_LoginSucceeded_BuildsAuditRow(t *testing.T) {
	r := &fakeTxRunner{}
	a := newAuditor(r)

	err := a.LoginSucceeded(context.Background(), "  Student@AMRITA.edu ", "student", "sub-123", "sess-abc", "OD-Amrita/1.0 (iPhone)")
	if err != nil {
		t.Fatalf("[P0] LoginSucceeded should succeed when the audit write succeeds: %v", err)
	}
	if !r.called {
		t.Fatalf("[P0] LoginSucceeded must co-commit through store.WithAudit")
	}
	if !r.changed {
		t.Errorf("[P0] an auth event's audit row is its ONLY artifact — the write must report changed=true so WithAudit inserts it")
	}
	if r.evt.Action != domain.ActionAuthLoginSucceeded {
		t.Errorf("[P0] action must come from the events.go registry: want %q got %q", domain.ActionAuthLoginSucceeded, r.evt.Action)
	}
	if r.evt.ActorAccount == nil || *r.evt.ActorAccount != "student@amrita.edu" {
		t.Errorf("[P0] actor_account must be the lowercased/trimmed login email, got %v", derefStr(r.evt.ActorAccount))
	}
	if r.evt.ActorRole == nil || *r.evt.ActorRole != "student" {
		t.Errorf("[P0] actor_role must be the provisioned role, got %v", derefStr(r.evt.ActorRole))
	}
	p := decodeAuditPayload(t, r.evt.Payload)
	if p["device"] != "OD-Amrita/1.0 (iPhone)" {
		t.Errorf("[P1] payload.device must carry the captured device, got %v", p["device"])
	}
	if p["session_id"] != "sess-abc" {
		t.Errorf("[P1] payload.session_id must carry the Supabase session id (dedup anchor), got %v", p["session_id"])
	}
	if p["subject"] != "sub-123" {
		t.Errorf("[P1] payload.subject must carry the Supabase subject, got %v", p["subject"])
	}
}

// TestEventAuditor_DomainRejected_BuildsAuditRow — [P0] the domain-rejection
// write (from the middleware's EnforceDomain failure branch, after a VALID token)
// carries the registered action, the lowercased rejected account, a NIL role (a
// rejected identity is never provisioned), and a payload with device + reason.
func TestEventAuditor_DomainRejected_BuildsAuditRow(t *testing.T) {
	r := &fakeTxRunner{}
	a := newAuditor(r)

	err := a.DomainRejected(context.Background(), " Outsider@Gmail.com ", "Mozilla/5.0", "email domain not permitted")
	if err != nil {
		t.Fatalf("[P0] DomainRejected should succeed when the audit write succeeds: %v", err)
	}
	if !r.called || !r.changed {
		t.Fatalf("[P0] DomainRejected must co-commit through WithAudit with changed=true (called=%v changed=%v)", r.called, r.changed)
	}
	if r.evt.Action != domain.ActionAuthDomainRejected {
		t.Errorf("[P0] action must be the registered %q, got %q", domain.ActionAuthDomainRejected, r.evt.Action)
	}
	if r.evt.ActorAccount == nil || *r.evt.ActorAccount != "outsider@gmail.com" {
		t.Errorf("[P0] actor_account must be the lowercased/trimmed rejected email (FR48 wants the account), got %v", derefStr(r.evt.ActorAccount))
	}
	if r.evt.ActorRole != nil {
		t.Errorf("[P0] a domain-rejected identity has no provisioned role — actor_role must be nil (SQL NULL), got %q", *r.evt.ActorRole)
	}
	p := decodeAuditPayload(t, r.evt.Payload)
	if p["device"] != "Mozilla/5.0" {
		t.Errorf("[P1] payload.device must carry the captured device, got %v", p["device"])
	}
	if p["reason"] != "email domain not permitted" {
		t.Errorf("[P1] payload.reason must carry the rejection reason, got %v", p["reason"])
	}
	if _, ok := p["session_id"]; ok {
		t.Errorf("[P2] a domain_rejected row must not carry a session_id (no session was established), got %v", p["session_id"])
	}
}

// TestEventAuditor_OmitsBlankDevice — [P2] an empty/blank device (e.g. a client
// that sent no User-Agent) is omitted from the payload rather than written as an
// empty string.
func TestEventAuditor_OmitsBlankDevice(t *testing.T) {
	r := &fakeTxRunner{}
	a := newAuditor(r)

	if err := a.LoginSucceeded(context.Background(), "s@amrita.edu", "student", "sub", "sess", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := decodeAuditPayload(t, r.evt.Payload)
	if _, ok := p["device"]; ok {
		t.Errorf("[P2] a blank device must be omitted from the payload, got %v", p["device"])
	}
}

// TestEventAuditor_LoginSucceeded_PropagatesAuditFailure — [P1] if the audit
// write fails, the error must surface so the call site can fail closed: a login
// that could not be audited must not complete as a success.
func TestEventAuditor_LoginSucceeded_PropagatesAuditFailure(t *testing.T) {
	r := &fakeTxRunner{writeErr: errors.New("audit insert: connection reset")}
	a := newAuditor(r)

	if err := a.LoginSucceeded(context.Background(), "s@amrita.edu", "student", "sub", "sess", "dev"); err == nil {
		t.Fatalf("[P1] a failed audit write must be returned so the caller fails closed (no login completes unaudited)")
	}
}

// TestEventAuditor_DomainRejected_PropagatesAuditFailure — [P1] same fail-closed
// contract for the rejection path: an unrecordable rejection must not be swallowed.
func TestEventAuditor_DomainRejected_PropagatesAuditFailure(t *testing.T) {
	r := &fakeTxRunner{writeErr: errors.New("audit insert: connection reset")}
	a := newAuditor(r)

	if err := a.DomainRejected(context.Background(), "outsider@gmail.com", "dev", "email domain not permitted"); err == nil {
		t.Fatalf("[P1] a failed audit write on domain rejection must be returned so the middleware fails closed")
	}
}

// TestAuthEvents_AreRegistered — [P2] guard: both auth events the auditor emits
// must be in the events.go registry, or store.WithAudit rejects the write with
// ErrUnregisteredAction (both constants are pre-registered in the registry).
func TestAuthEvents_AreRegistered(t *testing.T) {
	for _, act := range []domain.Action{domain.ActionAuthLoginSucceeded, domain.ActionAuthDomainRejected} {
		if !act.Registered() {
			t.Errorf("[P2] %q must be registered in events.go/AllActions or WithAudit rejects it", act)
		}
	}
}

// TestForcedLogout_StillAuditsSessionRevoked — [P1] regression: the auth-event
// auditor REUSES the forced-logout audit and adds no duplicate event. This guards
// that a forced logout still writes auth.session_revoked through WithAudit,
// unchanged by EventAuditor. Reuses the revocation service + fakes (same package)
// — it does not re-implement the revocation path.
func TestForcedLogout_StillAuditsSessionRevoked(t *testing.T) {
	st := &fakeRevStore{}
	writer := &fakeRevWriter{rev: Revocation{ID: "rev-1", Inserted: true}}
	dir := &fakeDirectory{actorID: "actor-id", actorRole: roleAdmin, targetSub: "target-sub"}
	svc := newTestService(st, writer, &fakeSupabaseRevoker{}, dir)

	if _, err := svc.RevokeUserSessions(context.Background(), adminActor(), "user-1", validAllRequest()); err != nil {
		t.Fatalf("[P1] forced logout should still succeed: %v", err)
	}
	if !st.called {
		t.Fatalf("[P1] forced logout must still co-commit through WithAudit")
	}
	if st.evt.Action != domain.ActionAuthSessionRevoked {
		t.Errorf("[P1] forced logout must still audit %q (1.5c reused, not duplicated by 1.7), got %q", domain.ActionAuthSessionRevoked, st.evt.Action)
	}
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
