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
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const tokenInvalidMessage = "Authentication token is invalid."

// jwksMinRefreshInterval rate-limits JWKS refetches so a burst of tokens
// carrying an unknown kid (or garbage) can't hammer the Supabase JWKS endpoint.
const jwksMinRefreshInterval = time.Minute

// VerifiedIdentity is the subset of Supabase JWT identity claims the app trusts.
// SessionID (the Supabase `session_id` claim) keys a scope=session revoke.
//
// AuthenticatedAt is the anchor a scope=all revoke compares against: the moment
// the user actually SIGNED IN for this session, taken from the earliest `amr`
// timestamp. It is deliberately not IssuedAt. Supabase re-mints the access token
// on every refresh with a fresh `iat` but the same session, so comparing a
// forced logout against `iat` let a revoked session back in after one silent
// refresh — the web client refreshes on the very 401 the revoke produced. `amr`
// is carried over unchanged by a refresh and moves only on a real sign-in.
//
// IssuedAt (`iat`) is still required and kept: it is the fallback anchor for a
// token with no usable `amr`, and the zero-value guard in RevocationAnchor.
type VerifiedIdentity struct {
	Email           string
	Name            string
	Subject         string
	SessionID       string
	IssuedAt        time.Time
	AuthenticatedAt time.Time
}

// RevocationAnchor is the instant a scope=all revocation is compared against:
// AuthenticatedAt when the verifier set it, IssuedAt otherwise.
//
// The fallback exists for identities built by hand (test fakes, the local
// portal harness) that predate AuthenticatedAt. A zero anchor would compare as
// the beginning of time and be "revoked" by every historical scope=all row, so
// a zero AuthenticatedAt must never reach the deny-list query.
func (id VerifiedIdentity) RevocationAnchor() time.Time {
	if !id.AuthenticatedAt.IsZero() {
		return id.AuthenticatedAt
	}
	return id.IssuedAt
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
	// providers is the lowercased allowlist of app_metadata.provider values
	// (AUTH_ALLOWED_PROVIDERS). Empty means NOTHING is accepted: a verifier that
	// was built without an allowlist fails closed rather than open.
	providers map[string]struct{}

	mu        sync.RWMutex
	keys      map[string]*ecdsa.PublicKey
	lastFetch time.Time
}

// NewVerifier builds a verifier and attempts one JWKS fetch. A fetch failure is
// logged, not fatal: the process still boots (keeping /health up) and the keys
// are populated on the first successful lazy refresh.
//
// allowedProviders is the set of Supabase identity providers accepted (matched
// case-insensitively against app_metadata.provider). An empty set rejects every
// token.
func NewVerifier(jwksURL, issuer, audience string, allowedProviders []string) (*Verifier, error) {
	providers := make(map[string]struct{}, len(allowedProviders))
	for _, p := range allowedProviders {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			providers[p] = struct{}{}
		}
	}
	v := &Verifier{
		jwksURL:   jwksURL,
		issuer:    issuer,
		audience:  audience,
		client:    &http.Client{Timeout: 10 * time.Second},
		providers: providers,
		keys:      map[string]*ecdsa.PublicKey{},
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
		// One minute of clock leeway on exp/nbf/iat.
		//
		// The token is minted by Supabase and checked here, on a Fly machine, and
		// nothing keeps those two clocks in step. Without leeway a token that the
		// client believes has ~0s left is rejected outright, and the client reads
		// that 401 as "signed out" — mid-mark, on a phone, for a session that is
		// in fact still valid. A minute is the usual allowance and is far shorter
		// than the token's own lifetime, so it widens no real window.
		jwt.WithLeeway(1*time.Minute),
	)
	if err != nil || !parsed.Valid {
		return VerifiedIdentity{}, errors.New("jwt validation failed")
	}
	if claims.Email == "" || claims.Subject == "" {
		return VerifiedIdentity{}, errors.New("missing identity claims")
	}
	// session_id is REQUIRED: without it the revocation guard
	// cannot distinguish one session from another, so a token that omits it is
	// treated as invalid rather than silently carrying a blank SessionID.
	if claims.SessionID == "" {
		return VerifiedIdentity{}, errors.New("missing session_id claim")
	}
	// session_id must be a real UUID: the revocation checker keys a scope=session
	// deny-list row on a uuid column, so a non-UUID value could never match and
	// would silently escape a session-scoped revoke. Reject it here rather than
	// let it through as an unrevokable session.
	if _, err := uuid.Parse(claims.SessionID); err != nil {
		return VerifiedIdentity{}, errors.New("session_id is not a valid uuid")
	}
	// The identity must come from an allowlisted provider. app_metadata is
	// written only by Supabase Auth (a user cannot edit it), so this is the
	// trustworthy answer to "how did this person sign in?".
	if _, ok := v.providers[claims.provider()]; !ok {
		return VerifiedIdentity{}, errors.New("identity provider not allowed")
	}
	// FAIL CLOSED: the email is trusted only on an explicit email_verified:true.
	// See emailVerified for where that verdict may come from.
	if !claims.emailVerified() {
		return VerifiedIdentity{}, errors.New("email not verified")
	}
	name, _ := claims.UserMetadata["name"].(string)
	// iat is load-bearing for revocation: a scope=all deny-list row rejects tokens
	// issued at/before revoked_after. A missing/zero iat would compare as the zero
	// time and appear revoked by ANY historical scope=all row, so require it.
	if claims.IssuedAt == nil || claims.IssuedAt.Time.IsZero() {
		return VerifiedIdentity{}, errors.New("missing iat claim")
	}
	issuedAt := claims.IssuedAt.Time
	// Never LATER than iat: a token cannot have been issued before its session
	// authenticated, so an amr timestamp past iat is a clock or provider oddity,
	// and honouring it would only make a forced logout easier to escape.
	authenticatedAt, ok := claims.authenticatedAt()
	if !ok || authenticatedAt.After(issuedAt) {
		authenticatedAt = issuedAt
	}
	return VerifiedIdentity{
		Email:           claims.Email,
		Name:            name,
		Subject:         claims.Subject,
		SessionID:       claims.SessionID,
		IssuedAt:        issuedAt,
		AuthenticatedAt: authenticatedAt,
	}, nil
}

type supabaseClaims struct {
	jwt.RegisteredClaims
	Email         string         `json:"email"`
	EmailVerified *bool          `json:"email_verified"`
	UserMetadata  map[string]any `json:"user_metadata"`
	AppMetadata   map[string]any `json:"app_metadata"`
	SessionID     string         `json:"session_id"`
	// AMR is kept raw and parsed leniently in authenticatedAt: Supabase sends an
	// array of {method, timestamp} objects, but RFC 8176 allows plain strings,
	// and a shape this code does not expect must degrade to the iat fallback
	// rather than fail the whole token.
	AMR json.RawMessage `json:"amr"`
}

// emailVerified is true ONLY for an explicit boolean true. A top-level
// email_verified claim (set by a Supabase custom access token hook) wins when
// present. Otherwise the provider's verdict that Supabase copies into
// user_metadata.email_verified is used, because standard Supabase access tokens
// carry it only there. Absent everywhere, false, or a non-boolean is unverified:
// this fails closed.
//
// Residual risk, recorded rather than hidden: a signed-in user can edit their
// own user_metadata. Closing that needs the top-level claim from a custom access
// token hook; once every token carries it, this fallback can be removed.
func (c *supabaseClaims) emailVerified() bool {
	if c.EmailVerified != nil {
		return *c.EmailVerified
	}
	v, ok := c.UserMetadata["email_verified"].(bool)
	return ok && v
}

// provider is the lowercased app_metadata.provider, or "" when it is absent or
// not a string (which no allowlist entry can match).
func (c *supabaseClaims) provider() string {
	p, _ := c.AppMetadata["provider"].(string)
	return strings.ToLower(strings.TrimSpace(p))
}

// authenticatedAt returns the earliest positive `amr` timestamp: when the user
// first authenticated in this session. ok is false when amr is absent, is not
// an array of objects, or carries no usable timestamp — the caller then falls
// back to iat.
//
// EARLIEST, not latest: a session can gain a second method later (an MFA
// step-up adds an entry), and a step-up must not reset the session's sign-in
// time past a forced logout that was meant to end it.
func (c *supabaseClaims) authenticatedAt() (time.Time, bool) {
	if len(c.AMR) == 0 {
		return time.Time{}, false
	}
	var entries []struct {
		Method    string   `json:"method"`
		Timestamp *float64 `json:"timestamp"`
	}
	if err := json.Unmarshal(c.AMR, &entries); err != nil {
		return time.Time{}, false
	}
	var earliest int64
	for _, entry := range entries {
		if entry.Timestamp == nil || *entry.Timestamp <= 0 {
			continue
		}
		ts := int64(*entry.Timestamp)
		if earliest == 0 || ts < earliest {
			earliest = ts
		}
	}
	if earliest == 0 {
		return time.Time{}, false
	}
	return time.Unix(earliest, 0), true
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
