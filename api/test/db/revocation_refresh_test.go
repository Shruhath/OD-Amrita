//go:build integration

package db

// A forced logout must survive a token refresh.
//
// Supabase re-mints the access token on every refresh: a NEW iat, the SAME
// session. The web client refreshes on the very 401 a revoke produces and
// retries. The deny-list used to compare a scope=all revoke against iat, so the
// refreshed token — issued after the revoke — was let straight back in.
//
// This drives the real chain end to end: a real ES256 Supabase-shaped token,
// the real Verifier (amr parsing), the auth middleware, the revocation guard,
// the cached checker and the sqlc IsTokenRevoked query against Postgres. Only
// the JWKS endpoint is local.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

const (
	refreshTestIssuer   = "https://auth.test/auth/v1"
	refreshTestAudience = "authenticated"
	refreshTestKid      = "refresh-test-kid"
)

type nopDomainAuditor struct{}

func (nopDomainAuditor) DomainRejected(context.Context, string, string, string) error { return nil }

func TestForcedLogout_SurvivesTokenRefresh(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	adminID := seedAdminUser(t, ctx, pool, "revoker-refresh-"+uuid.NewString())
	subject := "refresh-target-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth_session_revocations WHERE supabase_subject = $1`, subject)
	})

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "EC", "crv": "P-256", "alg": "ES256", "kid": refreshTestKid,
			"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
		}}})
	}))
	t.Cleanup(jwks.Close)
	verifier, err := svcauth.NewVerifier(jwks.URL, refreshTestIssuer, refreshTestAudience, []string{"azure"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	protected := router.Group("/api/v1")
	protected.Use(mwauth.Middleware(verifier, "amrita.edu", nopDomainAuditor{}))
	checker := svcauth.NewCachedRevocationChecker(svcauth.NewSQLCRevocationChecker(pool))
	protected.Use(mwauth.RevocationGuard(checker))
	protected.GET("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func(t *testing.T, token string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	sign := func(t *testing.T, sessionID string, signedIn, issued time.Time) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
			"iss":            refreshTestIssuer,
			"aud":            refreshTestAudience,
			"sub":            subject,
			"email":          "refresh.target@amrita.edu",
			"email_verified": true,
			"app_metadata":   map[string]any{"provider": "azure", "providers": []string{"azure"}},
			"user_metadata":  map[string]any{"name": "Refresh Target"},
			"session_id":     sessionID,
			"amr":            []map[string]any{{"method": "oauth", "timestamp": signedIn.Unix()}},
			"iat":            issued.Unix(),
			"exp":            issued.Add(time.Hour).Unix(),
		})
		token.Header["kid"] = refreshTestKid
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		return signed
	}

	// The session that is about to be force-logged-out signed in half an hour
	// ago and has a live token.
	oldSession := uuid.NewString()
	var dbNow time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&dbNow); err != nil {
		t.Fatalf("read db clock: %v", err)
	}
	signedIn := dbNow.Add(-30 * time.Minute)
	if code := call(t, sign(t, oldSession, signedIn, dbNow.Add(-time.Minute))); code != http.StatusOK {
		t.Fatalf("[P0] precondition: the live session must be admitted before the revoke, got %d", code)
	}

	var revokedAfter time.Time
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_session_revocations (supabase_subject, session_id, scope, reason, revoked_by_user_id)
		VALUES ($1, NULL, 'all', 'forced logout survives refresh', $2)
		RETURNING revoked_after`, subject, adminID).Scan(&revokedAfter); err != nil {
		t.Fatalf("record scope=all revocation: %v", err)
	}
	// What the admin revoke path does after it commits (revokeDroppingCaches in
	// cmd/api): drop the cached verdicts, here the one the precondition stored.
	checker.Clear()

	t.Run("refreshed token: iat after the revoke, amr before it -> 401", func(t *testing.T) {
		refreshed := sign(t, oldSession, signedIn, revokedAfter.Add(5*time.Second))
		if code := call(t, refreshed); code != http.StatusUnauthorized {
			t.Errorf("[P0] a token refreshed after a forced logout must be refused, got %d", code)
		}
	})

	t.Run("fresh sign-in after the revoke -> allowed", func(t *testing.T) {
		at := revokedAfter.Add(5 * time.Second)
		if code := call(t, sign(t, uuid.NewString(), at, at)); code != http.StatusOK {
			t.Errorf("[P0] a genuine re-login after the revoke must be admitted, got %d", code)
		}
	})

	t.Run("fresh sign-in in the same second as the revoke -> allowed", func(t *testing.T) {
		at := revokedAfter.Truncate(time.Second)
		if code := call(t, sign(t, uuid.NewString(), at, at)); code != http.StatusOK {
			t.Errorf("[P0] same-second tolerance: a re-login in the revoke's second must be admitted, got %d", code)
		}
	})

	t.Run("token with no amr falls back to iat", func(t *testing.T) {
		// Without amr the only anchor is iat, which is the old behaviour: a token
		// issued after the revoke passes. Kept as a pinned, documented fallback
		// rather than an accident.
		token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
			"iss": refreshTestIssuer, "aud": refreshTestAudience, "sub": subject,
			"email": "refresh.target@amrita.edu", "email_verified": true,
			"app_metadata": map[string]any{"provider": "azure"},
			"session_id":   uuid.NewString(),
			"iat":          revokedAfter.Add(-time.Minute).Unix(),
			"exp":          revokedAfter.Add(time.Hour).Unix(),
		})
		token.Header["kid"] = refreshTestKid
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		if code := call(t, signed); code != http.StatusUnauthorized {
			t.Errorf("[P0] a token issued before the revoke with no amr must be refused, got %d", code)
		}
	})
}
