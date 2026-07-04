package domain

import "testing"

// TestActionUserProvisionedRegistered pins the user.provisioned audit action
// added by Story 1.5b: its wire value, its presence in the registry (so
// store.WithAudit accepts it), and that it never collides with the reserved
// auth.* actions (which Story 1.7 owns). The registry drift-guard in
// events_test.go additionally proves the constant is listed in AllActions.
func TestActionUserProvisionedRegistered(t *testing.T) {
	if ActionUserProvisioned != Action("user.provisioned") {
		t.Fatalf("ActionUserProvisioned wire value changed: %q", ActionUserProvisioned)
	}
	if !ActionUserProvisioned.Registered() {
		t.Fatal("ActionUserProvisioned must be present in AllActions")
	}
	for _, reserved := range []Action{ActionAuthLoginSucceeded, ActionAuthDomainRejected} {
		if reserved == ActionUserProvisioned {
			t.Fatalf("user provisioning must not reuse reserved auth audit action %q", reserved)
		}
	}
}
