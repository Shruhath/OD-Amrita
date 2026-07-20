//go:build integration

// Package roundtriptest is the walking-skeleton exit gate: the executable proof
// that the thread device → contract → API → store/tx.go → Postgres → immutable
// audit row is actually connected, end to end.
//
// It wires the REAL chain against a REAL Postgres. Only the TokenVerifier is
// faked — a real Supabase ES256 JWT cannot be minted in CI, and the verifier is
// separately unit-tested against a local JWKS. Everything downstream of the
// verifier (auth middleware → MeHandler → user.Service → store.WithAudit →
// Postgres) is production code, unmodified.
//
// INTEGRATION tests: `//go:build integration` + DSN-gated, so `go test ./...`
// (no DB) skips them cleanly. Shares one database with test/db and test/store,
// so the packages MUST be serialized with `-p 1`.
//
// COUNTING DISCIPLINE: the audit ledger is append-only — the immutability trigger
// blocks DELETE/TRUNCATE for every role, including the owner. Tests therefore
// scope every count to their OWN actor (a per-run random email/subject) and never
// delete a row. This mirrors api/test/store/harness_test.go.
//
// Priority tags: [P0] critical gate · [P1] important · [P2] guard.
package roundtriptest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// The runtime DSN is the only one this gate needs: store.Store connects as
// od_runtime (DML-only), which is exactly the production write path. The
// migrator DSN is deliberately NOT used — this gate must not be able to reach
// for owner privileges to make an assertion pass.
const envRuntimeDSN = "DATABASE_URL_RUNTIME"

// allowedDomain is the domain the auth middleware enforces, matching production.
const allowedDomain = "amrita.edu"

// dsnOrSkip returns the DSN from env, or skips so the no-DB path stays clean.
func dsnOrSkip(t *testing.T, env string) string {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s not set; skipping DB integration test (set it to run against Postgres)", env)
	}
	return dsn
}

// connect opens a pgx pool for the given DSN and registers cleanup.
func connect(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// testCtx returns a context with a sane per-test timeout.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// randHex returns a short random token, used to build a per-run actor so counts
// are scoped to this test and cannot collide with a concurrent run or a leftover
// row from an earlier one.
func randHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// actor is one test's isolated identity. The email is inside the allowed domain
// so it survives EnforceDomain; the subject is the durable identity key the
// users table is unique on.
type actor struct {
	email   string
	subject string
	name    string
}

// newActor mints a fresh identity unique to this test run.
func newActor(t *testing.T) actor {
	t.Helper()
	id := randHex(t)
	return actor{
		email:   fmt.Sprintf("roundtrip.%s@cb.students.amrita.edu", id),
		subject: fmt.Sprintf("supabase-sub-%s", id),
		name:    "Round Trip " + id,
	}
}

// identity is the VerifiedIdentity the faked verifier injects — i.e. exactly what
// a genuine Supabase ES256 token would have yielded after verification.
// SessionID is a UUID because the 1.5c revocation guard keys on it and the
// verifier requires it.
func (a actor) identity(sessionID string) svcauth.VerifiedIdentity {
	return svcauth.VerifiedIdentity{
		Email:     a.email,
		Name:      a.name,
		Subject:   a.subject,
		SessionID: sessionID,
		IssuedAt:  time.Now(),
	}
}

// fakeVerifier is the ONLY faked collaborator in this gate. It stands in for the
// JWKS/ES256 verification a real Supabase token would go through, injecting the
// identity that verification would have produced. Everything it hands downstream
// is then processed by production code.
type fakeVerifier struct {
	id  svcauth.VerifiedIdentity
	err error
}

func (f fakeVerifier) Verify(ctx context.Context, token string) (svcauth.VerifiedIdentity, error) {
	if f.err != nil {
		return svcauth.VerifiedIdentity{}, f.err
	}
	return f.id, nil
}

// failingAuditInserter is the fault-injection seam (store.WithAuditInserter): it
// makes the audit INSERT fail inside the caller's transaction, without touching
// the schema. Used to prove that a failed audit write rolls the co-committed
// business write back — the tamper-evidence claim.
var errAuditInsert = errors.New("roundtrip: injected audit-insert failure")

func failingAuditInserter(ctx context.Context, tx pgx.Tx, evt store.AuditEvent) error {
	return errAuditInsert
}

// roundTripRouter assembles the REAL production chain over the given store:
//
//	mwauth.Middleware(verifier, "amrita.edu", eventAuditor)   ← only verifier faked
//	  → handlerauth.MeHandler(userSvc, eventAuditor)
//	    → svcuser.Service.Provision  → store.WithAudit → users + user.provisioned (ONE tx)
//	    → svcauth.EventAuditor.LoginSucceeded → store.WithAudit → auth.login_succeeded (ONE tx)
//
// errormw.Handler() renders the {code, message} envelope, so a provisioning or
// audit failure surfaces as INTERNAL 500 — never a 200 with a blank body.
//
// bootstrapAdmins is the AUTH_BOOTSTRAP_ADMINS allowlist; pass nil for the
// ordinary path (everyone resolves to `student`) or an actor's email to exercise
// the server-side admin resolution.
func roundTripRouter(st *store.Store, id svcauth.VerifiedIdentity, bootstrapAdmins map[string]struct{}) *gin.Engine {
	gin.SetMode(gin.TestMode)

	userSvc := svcuser.NewService(st, bootstrapAdmins, svcuser.NewSQLCProvisioner())
	eventAuditor := svcauth.NewEventAuditor(st)

	r := gin.New()
	r.Use(errormw.Handler())
	r.Use(mwauth.Middleware(fakeVerifier{id: id}, allowedDomain, eventAuditor))
	r.GET("/api/v1/auth/me", handlerauth.MeHandler(userSvc, eventAuditor))
	return r
}

// rejectedRouter is roundTripRouter's counterpart when the token verifier itself
// rejects the request — fakeVerifier.err's one reason for existing. MeHandler is
// wired identically; the middleware must abort before it is ever reached.
func rejectedRouter(st *store.Store, err error) *gin.Engine {
	gin.SetMode(gin.TestMode)

	userSvc := svcuser.NewService(st, nil, svcuser.NewSQLCProvisioner())
	eventAuditor := svcauth.NewEventAuditor(st)

	r := gin.New()
	r.Use(errormw.Handler())
	r.Use(mwauth.Middleware(fakeVerifier{err: err}, allowedDomain, eventAuditor))
	r.GET("/api/v1/auth/me", handlerauth.MeHandler(userSvc, eventAuditor))
	return r
}

// callMe performs one round trip against the assembled chain. The bearer token is
// opaque — the faked verifier ignores its value — but it must be PRESENT, because
// the middleware rejects a missing bearer as 401 before any verification runs.
// The User-Agent is the `device` recorded on the audit row, so it is set to a
// recognizable value the assertions can pin.
func callMe(t *testing.T, r *gin.Engine, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer opaque-token-verified-by-the-fake")
	req.Header.Set("User-Agent", userAgent)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- counting helpers: ALWAYS scoped to the test's own actor ---------------
//
// The ledger is append-only (the ENABLE ALWAYS immutability trigger RAISEs on
// UPDATE/DELETE/TRUNCATE for every role, owner included). There is no cleanup and
// no truncation: isolation comes from a per-run actor, and correctness from
// comparing a before/after delta rather than an absolute count.

// countUsers returns how many users rows exist for this actor's subject (0 or 1 —
// supabase_subject is UNIQUE).
func countUsers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subject string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE supabase_subject = $1`, subject).Scan(&n)
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// countAudit returns how many audit_log rows exist for this actor's account.
// Both rows the round trip can write (user.provisioned and auth.login_succeeded)
// carry actor_account = the lowercased email, so this catches both.
func countAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE actor_account = $1`, email).Scan(&n)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

// countAuditAction is countAudit narrowed to one action, so a test can assert
// WHICH rows landed, not merely how many.
func countAuditAction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, action string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE actor_account = $1 AND action = $2`,
		email, action).Scan(&n)
	if err != nil {
		t.Fatalf("count audit action %s: %v", action, err)
	}
	return n
}

// auditRow is the projection the content assertions (1.9-INT-004) read back —
// the same fields an operator eyeballs in drizzle-gateway during the device smoke
// run, but machine-asserted.
type auditRow struct {
	Action       string
	ActorAccount *string
	ActorRole    *string
	Payload      []byte
}

// latestAuditRow reads back this actor's most recent row for the given action.
func latestAuditRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, action string) auditRow {
	t.Helper()
	var row auditRow
	err := pool.QueryRow(ctx, `
		SELECT action, actor_account, actor_role, payload
		  FROM audit_log
		 WHERE actor_account = $1 AND action = $2
		 ORDER BY created_at DESC
		 LIMIT 1`, email, action).Scan(&row.Action, &row.ActorAccount, &row.ActorRole, &row.Payload)
	if err != nil {
		t.Fatalf("read audit row (%s): %v", action, err)
	}
	return row
}
