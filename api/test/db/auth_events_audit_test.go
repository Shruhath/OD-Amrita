//go:build integration

package db

// auth_events_audit_test.go — DB acceptance for auth-event auditing. Proves the auth-event
// audit actually lands an immutable row in audit_log through the real write path
// (store.WithAudit's no-op-write-is-a-write pattern), not just a fake. Run with a
// live Postgres + the runtime role:
//
//	DATABASE_URL_MIGRATOR=... DATABASE_URL_RUNTIME=... \
//	  go test -tags integration ./test/db/... -run AuthLoginSucceeded
//
// Each run uses a UNIQUE actor account (audit_log is append-only / immutable, so
// rows accumulate across runs) and asserts exactly one auth.login_succeeded row
// with the expected action / actor_account / actor_role / payload{device,
// session_id, subject}.
//
// It drives svcauth.NewEventAuditor(...).LoginSucceeded(...).

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// TestAuthLoginSucceeded_WritesImmutableAuditRow — [P1] a real auth.login_succeeded
// audit row co-commits through store.WithAudit and is queryable with the expected
// action, actor account, provisioned role, and jsonb payload.
func TestAuthLoginSucceeded_WritesImmutableAuditRow(t *testing.T) {
	ctx := testCtx(t)
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	st := store.New(runtimePool)
	auditor := svcauth.NewEventAuditor(st)

	// Unique per run so the count below is exact against an append-only ledger.
	uniq := time.Now().UnixNano()
	email := fmt.Sprintf("login.audit.%d@amrita.edu", uniq)
	subject := fmt.Sprintf("sub-%d", uniq)
	sessionID := fmt.Sprintf("sess-%d", uniq)
	const device = "OD-Amrita/1.0 (integration)"

	if err := auditor.LoginSucceeded(ctx, email, "student", subject, sessionID, device); err != nil {
		t.Fatalf("[P1] LoginSucceeded must co-commit an audit row: %v", err)
	}

	count, actorRole, payload := authEventRow(ctx, t, runtimePool, email, domain.ActionAuthLoginSucceeded)
	if count != 1 {
		t.Fatalf("[P1] expected exactly one %q row for %s, got %d", domain.ActionAuthLoginSucceeded, email, count)
	}
	if actorRole == nil || *actorRole != "student" {
		t.Errorf("[P1] actor_role must persist as the provisioned role, got %v", actorRole)
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("[P1] payload must be valid jsonb: %v (%s)", err, string(payload))
	}
	if p["device"] != device {
		t.Errorf("[P1] payload.device mismatch: got %v", p["device"])
	}
	if p["session_id"] != sessionID {
		t.Errorf("[P1] payload.session_id mismatch: got %v", p["session_id"])
	}
	if p["subject"] != subject {
		t.Errorf("[P1] payload.subject mismatch: got %v", p["subject"])
	}
}

// authEventRow counts audit rows for (action, actor_account) and returns the
// nullable actor_role + payload of the (expected single) row. Unlike the shared
// auditCount helper it keys on actor_account, since an auth event carries no
// target_id.
func authEventRow(ctx context.Context, t *testing.T, pool queryRower, actorAccount string, action domain.Action) (int, *string, []byte) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE action = $1 AND actor_account = $2
	`, string(action), actorAccount).Scan(&count); err != nil {
		t.Fatalf("count auth-event rows: %v", err)
	}
	if count != 1 {
		return count, nil, nil
	}
	var role *string
	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT actor_role, payload FROM audit_log WHERE action = $1 AND actor_account = $2
	`, string(action), actorAccount).Scan(&role, &payload); err != nil {
		if err == pgx.ErrNoRows {
			t.Fatalf("auth-event row vanished between count and fetch")
		}
		t.Fatalf("fetch auth-event row: %v", err)
	}
	return count, role, payload
}
