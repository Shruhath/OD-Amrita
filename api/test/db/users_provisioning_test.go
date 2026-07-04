//go:build integration

package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// Story 1.5b DB acceptance. Run with a live Postgres + the two roles:
//
//	DATABASE_URL_MIGRATOR=... DATABASE_URL_RUNTIME=... \
//	  go test -tags integration ./test/db/... -run Users
//
// Proves: 0003 creates exactly `users` (no refresh_tokens) with the required
// columns; od_runtime has DML but not DDL on it; and first-vs-returning
// provisioning through store.WithAudit writes exactly one user.provisioned audit
// row on the genuine first insert and none on the returning no-op.

func TestUsersMigration_SchemaAndRuntimeGrants(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))

	var tableCount int
	if err := migratorPool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_name IN ('users', 'refresh_tokens')
	`).Scan(&tableCount); err != nil {
		t.Fatalf("count story tables: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("Story 1.5b must create exactly users and no refresh_tokens table; matched count=%d", tableCount)
	}

	required := map[string]string{
		"id":               "uuid",
		"supabase_subject": "text",
		"email":            "text",
		"name":             "text",
		"role":             "text",
		"created_at":       "timestamp with time zone",
		"updated_at":       "timestamp with time zone",
		"last_login_at":    "timestamp with time zone",
	}
	for col, typ := range required {
		var got string
		err := migratorPool.QueryRow(ctx, `
			SELECT data_type
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'users'
			  AND column_name = $1
		`, col).Scan(&got)
		if err != nil {
			t.Fatalf("expected users.%s to exist: %v", col, err)
		}
		if got != typ {
			t.Errorf("users.%s type: want %q got %q", col, typ, got)
		}
	}

	if _, err := runtimePool.Exec(ctx, `
		INSERT INTO users (supabase_subject, email, name, role)
		VALUES ('atdd-sub-grant', 'atdd-grant@amrita.edu', 'ATDD Grant', 'student')
		ON CONFLICT (supabase_subject) DO UPDATE SET last_login_at = now()
	`); err != nil {
		t.Fatalf("od_runtime must have INSERT/UPDATE on users: %v", err)
	}
	if _, err := runtimePool.Exec(ctx, `ALTER TABLE users ADD COLUMN atdd_forbidden text`); !isPermissionDenied(err) {
		t.Fatalf("od_runtime must not have DDL on users; got err=%v", err)
	}
}

func TestUsersProvisioning_IdempotentAudit(t *testing.T) {
	ctx := testCtx(t)
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	s := store.New(runtimePool)
	runID := fmt.Sprintf("prov-%d", time.Now().UnixNano())
	subject := runID + "-sub"
	email := runID + "@amrita.edu"
	role := "student"
	targetTable := "users"

	var firstUserID string
	firstEvt := store.AuditEvent{
		Action:       domain.ActionUserProvisioned,
		ActorAccount: &email,
		ActorRole:    &role,
		TargetTable:  &targetTable,
		TargetID:     &firstUserID,
	}
	if err := s.WithAudit(ctx, firstEvt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return provisionRawUser(ctx, tx, subject, email, "Prov Student", role, &firstUserID)
	}); err != nil {
		t.Fatalf("first provisioning should co-commit user + audit row: %v", err)
	}
	if firstUserID == "" {
		t.Fatal("first provisioning must capture new user id for audit target_id")
	}
	if got := auditCount(ctx, t, runtimePool, firstUserID, domain.ActionUserProvisioned); got != 1 {
		t.Fatalf("first provisioning should write exactly one user.provisioned audit row, got %d", got)
	}

	secondEvt := store.AuditEvent{
		Action:       domain.ActionUserProvisioned,
		ActorAccount: &email,
		ActorRole:    &role,
		TargetTable:  &targetTable,
		TargetID:     &firstUserID,
	}
	if err := s.WithAudit(ctx, secondEvt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var returnedID string
		changed, err := provisionRawUser(ctx, tx, subject, email, "Prov Student", role, &returnedID)
		if returnedID != firstUserID {
			t.Fatalf("returning subject should map to same user id: first=%s returned=%s", firstUserID, returnedID)
		}
		return changed, err
	}); err != nil {
		t.Fatalf("returning-user provisioning should commit cleanly as no-op: %v", err)
	}
	if got := auditCount(ctx, t, runtimePool, firstUserID, domain.ActionUserProvisioned); got != 1 {
		t.Fatalf("second provisioning should not add audit rows, got total %d", got)
	}
}

func provisionRawUser(ctx context.Context, tx pgx.Tx, subject, email, name, role string, id *string) (bool, error) {
	var inserted bool
	err := tx.QueryRow(ctx, `
		INSERT INTO users (supabase_subject, email, name, role, last_login_at)
		VALUES ($1, lower($2), $3, $4, now())
		ON CONFLICT (supabase_subject) DO UPDATE
		SET last_login_at = now()
		RETURNING id::text, (xmax = 0) AS inserted
	`, subject, email, name, role).Scan(id, &inserted)
	return inserted, err
}

func auditCount(ctx context.Context, t *testing.T, pool queryRower, targetID string, action domain.Action) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE target_id = $1 AND action = $2
	`, targetID, string(action)).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
