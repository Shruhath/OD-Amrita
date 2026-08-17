package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type countingRevocationChecker struct {
	mu      sync.Mutex
	calls   int
	revoked bool
	err     error
}

func (c *countingRevocationChecker) IsRevoked(context.Context, VerifiedIdentity) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.revoked, c.err
}

func (c *countingRevocationChecker) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func testToken() VerifiedIdentity {
	return VerifiedIdentity{
		Subject:   "sub-1",
		SessionID: "sess-1",
		IssuedAt:  time.Unix(1_700_000_000, 0),
	}
}

// The most-executed query in the database: 15,766 calls against 319 marks, on a
// deny-list that is empty almost always.
func TestCachedRevocationChecker_RepeatedChecksHitTheDatabaseOnce(t *testing.T) {
	inner := &countingRevocationChecker{}
	checker := NewCachedRevocationChecker(inner)

	for range 50 {
		revoked, err := checker.IsRevoked(context.Background(), testToken())
		if err != nil {
			t.Fatalf("IsRevoked: %v", err)
		}
		if revoked {
			t.Fatal("want not revoked")
		}
	}

	if got := inner.count(); got != 1 {
		t.Errorf("hit the database %d times, want 1", got)
	}
}

// The key must identify one TOKEN, not one user: the deny-list matches on
// session id and issue time as well as subject, so a verdict computed for one
// token must never be served for another it would judge differently.
func TestCachedRevocationChecker_KeyDistinguishesSessionAndIssueTime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mutza func(VerifiedIdentity) VerifiedIdentity
	}{
		{"different subject", func(id VerifiedIdentity) VerifiedIdentity { id.Subject = "sub-2"; return id }},
		{"different session", func(id VerifiedIdentity) VerifiedIdentity { id.SessionID = "sess-2"; return id }},
		{"different issue time", func(id VerifiedIdentity) VerifiedIdentity {
			id.IssuedAt = id.IssuedAt.Add(time.Hour)
			return id
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &countingRevocationChecker{}
			checker := NewCachedRevocationChecker(inner)

			_, _ = checker.IsRevoked(context.Background(), testToken())
			_, _ = checker.IsRevoked(context.Background(), tc.mutza(testToken()))

			if got := inner.count(); got != 2 {
				t.Errorf("hit the database %d times, want 2 — these are different tokens", got)
			}
		})
	}
}

// The guard fails closed, so a cached error would refuse every request from a
// caller for the whole window over one transient blip.
func TestCachedRevocationChecker_DoesNotCacheErrors(t *testing.T) {
	inner := &countingRevocationChecker{err: errors.New("connection reset")}
	checker := NewCachedRevocationChecker(inner)

	if _, err := checker.IsRevoked(context.Background(), testToken()); err == nil {
		t.Fatal("want the error surfaced")
	}

	inner.mu.Lock()
	inner.err = nil
	inner.mu.Unlock()

	if _, err := checker.IsRevoked(context.Background(), testToken()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := inner.count(); got != 2 {
		t.Errorf("hit the database %d times, want 2 — a failure must be retried", got)
	}
}

// Clear is what keeps a forced logout immediate on the machine that took it.
func TestCachedRevocationChecker_ClearForcesAReload(t *testing.T) {
	inner := &countingRevocationChecker{}
	checker := NewCachedRevocationChecker(inner)

	if _, err := checker.IsRevoked(context.Background(), testToken()); err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}

	// An administrator revokes; the cache is dropped.
	inner.mu.Lock()
	inner.revoked = true
	inner.mu.Unlock()
	checker.Clear()

	revoked, err := checker.IsRevoked(context.Background(), testToken())
	if err != nil {
		t.Fatalf("IsRevoked after clear: %v", err)
	}
	if !revoked {
		t.Error("want revoked — a cleared cache must re-read the deny-list, not serve the stale allow")
	}
}

func TestCachedRevocationChecker_ConcurrentMissesCollapse(t *testing.T) {
	inner := &countingRevocationChecker{}
	checker := NewCachedRevocationChecker(inner)

	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = checker.IsRevoked(context.Background(), testToken())
		}()
	}
	wg.Wait()

	if got := inner.count(); got != 1 {
		t.Errorf("hit the database %d times under concurrency, want 1", got)
	}
}
