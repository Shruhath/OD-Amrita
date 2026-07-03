package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const tokenInvalidMessage = "Authentication token is invalid."

// VerifiedIdentity is the subset of Supabase JWT identity claims the app trusts.
type VerifiedIdentity struct {
	Email   string
	Name    string
	Subject string
}

// TokenVerifier verifies a bearer token and returns the trusted identity.
type TokenVerifier interface {
	Verify(context.Context, string) (VerifiedIdentity, error)
}

// Verifier verifies Supabase ES256 JWTs against a JWKS fetched at startup.
type Verifier struct {
	issuer   string
	audience string
	keys     map[string]*ecdsa.PublicKey
}

// NewVerifier fetches and caches the Supabase JWKS once at startup.
func NewVerifier(jwksURL, issuer, audience string) (*Verifier, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("auth verifier: build jwks request: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth verifier: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth verifier: fetch jwks: status %d", resp.StatusCode)
	}
	keys, err := parseJWKS(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("auth verifier: jwks contains no usable ES256 keys")
	}
	return &Verifier{issuer: issuer, audience: audience, keys: keys}, nil
}

func parseJWKS(r interface{ Read([]byte) (int, error) }) (map[string]*ecdsa.PublicKey, error) {
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
		xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil {
			return nil, fmt.Errorf("auth verifier: decode jwk x: %w", err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
		if err != nil {
			return nil, fmt.Errorf("auth verifier: decode jwk y: %w", err)
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !elliptic.P256().IsOnCurve(x, y) {
			return nil, errors.New("auth verifier: jwk point is not on P-256")
		}
		keys[jwk.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	}
	return keys, nil
}

// Verify validates ES256 signature, expiry, audience and issuer before returning
// the trusted identity. All failures collapse to AUTH_TOKEN_INVALID.
func (v *Verifier) Verify(_ context.Context, token string) (VerifiedIdentity, error) {
	id, err := v.verify(token)
	if err != nil {
		return VerifiedIdentity{}, invalidToken()
	}
	return id, nil
}

func (v *Verifier) verify(token string) (VerifiedIdentity, error) {
	claims := &supabaseClaims{}
	parsed, err := jwt.ParseWithClaims(
		token,
		claims,
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodES256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			kid, _ := t.Header["kid"].(string)
			key, ok := v.keys[kid]
			if !ok {
				return nil, errors.New("unknown kid")
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
	)
	if err != nil || !parsed.Valid {
		return VerifiedIdentity{}, errors.New("jwt validation failed")
	}
	name, _ := claims.UserMetadata["name"].(string)
	if claims.Email == "" || claims.Subject == "" {
		return VerifiedIdentity{}, errors.New("missing identity claims")
	}
	return VerifiedIdentity{Email: claims.Email, Name: name, Subject: claims.Subject}, nil
}

type supabaseClaims struct {
	jwt.RegisteredClaims
	Email        string         `json:"email"`
	UserMetadata map[string]any `json:"user_metadata"`
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
