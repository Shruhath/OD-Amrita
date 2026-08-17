package user

// read_before_write_test.go — the cost fix: /auth/me must not write when nothing
// changed. Production measured ProvisionUser as the most expensive query in the
// database (2,157 calls, 8ms average, 17s total), almost entirely re-writing
// last_login_at to a value seconds newer than the one already stored.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// countingTxStore records how many times the WRITE path was entered at all.
type countingTxStore struct {
	transactions int
	row          User
}

func (s *countingTxStore) WithAudit(ctx context.Context, _ store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	s.transactions++
	_, err := write(ctx, nil)
	return err
}

type stubCurrentReader struct {
	current Current
	err     error
}

func (r stubCurrentReader) CurrentUser(context.Context, string) (Current, error) {
	return r.current, r.err
}

const testSubject = "sub-abc"

func testIdentity() auth.VerifiedIdentity {
	return auth.VerifiedIdentity{Subject: testSubject, Email: "Person@amrita.edu", Name: "Person"}
}

func newTestService(store TxRunner, reader CurrentReader, now time.Time) *Service {
	svc := NewService(store, map[string]struct{}{}, fakeProvisionQueries{
		row: User{ID: "user-1", SupabaseSubject: testSubject, Email: "person@amrita.edu", Name: "Person", Role: RoleStudent},
	})
	if reader != nil {
		svc = svc.WithCurrentReader(reader)
	}
	return svc.withClock(func() time.Time { return now })
}

// THE POINT OF THE WHOLE CHANGE: a returning user whose row is already correct
// must not open a transaction.
func TestProvision_UnchangedReturningUserOpensNoTransaction(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	st := &countingTxStore{}
	svc := newTestService(st, stubCurrentReader{current: Current{
		ID:          "user-1",
		Email:       "person@amrita.edu",
		Name:        "Person",
		Role:        RoleStudent,
		LastLoginAt: now.Add(-time.Minute),
	}}, now)

	got, err := svc.Provision(context.Background(), testIdentity())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if st.transactions != 0 {
		t.Errorf("opened %d transactions, want 0 — an unchanged login must cost one read, not a write", st.transactions)
	}
	if got.ID != "user-1" || got.Email != "person@amrita.edu" || got.Role != RoleStudent {
		t.Errorf("returned %+v, want the stored row served straight from the read", got)
	}
	if got.Inserted {
		t.Error("Inserted must be false — a row read cannot have been inserted by this call, or it would earn a spurious audit row")
	}
}

// A first login still inserts and still earns its audit row. This is the
// property the optimisation must not break.
func TestProvision_FirstContactStillTakesTheWritePath(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	st := &countingTxStore{}
	svc := newTestService(st, stubCurrentReader{err: ErrNotProvisioned}, now)

	if _, err := svc.Provision(context.Background(), testIdentity()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if st.transactions != 1 {
		t.Errorf("opened %d transactions, want 1 — an unprovisioned subject must be inserted", st.transactions)
	}
}

// Identity fields track Supabase and must follow it immediately, not in fifteen
// minutes: a display-name change upstream should land on the next login.
func TestProvision_IdentityDriftWritesImmediately(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Second)

	for _, tc := range []struct {
		name    string
		current Current
	}{
		{"email changed upstream", Current{ID: "user-1", Email: "old@amrita.edu", Name: "Person", Role: RoleStudent, LastLoginAt: fresh}},
		{"name changed upstream", Current{ID: "user-1", Email: "person@amrita.edu", Name: "Old Name", Role: RoleStudent, LastLoginAt: fresh}},
		{"last_login_at never set", Current{ID: "user-1", Email: "person@amrita.edu", Name: "Person", Role: RoleStudent}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &countingTxStore{}
			svc := newTestService(st, stubCurrentReader{current: tc.current}, now)
			if _, err := svc.Provision(context.Background(), testIdentity()); err != nil {
				t.Fatalf("Provision: %v", err)
			}
			if st.transactions != 1 {
				t.Errorf("opened %d transactions, want 1 — drift must be written through", st.transactions)
			}
		})
	}
}

// A stored role that differs from the first-login resolution is CORRECT after an
// admin elevates someone — ProvisionUser sets role only on insert. Treating it
// as drift would write on every login for exactly the staff who log in most.
func TestProvision_ElevatedRoleIsNotTreatedAsDrift(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	st := &countingTxStore{}
	svc := newTestService(st, stubCurrentReader{current: Current{
		ID:          "user-1",
		Email:       "person@amrita.edu",
		Name:        "Person",
		Role:        RoleAdmin, // elevated later; ResolveRole would say student
		LastLoginAt: now.Add(-time.Minute),
	}}, now)

	got, err := svc.Provision(context.Background(), testIdentity())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if st.transactions != 0 {
		t.Errorf("opened %d transactions, want 0 — an elevated role is the correct stored state, not drift", st.transactions)
	}
	if got.Role != RoleAdmin {
		t.Errorf("Role = %q, want admin — the stored role must win over the first-login resolution", got.Role)
	}
}

// last_login_at still converges: once the row is stale enough, one write happens.
func TestProvision_StaleLastLoginIsRefreshedOnceTheWindowPasses(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	base := Current{ID: "user-1", Email: "person@amrita.edu", Name: "Person", Role: RoleStudent}

	justInside := base
	justInside.LastLoginAt = now.Add(-LoginTouchInterval + time.Second)
	st := &countingTxStore{}
	if _, err := newTestService(st, stubCurrentReader{current: justInside}, now).
		Provision(context.Background(), testIdentity()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if st.transactions != 0 {
		t.Errorf("inside the window: opened %d transactions, want 0", st.transactions)
	}

	past := base
	past.LastLoginAt = now.Add(-LoginTouchInterval - time.Second)
	st = &countingTxStore{}
	if _, err := newTestService(st, stubCurrentReader{current: past}, now).
		Provision(context.Background(), testIdentity()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if st.transactions != 1 {
		t.Errorf("past the window: opened %d transactions, want 1 — last_login_at must still converge", st.transactions)
	}
}

// A failing read must NOT quietly fall through to the write path: that would
// restore the expensive behaviour invisibly, which is the worst outcome.
func TestProvision_ReadFailureSurfacesRatherThanFallingBackToTheWrite(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	st := &countingTxStore{}
	boom := errors.New("connection reset")
	svc := newTestService(st, stubCurrentReader{err: boom}, now)

	if _, err := svc.Provision(context.Background(), testIdentity()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read failure surfaced", err)
	}
	if st.transactions != 0 {
		t.Errorf("opened %d transactions, want 0 — a read blip must not silently become a write", st.transactions)
	}
}

// Without a reader the service must behave EXACTLY as it did before, so every
// existing caller and test keeps its semantics.
func TestProvision_WithoutCurrentReaderAlwaysWrites(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	st := &countingTxStore{}
	svc := newTestService(st, nil, now)

	if _, err := svc.Provision(context.Background(), testIdentity()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if st.transactions != 1 {
		t.Errorf("opened %d transactions, want 1 — the opt-in setter must not change default behaviour", st.transactions)
	}
}
