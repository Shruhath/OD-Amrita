// session_revoked_test.go — Story 1.5c (DoD 9): the settled forced-logout audit
// action. UNIT test, no deps.
//
// The name is SETTLED: Story 1.7 owns the broader auth-event audit family and
// MUST reuse "auth.session_revoked" — it must not add a duplicate forced-logout
// event. This test pins the wire string so a later drift fails loudly.
package domain

import "testing"

func TestActionAuthSessionRevoked_SettledNameAndRegistered(t *testing.T) {
	// [P0] Settled wire name — do not rename (Story 1.7 reuses it).
	if got := string(ActionAuthSessionRevoked); got != "auth.session_revoked" {
		t.Errorf("[P0] settled event name: want auth.session_revoked, got %q", got)
	}

	// [P0] Registered() is what store.WithAudit gates on — an unregistered action
	// is rejected before any write, so the constant MUST be in the registry.
	if !ActionAuthSessionRevoked.Registered() {
		t.Error("[P0] ActionAuthSessionRevoked must be registered (added to AllActions)")
	}

	// [P0] Belt-and-braces: it appears in the canonical AllActions slice exactly
	// once. events_test.go already fails if a declared constant is missing here,
	// but the revocation story asserts its own action explicitly.
	count := 0
	for _, a := range AllActions {
		if a == ActionAuthSessionRevoked {
			count++
		}
	}
	if count != 1 {
		t.Errorf("[P0] ActionAuthSessionRevoked must be in AllActions exactly once, found %d", count)
	}
}
