// domain_test.go — unit tests for the @amrita.edu
// domain-FAMILY gate, enforced in OUR Go code (defense-in-depth behind the
// Supabase before-user-created hook). EnforceDomain is the pure seam the auth
// middleware calls after a token verifies; testing it in isolation pins the
// exact family rule and the exact rejection message.
package auth

import (
	"errors"
	"testing"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// rejectMessage is the exact, verbatim message NFR52/UX-DR13 require — "not a
// generic 401".
const rejectMessage = "Only @amrita.edu accounts can access this system."

// TestEnforceDomain pins the family rule: accept iff host == "amrita.edu" OR
// host ends in ".amrita.edu" at a label boundary, case-insensitive. This must
// match the Supabase hook's `host = 'amrita.edu' or host like '%.amrita.edu'`.
func TestEnforceDomain(t *testing.T) {
	const allowed = "amrita.edu"
	cases := []struct {
		email string
		ok    bool
	}{
		// accepted — the amrita.edu family (staff bare domain + student subdomains)
		{"staff@amrita.edu", true},
		{"student@cb.students.amrita.edu", true},
		{"x@bl.students.amrita.edu", true},
		{"person@sub.deep.amrita.edu", true},
		{"Mixed.Case@AMRITA.EDU", true}, // host compared case-insensitively

		// rejected — lookalikes, wrong domains, malformed
		{"attacker@amrita.edu.evil.com", false}, // ends in .evil.com, NOT .amrita.edu
		{"attacker@notamrita.edu", false},       // label boundary: no leading dot
		{"attacker@evilamrita.edu", false},
		{"attacker@xamrita.edu", false},
		{"nobody@gmail.com", false},
		{"noatsign", false},
		{"", false},
		{"a@evil.com@amrita.edu", false}, // multiple '@' is malformed — not the last-label host
		{"a@b@amrita.edu", false},
	}
	for _, tc := range cases {
		err := EnforceDomain(tc.email, allowed)
		switch {
		case tc.ok && err != nil:
			t.Errorf("[P0] EnforceDomain(%q) = %v, want ALLOWED", tc.email, err)
		case !tc.ok && err == nil:
			t.Errorf("[P0] EnforceDomain(%q) = nil, want REJECTED", tc.email)
		}
	}
}

// TestEnforceDomain_RejectionShape — a rejection is a typed
// domain.DomainError{AUTH_DOMAIN_REJECTED} carrying the EXACT AC message (403,
// distinct from the token-invalid 401). Asserted on typed fields, never a
// substring of the error string.
func TestEnforceDomain_RejectionShape(t *testing.T) {
	err := EnforceDomain("nobody@gmail.com", "amrita.edu")
	if err == nil {
		t.Fatal("[P0] expected a rejection for a non-amrita address")
	}
	var de domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("[P0] rejection must be a domain.DomainError, got %T", err)
	}
	if de.Code != contracts.ErrorCodeAUTHDOMAINREJECTED {
		t.Errorf("[P0] want AUTH_DOMAIN_REJECTED, got %q", string(de.Code))
	}
	if de.Message != rejectMessage {
		t.Errorf("[P0] exact message required by the AC.\n want: %q\n  got: %q", rejectMessage, de.Message)
	}
}
