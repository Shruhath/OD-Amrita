// verifier_hardening_test.go — regression coverage for the code-review hardening
// on top of the Story 1.5a verifier: expiry is REQUIRED (not merely validated
// when present), an explicitly-unverified email is rejected, and a rotated
// Supabase signing key is picked up by a lazy JWKS refetch (no restart).
//
// These reuse the stdlib ES256 harness in verifier_test.go (same package) and
// add a raw claims signer so a token can OMIT exp or carry email_verified.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signRaw ES256-signs an arbitrary claims map under key, stamping headerKid into
// the JWT header — full control over which claims are present (exp, email_verified).
func signRaw(t *testing.T, key es256Key, headerKid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": "ES256", "typ": "JWT", "kid": headerKid}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := b64url(hb) + "." + b64url(cb)
	sum := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key.priv, sum[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	sig := append(fixedBytes(r, 32), fixedBytes(s, 32)...)
	return signingInput + "." + b64url(sig)
}

func baseClaims() map[string]any {
	return map[string]any{
		"iss":           testIssuer,
		"aud":           testAudience,
		"sub":           "user-uuid-123",
		"email":         "staff@amrita.edu",
		"session_id":    "11111111-1111-1111-1111-111111111111",
		"exp":           time.Now().Add(time.Hour).Unix(),
		"user_metadata": map[string]any{"name": "Staff Member"},
	}
}

// TestVerify_RejectsTokenWithoutExp — a validly-signed token that OMITS exp is
// rejected (WithExpirationRequired), so a session can never be non-expiring.
func TestVerify_RejectsTokenWithoutExp(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	claims := baseClaims()
	delete(claims, "exp")

	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, claims)); err == nil {
		t.Error("[P0] a token with no exp must be rejected, got nil error")
	}
}

// TestVerify_RejectsExplicitlyUnverifiedEmail — email_verified:false is rejected
// (top-level and via user_metadata), while an ABSENT claim stays permissive so
// providers that omit it don't break real logins.
func TestVerify_RejectsExplicitlyUnverifiedEmail(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	topLevel := baseClaims()
	topLevel["email_verified"] = false
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, topLevel)); err == nil {
		t.Error("[P0] top-level email_verified:false must be rejected")
	}

	inMeta := baseClaims()
	inMeta["user_metadata"] = map[string]any{"name": "Staff Member", "email_verified": false}
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, inMeta)); err == nil {
		t.Error("[P0] user_metadata.email_verified:false must be rejected")
	}

	verified := baseClaims()
	verified["email_verified"] = true
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, verified)); err != nil {
		t.Errorf("[P0] email_verified:true must be accepted, got %v", err)
	}
}

// TestVerify_RotatedKeyIsRefetched — after the JWKS rotates to a new signing key,
// a token minted under the new kid verifies via a lazy refetch (D1: no restart).
func TestVerify_RotatedKeyIsRefetched(t *testing.T) {
	oldKey := newES256Key(t, "kid-old")
	newKey := newES256Key(t, "kid-new")

	// A server whose served JWKS can be swapped to simulate Supabase rotation.
	current := jwksJSON(t, oldKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(current)
	}))
	t.Cleanup(srv.Close)

	v, err := NewVerifier(srv.URL, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Rotate: the endpoint now serves only the new key. Clear the refresh
	// cooldown stamped by the initial fetch so the unknown-kid miss refetches.
	current = jwksJSON(t, newKey)
	v.mu.Lock()
	v.lastFetch = time.Time{}
	v.mu.Unlock()

	token := signRaw(t, newKey, newKey.kid, baseClaims())
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Errorf("[P0] rotated-key token must verify after lazy JWKS refetch, got %v", err)
	}
}
