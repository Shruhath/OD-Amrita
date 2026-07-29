//go:build integration

package cardtokentest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCardTokenLedgerSchemaAndRuntimePrivileges(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))

	columns := map[string]string{}
	rows, err := migrator.Query(ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'student_card_tokens'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("read card token ledger columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = typ
	}
	for name, typ := range map[string]string{
		"id":           "uuid",
		"student_id":   "uuid",
		"version":      "text",
		"token_sha256": "bytea",
		"created_at":   "timestamp with time zone",
		"updated_at":   "timestamp with time zone",
	} {
		if columns[name] != typ {
			t.Errorf("[P0] student_card_tokens.%s type=%q, want %q", name, columns[name], typ)
		}
	}

	for privilege, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": false} {
		var got bool
		if err := migrator.QueryRow(ctx,
			`SELECT has_table_privilege('od_runtime', 'student_card_tokens', $1)`, privilege,
		).Scan(&got); err != nil {
			t.Fatalf("check %s privilege: %v", privilege, err)
		}
		if got != want {
			t.Errorf("[P0] od_runtime %s on student_card_tokens = %v, want %v", privilege, got, want)
		}
	}
}

func TestCardTokenLedgerStoresFingerprintOnlyAndResolvesInConstantLookupShape(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	seedStudent(t, ctx, migrator)

	token := "QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"
	hash := sha256.Sum256([]byte(token))
	if _, err := migrator.Exec(ctx, `
		INSERT INTO student_card_tokens (student_id, version, token_sha256)
		VALUES ('97000000-0000-4000-8000-000000000101', 'v1', $1)
		ON CONFLICT (student_id, version) DO UPDATE
		SET token_sha256 = EXCLUDED.token_sha256, updated_at = now()`, hash[:]); err != nil {
		t.Fatalf("upsert token ledger row: %v", err)
	}

	var storedPlaintext int
	if err := migrator.QueryRow(ctx, `
		SELECT count(*)
		FROM student_card_tokens
		WHERE encode(token_sha256, 'escape') LIKE '%' || $1 || '%'`, token).Scan(&storedPlaintext); err != nil {
		t.Fatalf("check plaintext token leakage: %v", err)
	}
	if storedPlaintext != 0 {
		t.Fatalf("[P0] raw token appears in ledger storage")
	}

	var rollNo, name, branch, section, batch string
	if err := migrator.QueryRow(ctx, `
		SELECT s.roll_no, s.name, b.code, s.section, s.batch
		FROM student_card_tokens t
		JOIN students s ON s.id = t.student_id
		JOIN branches b ON b.id = s.branch_id
		WHERE t.version = 'v1' AND t.token_sha256 = decode($1, 'hex')`, hex.EncodeToString(hash[:])).
		Scan(&rollNo, &name, &branch, &section, &batch); err != nil {
		t.Fatalf("resolve token fingerprint: %v", err)
	}
	if rollNo != "CB.SC.U4CSE24101" || name != "ATDD Card Student" || branch != "CSE" || section != "A" || batch != "2024" {
		t.Fatalf("resolved student = %s/%s/%s/%s/%s", rollNo, name, branch, section, batch)
	}
}

func seedStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var branchID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO branches (code, name)
		VALUES ('CSE', 'Computer Science and Engineering')
		ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`).Scan(&branchID); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ('97000000-0000-4000-8000-000000000101', 'CB.SC.U4CSE24101', 'ATDD Card Student',
		        $1, 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch`, branchID); err != nil {
		t.Fatalf("seed student: %v", err)
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dsnOrSkip(t *testing.T, env string) string {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s not set; skipping database integration test", env)
	}
	return dsn
}

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
