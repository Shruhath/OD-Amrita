// Package auth — red-phase ATDD scaffold for Story 1.5a (DoD 2): the Supabase
// session-JWT verifier. UNIT test: no DB, no live Supabase. A stdlib-signed
// httptest JWKS server stands in for Supabase's
// /auth/v1/.well-known/jwks.json — so the ES256 verify path is exercised for
// real (genuine reject-on-bad-sig / expired / wrong-aud / wrong-iss), not
// mocked.
//
// RED PHASE: service/auth is still a Story 1.1 doc stub, so TokenVerifier,
// VerifiedIdentity, Verifier and NewVerifier do not exist yet — THIS PACKAGE
// FAILS TO COMPILE until Task 4 lands them. That compile error IS the intended
// red signal (the Go translation of test.skip()); it clears the moment the
// verifier is implemented. `go build ./...` (no test files) stays green.
//
// STDLIB-ONLY HARNESS ON PURPOSE: the ES256 signer and the JWKS document are
// built from crypto/ecdsa + encoding/json, so the ONLY undefined symbols are
// the auth.* ones under test — NOT a missing golang-jwt/keyfunc dependency
// (those libs are added by the implementation via pinned `go get`, per the
// go-mod-tidy trap in the story). This keeps the red signal about the code,
// not the module graph.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const (
	testIssuer   = "https://project-ref.supabase.co/auth/v1"
	testAudience = "authenticated"
)

// es256Key is a P-256 signing key plus the kid it is published under in the
// test JWKS.
type es256Key struct {
	priv *ecdsa.PrivateKey
	kid  string
}

func newES256Key(t *testing.T, kid string) es256Key {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	return es256Key{priv: priv, kid: kid}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// fixedBytes left-pads a big.Int to a fixed width. P-256 coordinates and the
// r/s halves of an ES256 signature are 32 bytes each.
func fixedBytes(n *big.Int, width int) []byte {
	b := n.Bytes()
	if len(b) >= width {
		return b
	}
	out := make([]byte, width)
	copy(out[width-len(b):], b)
	return out
}

// jwksJSON renders a JWKS document exposing the PUBLIC half of each key, in the
// shape Supabase publishes (EC / P-256, base64url X/Y, alg ES256).
func jwksJSON(t *testing.T, keys ...es256Key) []byte {
	t.Helper()
	type jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	for _, k := range keys {
		set.Keys = append(set.Keys, jwk{
			Kty: "EC", Crv: "P-256", Kid: k.kid, Alg: "ES256", Use: "sig",
			X: b64url(fixedBytes(k.priv.PublicKey.X, 32)),
			Y: b64url(fixedBytes(k.priv.PublicKey.Y, 32)),
		})
	}
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return raw
}

// tokenClaims are the Supabase session-JWT claims the verifier reads. name is
// carried under user_metadata (Supabase's shape).
type tokenClaims struct {
	iss   string
	aud   string
	sub   string
	email string
	name  string
	exp   time.Time
}

func validClaims() tokenClaims {
	return tokenClaims{
		iss:   testIssuer,
		aud:   testAudience,
		sub:   "user-uuid-123",
		email: "staff@amrita.edu",
		name:  "Staff Member",
		exp:   time.Now().Add(time.Hour),
	}
}

// signToken builds and ES256-signs a JWT for claims under signKey's private
// key, stamping headerKid into the JWT header. Splitting signKey from headerKid
// lets a token advertise a kid that is / isn't in the JWKS, or be signed by a
// key whose public half is NOT published (the bad-signature case).
func signToken(t *testing.T, signKey es256Key, headerKid string, c tokenClaims) string {
	t.Helper()
	header := map[string]string{"alg": "ES256", "typ": "JWT", "kid": headerKid}
	claims := map[string]any{
		"iss":           c.iss,
		"aud":           c.aud,
		"sub":           c.sub,
		"email":         c.email,
		"exp":           c.exp.Unix(),
		"user_metadata": map[string]any{"name": c.name},
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

// newVerifierWithJWKS spins an httptest server serving jwks and constructs the
// verifier against it, keyed to the given issuer/audience.
func newVerifierWithJWKS(t *testing.T, jwks []byte, issuer, audience string) *Verifier {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	t.Cleanup(srv.Close)

	v, err := NewVerifier(srv.URL, issuer, audience)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// TestVerify_ValidToken — a correctly signed, in-audience, in-issuer, unexpired
// token yields the verified identity (email, name via user_metadata, subject).
func TestVerify_ValidToken(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	id, err := v.Verify(context.Background(), signToken(t, key, key.kid, validClaims()))
	if err != nil {
		t.Fatalf("[P0] valid token rejected: %v", err)
	}
	if id.Email != "staff@amrita.edu" {
		t.Errorf("[P0] email: want staff@amrita.edu, got %q", id.Email)
	}
	if id.Name != "Staff Member" {
		t.Errorf("[P0] name (from user_metadata.name): want Staff Member, got %q", id.Name)
	}
	if id.Subject != "user-uuid-123" {
		t.Errorf("[P0] subject: want user-uuid-123, got %q", id.Subject)
	}
}

// TestVerify_RejectsInvalidTokens — each genuine failure mode is rejected. The
// signature/issuer/audience/expiry checks are exercised against the real JWKS,
// not stubbed, so this proves the verifier actually validates rather than
// trusting the token.
func TestVerify_RejectsInvalidTokens(t *testing.T) {
	key := newES256Key(t, "kid-1")
	// Same kid, DIFFERENT private key whose public half is not in the JWKS ->
	// a signature that cannot verify against the published key.
	imposter := newES256Key(t, "kid-1")

	cases := []struct {
		name  string
		token func() string
	}{
		{"bad signature", func() string { return signToken(t, imposter, key.kid, validClaims()) }},
		{"expired", func() string {
			c := validClaims()
			c.exp = time.Now().Add(-time.Minute)
			return signToken(t, key, key.kid, c)
		}},
		{"wrong audience", func() string {
			c := validClaims()
			c.aud = "anon"
			return signToken(t, key, key.kid, c)
		}},
		{"wrong issuer", func() string {
			c := validClaims()
			c.iss = "https://evil.example/auth/v1"
			return signToken(t, key, key.kid, c)
		}},
		{"unknown kid", func() string { return signToken(t, key, "kid-not-in-jwks", validClaims()) }},
		{"malformed", func() string { return "not.a.jwt" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)
			if _, err := v.Verify(context.Background(), tc.token()); err == nil {
				t.Errorf("[P0] %s: expected verification to fail, got nil error", tc.name)
			}
		})
	}
}

// TestVerify_FailureIsAuthTokenInvalid — a verification failure is surfaced as
// a domain.DomainError{AUTH_TOKEN_INVALID}, never the raw jwt/jwks cause
// (DoD 2/7: no leak; the middleware renders this as a clean 401).
//
// References contracts.ErrorCodeAUTHTOKENINVALID, which does NOT exist until the
// contract adds AUTH_TOKEN_INVALID and is regenerated (Task 2) — a second,
// deliberate red lever alongside the undefined verifier symbols.
func TestVerify_FailureIsAuthTokenInvalid(t *testing.T) {
	key := newES256Key(t, "kid-1")
	v := newVerifierWithJWKS(t, jwksJSON(t, key), testIssuer, testAudience)

	expired := validClaims()
	expired.exp = time.Now().Add(-time.Minute)

	_, err := v.Verify(context.Background(), signToken(t, key, key.kid, expired))
	if err == nil {
		t.Fatal("[P0] expected an error for an expired token")
	}
	var de domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("[P0] verifier failure must be a domain.DomainError, got %T: %v", err, err)
	}
	if de.Code != contracts.ErrorCodeAUTHTOKENINVALID {
		t.Errorf("[P0] want code AUTH_TOKEN_INVALID, got %q", string(de.Code))
	}
}
