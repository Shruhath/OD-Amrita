//go:build integration

// roundtrip_test.go — AC 1 of the walking-skeleton exit gate, as an executable
// conservation property rather than a vibe:
//
//	returning identity   → exactly 1 new audit_log row, exactly 0 new users rows
//	first-contact identity → exactly 1 new users row, exactly 2 new audit_log rows
//	audit insert forced to fail → 0 rows of EITHER kind, and a 500 (never a 200)
//
// The epic's negative test — that od_runtime cannot UPDATE/DELETE/TRUNCATE
// audit_log, nor disable the immutability trigger — is already proven in
// api/test/db/immutability_test.go and api/test/db/roles_test.go. It is cited
// here, deliberately NOT rewritten (no duplicate coverage).
package roundtriptest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// The device string the round trip records as `payload.device`. In production this
// is the phone's User-Agent; here it is pinned so 1.9-INT-004 can assert the value
// actually travelled from the request into the ledger.
const testDevice = "OD-Amrita-RoundTripGate/1.0 (integration)"

// TestRoundTrip_FirstContact_WritesUserAndTwoAuditRows — [P0] 1.9-INT-001.
//
// A first-contact identity completing GET /api/v1/auth/me must yield exactly ONE
// users row and exactly TWO audit rows: user.provisioned (co-committed WITH the
// users row in one tx) and auth.login_succeeded (its own tx). The response must be
// 200 and must carry the server-resolved role.
func TestRoundTrip_FirstContact_WritesUserAndTwoAuditRows(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	a := newActor(t)

	usersBefore := countUsers(t, ctx, pool, a.subject)
	auditBefore := countAudit(t, ctx, pool, a.email)
	if usersBefore != 0 || auditBefore != 0 {
		t.Fatalf("[P0] a freshly minted actor must not pre-exist; got users=%d audit=%d", usersBefore, auditBefore)
	}

	st := store.New(pool)
	r := roundTripRouter(st, a.identity(uuid.NewString()), nil)

	w := callMe(t, r, testDevice)

	if w.Code != http.StatusOK {
		t.Fatalf("[P0] first contact must complete the round trip with 200, got %d (%s)", w.Code, w.Body.String())
	}

	dUsers := countUsers(t, ctx, pool, a.subject) - usersBefore
	dAudit := countAudit(t, ctx, pool, a.email) - auditBefore

	if dUsers != 1 {
		t.Errorf("[P0] first contact must write exactly 1 users row, got Δusers=%d", dUsers)
	}
	if dAudit != 2 {
		t.Errorf("[P0] first contact must write exactly 2 audit rows (user.provisioned + auth.login_succeeded), got Δaudit=%d", dAudit)
	}

	// Assert WHICH rows landed, not merely how many — 2 rows of the wrong actions
	// would satisfy a bare count.
	if n := countAuditAction(t, ctx, pool, a.email, string(domain.ActionUserProvisioned)); n != 1 {
		t.Errorf("[P0] expected exactly 1 %s row, got %d", domain.ActionUserProvisioned, n)
	}
	if n := countAuditAction(t, ctx, pool, a.email, string(domain.ActionAuthLoginSucceeded)); n != 1 {
		t.Errorf("[P0] expected exactly 1 %s row, got %d", domain.ActionAuthLoginSucceeded, n)
	}

	// The response must be the real contract type, carrying the server-resolved
	// role. This is the value the device cannot compute for itself (AC 2).
	var got contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("[P0] response must decode as AuthenticatedIdentity: %v (%s)", err, w.Body.String())
	}
	if got.Subject != a.subject {
		t.Errorf("[P0] response subject = %q, want %q", got.Subject, a.subject)
	}
	if got.Email != a.email {
		t.Errorf("[P0] response email = %q, want %q (lowercased, as stored)", got.Email, a.email)
	}
	if got.Name == nil || *got.Name != a.name {
		t.Errorf("[P0] response name = %v, want %q (AC 2 requires the server-supplied name to render)", got.Name, a.name)
	}
	// No bootstrap allowlist was passed, so ResolveRole must land on `student`.
	// (Coordinator assignment is Story 2.4 — see the story's spec-vs-reality note.)
	if got.Role != contracts.AuthenticatedIdentityRoleStudent {
		t.Errorf("[P0] server-resolved role = %q, want %q", got.Role, contracts.AuthenticatedIdentityRoleStudent)
	}
}

// TestRoundTrip_ReturningIdentity_WritesExactlyOneAuditRow — [P0] 1.9-INT-002.
//
// THE literal epic exit criterion: "one observable round trip writes exactly one
// auditable row through store/tx.go". A returning identity provisions nothing
// (the last_login_at bump is not an auditable event → changed=false → no
// user.provisioned row) and earns exactly ONE new row: auth.login_succeeded.
func TestRoundTrip_ReturningIdentity_WritesExactlyOneAuditRow(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	a := newActor(t)
	st := store.New(pool)

	// First contact — establishes the user. Asserted by 1.9-INT-001; here it is
	// only the precondition.
	first := callMe(t, roundTripRouter(st, a.identity(uuid.NewString()), nil), testDevice)
	if first.Code != http.StatusOK {
		t.Fatalf("[P0] precondition: first contact must succeed, got %d (%s)", first.Code, first.Body.String())
	}

	usersBefore := countUsers(t, ctx, pool, a.subject)
	auditBefore := countAudit(t, ctx, pool, a.email)

	// The SAME subject logs in again — a new session, a returning identity.
	second := callMe(t, roundTripRouter(st, a.identity(uuid.NewString()), nil), testDevice)
	if second.Code != http.StatusOK {
		t.Fatalf("[P0] returning identity must complete the round trip with 200, got %d (%s)", second.Code, second.Body.String())
	}

	dUsers := countUsers(t, ctx, pool, a.subject) - usersBefore
	dAudit := countAudit(t, ctx, pool, a.email) - auditBefore

	if dUsers != 0 {
		t.Errorf("[P0] a returning identity must write NO new users row, got Δusers=%d", dUsers)
	}
	if dAudit != 1 {
		t.Errorf("[P0] a returning identity must write EXACTLY ONE auditable row (the epic exit criterion), got Δaudit=%d", dAudit)
	}
	// And that one row must be the login, not a second provisioning.
	if n := countAuditAction(t, ctx, pool, a.email, string(domain.ActionUserProvisioned)); n != 1 {
		t.Errorf("[P0] %s must remain at exactly 1 across both round trips (provisioning is once-only), got %d",
			domain.ActionUserProvisioned, n)
	}
	if n := countAuditAction(t, ctx, pool, a.email, string(domain.ActionAuthLoginSucceeded)); n != 2 {
		t.Errorf("[P0] %s must fire on EVERY round trip (2 calls → 2 rows), got %d",
			domain.ActionAuthLoginSucceeded, n)
	}
}

// TestRoundTrip_AuditInsertFailure_RollsBackTheBusinessWrite — [P0] 1.9-INT-003.
//
// The tamper-evidence claim, and the sharpest test in this gate. With the audit
// inserter forced to fail, a first contact must leave ZERO rows of EITHER kind —
// the audit failure rolls the co-committed users write back — and the handler must
// fail closed with 500 INTERNAL. A 200 here would mean a login completed with no
// ledger entry, which is the exact divergence store/tx.go exists to prevent.
//
// NOTE: nothing in the repo has previously proven this end-to-end, against a real
// Postgres, through the real service wiring. store.WithAudit *trusts* the caller's
// `changed` flag (a deferred note from the 1.4 review). If this test fails, that is
// a real defect — not a red phase to be "made green".
func TestRoundTrip_AuditInsertFailure_RollsBackTheBusinessWrite(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	a := newActor(t)

	usersBefore := countUsers(t, ctx, pool, a.subject)
	auditBefore := countAudit(t, ctx, pool, a.email)

	// The ONE deviation from production wiring: the audit insert is made to fail.
	st := store.New(pool, store.WithAuditInserter(failingAuditInserter))
	r := roundTripRouter(st, a.identity(uuid.NewString()), nil)

	w := callMe(t, r, testDevice)

	if w.Code == http.StatusOK {
		t.Fatalf("[P0] a login whose audit write fails must NEVER return 200 — the ledger would diverge from the users table")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("[P0] audit-insert failure must fail closed as 500, got %d (%s)", w.Code, w.Body.String())
	}

	// The error envelope must be the contract's {code, message}, rendered by the
	// centralized error middleware — never a hand-written body.
	var envelope contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("[P0] error response must decode as the {code, message} envelope: %v (%s)", err, w.Body.String())
	}
	if envelope.Code != contracts.ErrorCodeINTERNAL {
		t.Errorf("[P0] error code = %q, want %q", envelope.Code, contracts.ErrorCodeINTERNAL)
	}

	dUsers := countUsers(t, ctx, pool, a.subject) - usersBefore
	dAudit := countAudit(t, ctx, pool, a.email) - auditBefore

	if dUsers != 0 {
		t.Errorf("[P0] the audit failure must roll the users write back — got Δusers=%d (an unaudited user was persisted)", dUsers)
	}
	if dAudit != 0 {
		t.Errorf("[P0] a failed audit insert must leave no audit row, got Δaudit=%d", dAudit)
	}
}

// TestRoundTrip_InvalidToken_RejectsBeforeAnyWrite — [P0] 1.9-INT-006.
//
// fakeVerifier.err exists specifically to simulate a token the JWKS/ES256
// verification step itself rejects (expired, bad signature, wrong issuer/audience).
// That path must abort 401 before MeHandler — and therefore store.WithAudit — is
// ever reached: an invalid token must leave the ledger and the users table
// untouched, not merely return the right status code.
func TestRoundTrip_InvalidToken_RejectsBeforeAnyWrite(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	a := newActor(t)

	usersBefore := countUsers(t, ctx, pool, a.subject)
	auditBefore := countAudit(t, ctx, pool, a.email)

	st := store.New(pool)
	verifyErr := domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid."}
	w := callMe(t, rejectedRouter(st, verifyErr), testDevice)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("[P0] a token the verifier rejects must be a 401, got %d (%s)", w.Code, w.Body.String())
	}

	var envelope contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("[P0] error response must decode as the {code, message} envelope: %v (%s)", err, w.Body.String())
	}
	if envelope.Code != contracts.ErrorCodeAUTHTOKENINVALID {
		t.Errorf("[P0] error code = %q, want %q", envelope.Code, contracts.ErrorCodeAUTHTOKENINVALID)
	}

	dUsers := countUsers(t, ctx, pool, a.subject) - usersBefore
	dAudit := countAudit(t, ctx, pool, a.email) - auditBefore
	if dUsers != 0 || dAudit != 0 {
		t.Errorf("[P0] a rejected token must never reach the write path, got Δusers=%d Δaudit=%d", dUsers, dAudit)
	}
}

// TestRoundTrip_LoginAuditRowContent — [P1] 1.9-INT-004.
//
// The audit row is the artifact. This asserts what an operator reads back in
// drizzle-gateway during the device smoke run (Task 7): the action, the actor
// account, the SERVER-RESOLVED role, and a payload carrying the session_id and
// the device (the request's User-Agent). A row with the right count but an empty
// payload would still fail the story's intent.
func TestRoundTrip_LoginAuditRowContent(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	a := newActor(t)
	sessionID := uuid.NewString()

	st := store.New(pool)
	w := callMe(t, roundTripRouter(st, a.identity(sessionID), nil), testDevice)
	if w.Code != http.StatusOK {
		t.Fatalf("[P1] precondition: round trip must succeed, got %d (%s)", w.Code, w.Body.String())
	}

	row := latestAuditRow(t, ctx, pool, a.email, string(domain.ActionAuthLoginSucceeded))

	if row.ActorAccount == nil || *row.ActorAccount != a.email {
		t.Errorf("[P1] actor_account must be the lowercased account, got %v want %q", row.ActorAccount, a.email)
	}
	if row.ActorRole == nil || *row.ActorRole != string(contracts.AuthenticatedIdentityRoleStudent) {
		t.Errorf("[P1] actor_role must be the server-resolved role, got %v want %q",
			row.ActorRole, contracts.AuthenticatedIdentityRoleStudent)
	}

	var payload map[string]any
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("[P1] payload must be readable jsonb: %v (%s)", err, string(row.Payload))
	}
	if got := payload["session_id"]; got != sessionID {
		t.Errorf("[P1] payload.session_id = %v, want %q (the dedup anchor the revocation guard keys on)", got, sessionID)
	}
	if got := payload["device"]; got != testDevice {
		t.Errorf("[P1] payload.device = %v, want %q (the request User-Agent must reach the ledger)", got, testDevice)
	}
	if got := payload["subject"]; got != a.subject {
		t.Errorf("[P1] payload.subject = %v, want %q", got, a.subject)
	}
}

// TestRoundTrip_BootstrapAdmin_RoleIsResolvedServerSide — [P1] 1.9-INT-005.
//
// The backend half of AC 2's claim. The screen must render a value it CANNOT have
// computed — the role. This proves the role is resolved server-side from the
// AUTH_BOOTSTRAP_ADMINS allowlist (nothing the device sends can influence it) and
// that the resolved value is what gets both echoed AND audited.
//
// It also pins the story's spec-vs-reality note: `admin` is reachable only via the
// allowlist. There is no `coordinator` path yet, and this story must not add one
// (that is Story 2.4).
func TestRoundTrip_BootstrapAdmin_RoleIsResolvedServerSide(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	a := newActor(t)

	// The allowlist is lowercased at config load; mirror that here.
	bootstrapAdmins := map[string]struct{}{a.email: {}}

	st := store.New(pool)
	w := callMe(t, roundTripRouter(st, a.identity(uuid.NewString()), bootstrapAdmins), testDevice)
	if w.Code != http.StatusOK {
		t.Fatalf("[P1] bootstrap admin must complete the round trip, got %d (%s)", w.Code, w.Body.String())
	}

	var got contracts.AuthenticatedIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("[P1] response must decode as AuthenticatedIdentity: %v (%s)", err, w.Body.String())
	}
	if got.Role != contracts.AuthenticatedIdentityRoleAdmin {
		t.Errorf("[P1] an allowlisted email must resolve SERVER-SIDE to admin, got %q", got.Role)
	}

	// The audited role must match the echoed one — the ledger and the response
	// cannot tell different stories about who logged in.
	row := latestAuditRow(t, ctx, pool, a.email, string(domain.ActionAuthLoginSucceeded))
	if row.ActorRole == nil || *row.ActorRole != string(contracts.AuthenticatedIdentityRoleAdmin) {
		t.Errorf("[P1] the audited role must match the echoed role, got %v want admin", row.ActorRole)
	}
}
