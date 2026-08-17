package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type recordingLoginAuditor struct {
	mu       sync.Mutex
	sessions []string
	err      error
}

func (r *recordingLoginAuditor) LoginSucceeded(_ context.Context, _, _, _, sessionID, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.sessions = append(r.sessions, sessionID)
	return nil
}

func (r *recordingLoginAuditor) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// The whole point: /auth/me is called on every token refresh, and each call used
// to append a login_succeeded row. In production that was ~79% of the audit
// ledger.
func TestThrottledLoginAuditor_WritesOncePerSession(t *testing.T) {
	inner := &recordingLoginAuditor{}
	auditor := NewThrottledLoginAuditor(inner)

	for range 20 {
		if err := auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", "sess-1", "ua"); err != nil {
			t.Fatalf("LoginSucceeded: %v", err)
		}
	}

	if got := inner.count(); got != 1 {
		t.Errorf("wrote %d audit rows for one session, want 1", got)
	}
}

// Distinct sessions are distinct logins and must each be recorded — the audit
// question "who signed in and when" has to stay answerable.
func TestThrottledLoginAuditor_DistinctSessionsEachGetARow(t *testing.T) {
	inner := &recordingLoginAuditor{}
	auditor := NewThrottledLoginAuditor(inner)

	for _, session := range []string{"sess-1", "sess-2", "sess-3"} {
		if err := auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", session, "ua"); err != nil {
			t.Fatalf("LoginSucceeded: %v", err)
		}
	}

	if got := inner.count(); got != 3 {
		t.Errorf("wrote %d audit rows for three sessions, want 3", got)
	}
}

// Two people must never share a suppression slot, even if a session id somehow
// repeated across subjects.
func TestThrottledLoginAuditor_KeyIncludesSubject(t *testing.T) {
	inner := &recordingLoginAuditor{}
	auditor := NewThrottledLoginAuditor(inner)

	_ = auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", "same", "ua")
	_ = auditor.LoginSucceeded(context.Background(), "b@amrita.edu", "student", "sub-2", "same", "ua")

	if got := inner.count(); got != 2 {
		t.Errorf("wrote %d audit rows for two subjects, want 2 — one person must not suppress another's audit", got)
	}
}

// FAIL-CLOSED IS PRESERVED. A failed write must not be remembered as success:
// the handler refuses to answer 200 for a login it could not record, and the
// next attempt has to reach the database again.
func TestThrottledLoginAuditor_DoesNotCacheFailures(t *testing.T) {
	inner := &recordingLoginAuditor{err: errors.New("audit insert failed")}
	auditor := NewThrottledLoginAuditor(inner)

	if err := auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", "sess-1", "ua"); err == nil {
		t.Fatal("want the audit failure surfaced")
	}

	// The write now succeeds; the retry must actually reach it rather than being
	// served a cached "already audited".
	inner.err = nil
	if err := auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", "sess-1", "ua"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := inner.count(); got != 1 {
		t.Errorf("wrote %d audit rows, want 1 — the retry after a failure must be attempted", got)
	}
}

// An absent session id cannot be deduplicated. Passing it straight through means
// an extra row, which is the recoverable direction of that error.
func TestThrottledLoginAuditor_EmptySessionIsNeverSuppressed(t *testing.T) {
	inner := &recordingLoginAuditor{}
	auditor := NewThrottledLoginAuditor(inner)

	for range 3 {
		_ = auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", "", "ua")
	}

	if got := inner.count(); got != 3 {
		t.Errorf("wrote %d audit rows, want 3 — an un-dedupable login must not be dropped", got)
	}
}

// Concurrent first calls for one session collapse onto a single write rather
// than racing into several.
func TestThrottledLoginAuditor_ConcurrentFirstCallsCollapse(t *testing.T) {
	inner := &recordingLoginAuditor{}
	auditor := NewThrottledLoginAuditor(inner)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = auditor.LoginSucceeded(context.Background(), "a@amrita.edu", "student", "sub-1", "sess-1", "ua")
		}()
	}
	wg.Wait()

	if got := inner.count(); got != 1 {
		t.Errorf("wrote %d audit rows under concurrency, want 1", got)
	}
}
