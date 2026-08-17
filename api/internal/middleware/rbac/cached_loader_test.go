package rbac

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type countingLoader struct {
	mu        sync.Mutex
	calls     int
	principal Principal
	err       error
}

func (l *countingLoader) Load(context.Context, string) (Principal, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.principal, l.err
}

func (l *countingLoader) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// This read ran on every authenticated request — 13,571 calls in production
// against 319 actual marks — to answer a question that changes a handful of
// times per event.
func TestCachedPrincipalLoader_RepeatedLoadsHitTheDatabaseOnce(t *testing.T) {
	inner := &countingLoader{principal: Principal{UserID: "u1", Subject: "sub-1", Role: RoleAdmin}}
	loader := NewCachedPrincipalLoader(inner)

	for range 50 {
		got, err := loader.Load(context.Background(), "sub-1")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.Role != RoleAdmin {
			t.Fatalf("Role = %q, want admin", got.Role)
		}
	}

	if got := inner.count(); got != 1 {
		t.Errorf("hit the database %d times, want 1", got)
	}
}

// Two people must never share an entry.
func TestCachedPrincipalLoader_KeysOnSubject(t *testing.T) {
	inner := &countingLoader{principal: Principal{UserID: "u1", Role: RoleAdmin}}
	loader := NewCachedPrincipalLoader(inner)

	_, _ = loader.Load(context.Background(), "sub-1")
	_, _ = loader.Load(context.Background(), "sub-2")

	if got := inner.count(); got != 2 {
		t.Errorf("hit the database %d times for two subjects, want 2", got)
	}
}

// ErrPrincipalNotFound is how an unprovisioned caller is REFUSED. Caching it
// would keep refusing someone for the whole window after their first login had
// already provisioned them.
func TestCachedPrincipalLoader_DoesNotCacheNotFound(t *testing.T) {
	inner := &countingLoader{err: ErrPrincipalNotFound}
	loader := NewCachedPrincipalLoader(inner)

	if _, err := loader.Load(context.Background(), "sub-1"); !errors.Is(err, ErrPrincipalNotFound) {
		t.Fatalf("err = %v, want ErrPrincipalNotFound", err)
	}

	// Provisioning has now happened.
	inner.mu.Lock()
	inner.err = nil
	inner.principal = Principal{UserID: "u1", Role: "student"}
	inner.mu.Unlock()

	got, err := loader.Load(context.Background(), "sub-1")
	if err != nil {
		t.Fatalf("Load after provisioning: %v", err)
	}
	if got.UserID != "u1" {
		t.Errorf("UserID = %q, want the freshly provisioned principal, not a cached refusal", got.UserID)
	}
}

// Forget is what lets a just-changed role be seen without waiting out the TTL.
func TestCachedPrincipalLoader_ForgetReloads(t *testing.T) {
	inner := &countingLoader{principal: Principal{UserID: "u1", Role: "student"}}
	loader := NewCachedPrincipalLoader(inner)

	_, _ = loader.Load(context.Background(), "sub-1")
	loader.Forget("sub-1")
	_, _ = loader.Load(context.Background(), "sub-1")

	if got := inner.count(); got != 2 {
		t.Errorf("hit the database %d times, want 2 — Forget must force a reload", got)
	}
}

// Clear is the forced-logout path, where the affected subjects cannot be named.
func TestCachedPrincipalLoader_ClearReloadsEverySubject(t *testing.T) {
	inner := &countingLoader{principal: Principal{UserID: "u1", Role: "student"}}
	loader := NewCachedPrincipalLoader(inner)

	_, _ = loader.Load(context.Background(), "sub-1")
	_, _ = loader.Load(context.Background(), "sub-2")
	loader.Clear()
	_, _ = loader.Load(context.Background(), "sub-1")
	_, _ = loader.Load(context.Background(), "sub-2")

	if got := inner.count(); got != 4 {
		t.Errorf("hit the database %d times, want 4 — Clear must drop every entry", got)
	}
}

// A burst of concurrent requests for one subject — the polling-screen shape —
// must collapse onto one query rather than racing into many.
func TestCachedPrincipalLoader_ConcurrentMissesCollapse(t *testing.T) {
	inner := &countingLoader{principal: Principal{UserID: "u1", Role: RoleAdmin}}
	loader := NewCachedPrincipalLoader(inner)

	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = loader.Load(context.Background(), "sub-1")
		}()
	}
	wg.Wait()

	if got := inner.count(); got != 1 {
		t.Errorf("hit the database %d times under concurrency, want 1", got)
	}
}
