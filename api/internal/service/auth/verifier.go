package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const tokenInvalidMessage = "Authentication token is invalid."

// jwksMinRefreshInterval rate-limits JWKS refetches so a burst of tokens
// carrying an unknown kid (or garbage) can't hammer the Supabase JWKS endpoint.
const jwksMinRefreshInterval = time.Minute

// VerifiedIdentity is the subset of Supabase JWT identity claims the app trusts.
// SessionID (the Supabase `session_id` claim) and IssuedAt (`iat`) let the
// middleware/services tell one session apart from another and enforce revocation
// (Story 1.5c): SessionID keys a scope=session revoke; IssuedAt is the anchor a
// scope=all revoke compares against.
type VerifiedIdentity struct {
	Email     string
	Name      string
	Subject   string
	SessionID string
	IssuedAt  time.Time
}

// TokenVerifier verifies a bearer token and returns the trusted identity.
type TokenVerifier interface {
	Verify(context.Context, string) (VerifiedIdentity, error)
}

// Verifier verifies Supabase ES256 JWTs against a JWKS. Keys are cached and
// refreshed lazily on an unknown kid (rate-limited) so Supabase key rotation
// self-heals without a redeploy, and a JWKS outage at boot never blocks startup
// (protected routes fail closed until the keys become reachable).
type Verifier struct {
	jwksURL  string
	issuer   string
	audience string
	client   *http.Client

	mu        sync.RWMutex
	keys      map[string]*ecdsa.PublicKey
	lastFetch time.Time
}

// NewVerifier builds a verifier and attempts one JWKS fetch. A fetch failure is
// logged, not fatal: the process still boots (keeping /health up) and the keys
// are populated on the first successful lazy refresh.
func NewVerifier(jwksURL, issuer, audience string) (*Verifier, error) {
	v := &Verifier{
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
		client:   &http.Client{Timeout: 10 * time.Second},
		keys:     map[string]*ecdsa.PublicKey{},
	}
	if err := v.refresh(context.Background()); err != nil {
		log.Printf("auth verifier: initial JWKS fetch failed, will retry lazily: %v", err)
	}
	return v, nil
}

// refresh refetches the JWKS and swaps in the new key set. It is rate-limited:
// callers past the initial fetch only trigger a network call once the cooldown
// has elapsed, so an attacker replaying unknown-kid tokens can't amplify load.
func (v *Verifier) refresh(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.lastFetch.IsZero() && time.Since(v.lastFetch) < jwksMinRefreshInterval {
		return nil
	}
	keys, err := v.fetchJWKS(ctx)
	// Stamp the attempt regardless of outcome so failures are rate-limited too.
	v.lastFetch = time.Now()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("auth verifier: jwks contains no usable ES256 keys")
	}
	v.keys = keys
	return nil
}

func (v *Verifier) fetchJWKS(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("auth verifier: build jwks request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth verifier: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth verifier: fetch jwks: status %d", resp.StatusCode)
	}
	return parseJWKS(resp.Body)
}

// lookup returns the cached key for kid, refreshing once (rate-limited) on a
// miss so a freshly-rotated signing key is picked up without a restart.
func (v *Verifier) lookup(ctx context.Context, kid string) (*ecdsa.PublicKey, bool) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	v.mu.RUnlock()
	if ok {
		return key, true
	}
	if err := v.refresh(ctx); err != nil {
		return nil, false
	}
	v.mu.RLock()
	key, ok = v.keys[kid]
	v.mu.RUnlock()
	return key, ok
}

func parseJWKS(r io.Reader) (map[string]*ecdsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r).Decode(&set); err != nil {
		return nil, fmt.Errorf("auth verifier: decode jwks: %w", err)
	}
	keys := make(map[string]*ecdsa.PublicKey, len(set.Keys))
	for _, jwk := range set.Keys {
		if jwk.Kty != "EC" || jwk.Crv != "P-256" || jwk.Alg != "ES256" || jwk.Kid == "" {
			continue
		}
		// A single malformed key must not discard the whole set — skip it so a
		// usable ES256 key published alongside it (e.g. mid-rotation) still works.
		xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil {
			log.Printf("auth verifier: skipping jwk %q: decode x: %v", jwk.Kid, err)
			continue
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
		if err != nil {
			log.Printf("auth verifier: skipping jwk %q: decode y: %v", jwk.Kid, err)
			continue
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !elliptic.P256().IsOnCurve(x, y) {
			log.Printf("auth verifier: skipping jwk %q: point not on P-256", jwk.Kid)
			continue
		}
		keys[jwk.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	}
	return keys, nil
}

// Verify validates ES256 signature, expiry, audience and issuer before returning
// the trusted identity. All failures collapse to AUTH_TOKEN_INVALID.
func (v *Verifier) Verify(ctx context.Context, token string) (VerifiedIdentity, error) {
	id, err := v.verify(ctx, token)
	if err != nil {
		return VerifiedIdentity{}, invalidToken()
	}
	return id, nil
}

func (v *Verifier) verify(ctx context.Context, token string) (VerifiedIdentity, error) {
	claims := &supabaseClaims{}
	parsed, err := jwt.ParseWithClaims(
		token,
		claims,
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodES256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			kid, _ := t.Header["kid"].(string)
			key, ok := v.lookup(ctx, kid)
			if !ok {
				return nil, errors.New("unknown kid")
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsed.Valid {
		return VerifiedIdentity{}, errors.New("jwt validation failed")
	}
	if claims.Email == "" || claims.Subject == "" {
		return VerifiedIdentity{}, errors.New("missing identity claims")
	}
	// session_id is REQUIRED (Story 1.5c, DoD 2): without it the revocation guard
	// cannot distinguish one session from another, so a token that omits it is
	// treated as invalid rather than silently carrying a blank SessionID.
	if claims.SessionID == "" {
		return VerifiedIdentity{}, errors.New("missing session_id claim")
	}
	// Trust the email host only if the provider hasn't flagged it unverified.
	// Reject on an explicit false; absence stays permissive (Supabase omits the
	// claim for some providers) so real logins aren't broken.
	if !claims.emailVerified() {
		return VerifiedIdentity{}, errors.New("email not verified")
	}
	name, _ := claims.UserMetadata["name"].(string)
	var issuedAt time.Time
	if claims.IssuedAt != nil {
		issuedAt = claims.IssuedAt.Time
	}
	return VerifiedIdentity{
		Email:     claims.Email,
		Name:      name,
		Subject:   claims.Subject,
		SessionID: claims.SessionID,
		IssuedAt:  issuedAt,
	}, nil
}

type supabaseClaims struct {
	jwt.RegisteredClaims
	Email         string         `json:"email"`
	EmailVerified *bool          `json:"email_verified"`
	UserMetadata  map[string]any `json:"user_metadata"`
	SessionID     string         `json:"session_id"`
}

// emailVerified reports the provider's verification signal, treating only an
// explicit false (top-level or in user_metadata) as unverified.
func (c *supabaseClaims) emailVerified() bool {
	if c.EmailVerified != nil {
		return *c.EmailVerified
	}
	if v, ok := c.UserMetadata["email_verified"].(bool); ok {
		return v
	}
	return true
}

func invalidToken() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: tokenInvalidMessage}
}

// RejectingVerifier lets a development process boot without Supabase settings;
// every protected request still fails closed with AUTH_TOKEN_INVALID.
type RejectingVerifier struct{}

func (RejectingVerifier) Verify(context.Context, string) (VerifiedIdentity, error) {
	return VerifiedIdentity{}, invalidToken()
}
