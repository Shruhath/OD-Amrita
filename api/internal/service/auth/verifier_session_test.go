// verifier_session_test.go — the verifier exposes the
// Supabase session identity (session_id, iat) so middleware/services can tell one
// session apart from another and enforce revocation. UNIT test, no DB.
//
// It REUSES the stdlib ES256 harness in verifier_test.go: newES256Key, jwksJSON,
// newVerifierWithJWKS, b64url, fixedBytes, es256Key, testIssuer, testAudience.
// Only a session-aware signer is added here.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"
)

// sessionClaims extends the Supabase session-JWT with the claims revocation
// needs: session_id (correlates to auth.sessions) and iat (issued-at, the anchor
// for "revoke all sessions issued before T"). withSessionID=false omits the
// claim entirely to exercise the production reject-on-missing path.
type sessionClaims struct {
	iss           string
	aud           string
	sub           string
	email         string
	sessionID     string
	withSessionID bool
	iat           time.Time
	exp           time.Time
}

func validSessionClaims() sessionClaims {
	now := time.Now()
	return sessionClaims{
		iss:           testIssuer,
		aud:           testAudience,
		sub:           "user-uuid-123",
		email:         "staff@amrita.edu",
		sessionID:     "11111111-1111-1111-1111-111111111111",
		withSessionID: true,
		iat:           now.Add(-time.Minute).Truncate(time.Second),
		exp:           now.Add(time.Hour),
	}
}

// signSessionToken ES256-signs a JWT carrying session_id + iat, reusing the
// published-key harness from verifier_test.go.
func signSessionToken(t *testing.T, signKey es256Key, headerKid string, c sessionClaims) string {
	t.Helper()
	header := map[string]string{"alg": "ES256", "typ": "JWT", "kid": headerKid}
	claims := map[string]any{
		"iss":            c.iss,
		"aud":            c.aud,
		"sub":            c.sub,
		"email":          c.email,
		"iat":            c.iat.Unix(),
		"exp":            c.exp.Unix(),
		"email_verified": true,
		"user_metadata":  map[string]any{"name": "Staff Member"},
		"app_metadata":   map[string]any{"provider": "azure", "providers": []string{"azure"}},
	}
	if c.withSessionID {
		claims["session_id"] = c.sessionID
	}
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
	r, s, err := ecdsa.Sign(rand.Reader, signKey.priv, sum[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	sig := append(fixedBytes(r, 32), fixedBytes(s, 32)...)
	return signingInput + "." + b64url(sig)
}

// TestVerify_ExposesSessionIdentity — a valid Supabase JWT yields SessionID and
// IssuedAt on the trusted identity (DoD 2). These are the inputs the revocation
// check keys on: session_id for scope=session, iat for scope=all.
func TestVerify_ExposesSessionIdentity(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	c := validSessionClaims()
	id, err := v.Verify(context.Background(), signSessionToken(t, key, key.kid, c))
	if err != nil {
		t.Fatalf("[P0] valid session token rejected: %v", err)
	}
	if id.SessionID != c.sessionID {
		t.Errorf("[P0] SessionID: want %q, got %q", c.sessionID, id.SessionID)
	}
	if id.IssuedAt.Unix() != c.iat.Unix() {
		t.Errorf("[P0] IssuedAt: want unix %d, got unix %d", c.iat.Unix(), id.IssuedAt.Unix())
	}
}

// TestVerify_RejectsTokenMissingSessionID — production verification MUST reject a
// token with no session_id claim (DoD 2). Without it the revocation check cannot
// distinguish sessions, so a missing claim is treated as an invalid token, not a
// silent blank SessionID.
func TestVerify_RejectsTokenMissingSessionID(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	c := validSessionClaims()
	c.withSessionID = false

	if _, err := v.Verify(context.Background(), signSessionToken(t, key, key.kid, c)); err == nil {
		t.Error("[P0] a token missing session_id must be rejected (AUTH_TOKEN_INVALID), got nil error")
	}
}

// signWithAMR signs baseClaims (verifier_hardening_test.go) carrying the given
// amr value and iat; amr=nil omits the claim.
func signWithAMR(t *testing.T, key es256Key, iat time.Time, amr any) string {
	t.Helper()
	claims := baseClaims()
	claims["iat"] = iat.Unix()
	if amr != nil {
		claims["amr"] = amr
	}
	return signRaw(t, key, key.kid, claims)
}

// TestVerify_AuthenticatedAtComesFromAMR — the forced-logout anchor is the
// session's sign-in time. Supabase refreshes re-mint iat but carry amr through,
// so a refreshed token must still report the ORIGINAL sign-in.
func TestVerify_AuthenticatedAtComesFromAMR(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	iat := time.Now().Add(-time.Minute).Truncate(time.Second)
	signedIn := iat.Add(-50 * time.Minute)
	mfa := iat.Add(-10 * time.Minute)

	cases := []struct {
		name string
		amr  any
		want time.Time
	}{
		{"single oauth entry", []map[string]any{{"method": "oauth", "timestamp": signedIn.Unix()}}, signedIn},
		{"earliest of several entries wins", []map[string]any{
			{"method": "totp", "timestamp": mfa.Unix()},
			{"method": "oauth", "timestamp": signedIn.Unix()},
		}, signedIn},
		{"amr absent falls back to iat", nil, iat},
		{"amr as RFC 8176 strings falls back to iat", []string{"pwd", "mfa"}, iat},
		{"amr not an array falls back to iat", map[string]any{"method": "oauth"}, iat},
		{"entries without timestamps fall back to iat", []map[string]any{{"method": "oauth"}}, iat},
		{"zero/negative timestamps are ignored", []map[string]any{
			{"method": "oauth", "timestamp": 0},
			{"method": "otp", "timestamp": -5},
		}, iat},
		{"a timestamp after iat is clamped to iat", []map[string]any{{"method": "oauth", "timestamp": iat.Add(time.Hour).Unix()}}, iat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := v.Verify(context.Background(), signWithAMR(t, key, iat, tc.amr))
			if err != nil {
				t.Fatalf("[P0] token rejected: %v", err)
			}
			if !id.AuthenticatedAt.Equal(tc.want) {
				t.Errorf("[P0] AuthenticatedAt = %v, want %v", id.AuthenticatedAt, tc.want)
			}
			if !id.RevocationAnchor().Equal(tc.want) {
				t.Errorf("[P0] RevocationAnchor = %v, want %v", id.RevocationAnchor(), tc.want)
			}
			if !id.IssuedAt.Equal(iat) {
				t.Errorf("[P1] IssuedAt = %v, want %v (iat is still reported)", id.IssuedAt, iat)
			}
		})
	}
}

// TestRevocationAnchor_FallsBackToIssuedAt — hand-built identities (fakes, the
// local portal harness) carry no AuthenticatedAt; a zero anchor would read as
// "signed in at the beginning of time" and be revoked by any scope=all row.
func TestRevocationAnchor_FallsBackToIssuedAt(t *testing.T) {
	iat := time.Unix(1_700_000_000, 0)
	if got := (VerifiedIdentity{IssuedAt: iat}).RevocationAnchor(); !got.Equal(iat) {
		t.Errorf("[P0] zero AuthenticatedAt must fall back to IssuedAt, got %v", got)
	}
	signedIn := iat.Add(-time.Hour)
	if got := (VerifiedIdentity{IssuedAt: iat, AuthenticatedAt: signedIn}).RevocationAnchor(); !got.Equal(signedIn) {
		t.Errorf("[P0] AuthenticatedAt must win when set, got %v", got)
	}
}
