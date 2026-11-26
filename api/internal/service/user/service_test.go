package user

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestResolveRole(t *testing.T) {
	t.Run("ordinary identity defaults to least-privileged student", func(t *testing.T) {
		got := ResolveRole("student@cb.students.amrita.edu", map[string]struct{}{})
		if got != RoleStudent {
			t.Fatalf("want RoleStudent for empty bootstrap allowlist, got %q", got)
		}
	})

	t.Run("bootstrap admin allowlist resolves admin case-insensitively", func(t *testing.T) {
		admins := map[string]struct{}{"first.admin@amrita.edu": {}}
		got := ResolveRole("FIRST.ADMIN@AMRITA.EDU", admins)
		if got != RoleAdmin {
			t.Fatalf("want RoleAdmin for allowlisted email, got %q", got)
		}
	})

	t.Run("first-login provisioning never assigns teacher or coordinator", func(t *testing.T) {
		for _, email := range []string{"teacher@amrita.edu", "coordinator@amrita.edu", "student@amrita.edu"} {
			got := ResolveRole(email, map[string]struct{}{})
			if got == RoleTeacher || got == RoleCoordinator {
				t.Fatalf("first-login role resolution must never assign %q for %s", got, email)
			}
		}
	})
}

// fakeTxStore is a stand-in for *store.Store: it captures the audit event and
// the changed signal without a database, so the provisioning contract (audit
// shape + no-audit-on-no-op) can be proven as a unit test.
type fakeTxStore struct {
	events  []store.AuditEvent
	changed []bool
	err     error
}

func (s *fakeTxStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	s.events = append(s.events, evt)
	if s.err != nil {
		return s.err
	}
	changed, err := write(ctx, nil)
	s.changed = append(s.changed, changed)
	return err
}

// fakeProvisionQueries returns a canned row (or error); tx is irrelevant to the
// unit test — the real tx binding is proven by the integration suite.
type fakeProvisionQueries struct {
	row User
	err error
}

func (q fakeProvisionQueries) ProvisionUser(_ context.Context, _ pgx.Tx, _ ProvisionUserParams) (User, error) {
	if q.err != nil {
		return User{}, q.err
	}
	return q.row, nil
}

func TestProvision_FirstLoginWritesAuditRow(t *testing.T) {
	id := auth.VerifiedIdentity{
		Email:   "FIRST.ADMIN@AMRITA.EDU",
		Name:    "First Admin",
		Subject: "supabase-sub-admin",
	}
	user := User{
		ID:              "user-id-admin",
		SupabaseSubject: id.Subject,
		Email:           "first.admin@amrita.edu",
		Name:            id.Name,
		Role:            RoleAdmin,
		Inserted:        true,
	}
	storeSpy := &fakeTxStore{}
	svc := NewService(storeSpy, map[string]struct{}{"first.admin@amrita.edu": {}}, fakeProvisionQueries{row: user})

	got, err := svc.Provision(context.Background(), id)
	if err != nil {
		t.Fatalf("first provisioning should succeed: %v", err)
	}
	if got.Role != RoleAdmin || got.Email != "first.admin@amrita.edu" {
		t.Fatalf("provisioned user mismatch: %+v", got)
	}
	if len(storeSpy.events) != 1 {
		t.Fatalf("expected one WithAudit event, got %d", len(storeSpy.events))
	}
	evt := storeSpy.events[0]
	if evt.Action != domain.ActionUserProvisioned {
		t.Fatalf("audit action must be user.provisioned, got %q", evt.Action)
	}
	if evt.ActorAccount == nil || *evt.ActorAccount != "first.admin@amrita.edu" {
		t.Errorf("actor_account must be lowercased provisioned email, got %#v", evt.ActorAccount)
	}
	if evt.ActorRole == nil || *evt.ActorRole != string(RoleAdmin) {
		t.Errorf("actor_role must be resolved role, got %#v", evt.ActorRole)
	}
	if evt.TargetTable == nil || *evt.TargetTable != "users" {
		t.Errorf("target_table must be users, got %#v", evt.TargetTable)
	}
	// TargetID is a pointer filled by the write; it must dereference to the new
	// user id at audit-insert time even though evt was captured before the write.
	if evt.TargetID == nil || *evt.TargetID != "user-id-admin" {
		t.Errorf("target_id must be provisioned user id, got %#v", evt.TargetID)
	}
	var payload map[string]string
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		t.Fatalf("decode provisioning audit payload: %v", err)
	}
	wantPayload := map[string]string{
		"email":            "first.admin@amrita.edu",
		"name":             "First Admin",
		"resolved_role":    string(RoleAdmin),
		"supabase_subject": "supabase-sub-admin",
	}
	for key, want := range wantPayload {
		if payload[key] != want {
			t.Errorf("payload[%s] = %q, want %q", key, payload[key], want)
		}
	}
	if len(storeSpy.changed) != 1 || !storeSpy.changed[0] {
		t.Fatalf("first insert must report changed=true to WithAudit, got %v", storeSpy.changed)
	}
}

func TestProvision_ReturningUserIsNoAuditNoOp(t *testing.T) {
	id := auth.VerifiedIdentity{Email: "student@amrita.edu", Subject: "sub-student"}
	returning := User{
		ID:              "user-id-student",
		SupabaseSubject: id.Subject,
		Email:           id.Email,
		Role:            RoleStudent,
		Inserted:        false,
	}
	storeSpy := &fakeTxStore{}
	svc := NewService(storeSpy, map[string]struct{}{}, fakeProvisionQueries{row: returning})

	if _, err := svc.Provision(context.Background(), id); err != nil {
		t.Fatalf("returning-user provisioning should succeed: %v", err)
	}
	if len(storeSpy.changed) != 1 || storeSpy.changed[0] {
		t.Fatalf("returning user must report changed=false so WithAudit writes no audit row, got %v", storeSpy.changed)
	}
}

func TestProvision_PropagatesBusinessWriteFailure(t *testing.T) {
	boom := errors.New("provision query failed")
	storeSpy := &fakeTxStore{}
	svc := NewService(storeSpy, map[string]struct{}{}, fakeProvisionQueries{err: boom})

	_, err := svc.Provision(context.Background(), auth.VerifiedIdentity{Email: "student@amrita.edu", Subject: "sub"})
	if !errors.Is(err, boom) {
		t.Fatalf("expected provisioning query error to propagate, got %v", err)
	}
}
