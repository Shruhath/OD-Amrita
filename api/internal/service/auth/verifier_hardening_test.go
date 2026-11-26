// verifier_hardening_test.go — regression coverage for the code-review hardening
// on top of the base verifier: expiry is REQUIRED (not merely validated
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
		"iss":            testIssuer,
		"aud":            testAudience,
		"sub":            "user-uuid-123",
		"email":          "staff@amrita.edu",
		"session_id":     "11111111-1111-1111-1111-111111111111",
		"iat":            time.Now().Add(-time.Minute).Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
		"user_metadata":  map[string]any{"name": "Staff Member"},
		"email_verified": true,
		"app_metadata":   map[string]any{"provider": "azure", "providers": []string{"azure"}},
	}
}

// TestVerify_RejectsTokenWithoutIAT — iat is load-bearing for revocation (a
// scope=all deny-list row rejects tokens issued at/before revoked_after). A
// token that OMITS iat would compare as the zero time and appear revoked by any
// historical scope=all row, so a validly-signed token without iat is rejected.
func TestVerify_RejectsTokenWithoutIAT(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	claims := baseClaims()
	delete(claims, "iat")

	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, claims)); err == nil {
		t.Error("[P0] a token missing iat must be rejected (AUTH_TOKEN_INVALID), got nil error")
	}
}

// TestVerify_RejectsNonUUIDSessionID — session_id keys a scope=session deny-list
// row on a uuid column, so a non-UUID session_id could never match and would
// silently escape a session-scoped revoke. It is rejected at verification.
func TestVerify_RejectsNonUUIDSessionID(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	claims := baseClaims()
	claims["session_id"] = "not-a-uuid"

	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, claims)); err == nil {
		t.Error("[P0] a token with a non-UUID session_id must be rejected (AUTH_TOKEN_INVALID), got nil error")
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

// TestVerify_EmailVerificationFailsClosed — the email is trusted ONLY on an
// explicit email_verified:true. false is rejected, a claim ABSENT everywhere is
// rejected (it used to be waved through), a top-level verdict overrides
// user_metadata, and user_metadata.email_verified:true (where standard Supabase
// tokens carry the provider's verdict) is accepted when no top-level claim exists.
func TestVerify_EmailVerificationFailsClosed(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	explicitFalse := baseClaims()
	explicitFalse["email_verified"] = false
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, explicitFalse)); err == nil {
		t.Error("[P0] top-level email_verified:false must be rejected")
	}

	absent := baseClaims()
	delete(absent, "email_verified")
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, absent)); err == nil {
		t.Error("[P0] an ABSENT email_verified claim must be rejected (fail closed), got nil error")
	}

	metaTrue := baseClaims()
	delete(metaTrue, "email_verified")
	metaTrue["user_metadata"] = map[string]any{"name": "Staff Member", "email_verified": true}
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, metaTrue)); err != nil {
		t.Errorf("[P0] user_metadata.email_verified:true with no top-level claim must be accepted (standard Supabase token shape), got %v", err)
	}

	metaFalse := baseClaims()
	delete(metaFalse, "email_verified")
	metaFalse["user_metadata"] = map[string]any{"name": "Staff Member", "email_verified": false}
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, metaFalse)); err == nil {
		t.Error("[P0] user_metadata.email_verified:false must be rejected")
	}

	metaString := baseClaims()
	delete(metaString, "email_verified")
	metaString["user_metadata"] = map[string]any{"name": "Staff Member", "email_verified": "true"}
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, metaString)); err == nil {
		t.Error("[P0] a non-boolean user_metadata.email_verified must be rejected")
	}

	topLevelWins := baseClaims()
	topLevelWins["email_verified"] = false
	topLevelWins["user_metadata"] = map[string]any{"name": "Staff Member", "email_verified": true}
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, topLevelWins)); err == nil {
		t.Error("[P0] a top-level email_verified:false must override user_metadata")
	}

	nonBool := baseClaims()
	nonBool["email_verified"] = "true"
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, nonBool)); err == nil {
		t.Error("[P0] a non-boolean email_verified must be rejected")
	}

	verified := baseClaims()
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, verified)); err != nil {
		t.Errorf("[P0] email_verified:true from an allowed provider must be accepted, got %v", err)
	}
}

// TestVerify_ProviderAllowlist — app_metadata.provider (server-written, unlike
// user_metadata) must be in AUTH_ALLOWED_PROVIDERS. A wrong, missing or
// non-string provider is rejected; matching is case-insensitive.
func TestVerify_ProviderAllowlist(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	cases := []struct {
		name     string
		metadata any
		wantOK   bool
	}{
		{"allowed provider", map[string]any{"provider": "azure"}, true},
		{"allowed provider, different case", map[string]any{"provider": "Azure"}, true},
		{"wrong provider", map[string]any{"provider": "email"}, false},
		{"another oauth provider", map[string]any{"provider": "google"}, false},
		{"provider only in the providers list", map[string]any{"providers": []string{"azure"}}, false},
		{"provider not a string", map[string]any{"provider": true}, false},
		{"app_metadata absent", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := baseClaims()
			if tc.metadata == nil {
				delete(claims, "app_metadata")
			} else {
				claims["app_metadata"] = tc.metadata
			}
			_, err := v.Verify(context.Background(), signRaw(t, key, key.kid, claims))
			if tc.wantOK && err != nil {
				t.Errorf("[P0] want accepted, got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Error("[P0] want rejected (AUTH_TOKEN_INVALID), got nil error")
			}
		})
	}
}

// TestVerify_EmptyProviderAllowlistRejectsEverything — a verifier built without
// an allowlist must fail closed, not accept any provider.
func TestVerify_EmptyProviderAllowlistRejectsEverything(t *testing.T) {
	key := newES256Key(t, "kid-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON(t, key))
	}))
	t.Cleanup(srv.Close)
	v, err := NewVerifier(srv.URL, testIssuer, testAudience, nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := v.Verify(context.Background(), signRaw(t, key, key.kid, baseClaims())); err == nil {
		t.Error("[P0] an empty provider allowlist must reject every token")
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

	v, err := NewVerifier(srv.URL, testIssuer, testAudience, []string{"azure"})
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
