//go:build integration

package cardtokentest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
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

	var hasLookupIndex bool
	if err := migrator.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_indexes
			WHERE schemaname = 'public'
			  AND tablename = 'student_card_tokens'
			  AND indexname = 'student_card_tokens_version_fingerprint_unique_idx'
			  AND indexdef LIKE '%(version, token_sha256)%'
		)`).Scan(&hasLookupIndex); err != nil {
		t.Fatalf("check version/fingerprint lookup index: %v", err)
	}
	if !hasLookupIndex {
		t.Fatal("[P0] student_card_tokens must keep an indexed O(1)-shape lookup on (version, token_sha256)")
	}
}

func TestCardTokenLedgerStoresFingerprintOnlyAndResolvesInConstantLookupShape(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	studentID := uuid.NewString()
	rollNo := uniqueRollNo(studentID)
	seedStudent(t, ctx, migrator, studentID, rollNo)

	tokenSeed := sha256.Sum256([]byte(studentID))
	token := base64.RawURLEncoding.EncodeToString(tokenSeed[:])
	hash := sha256.Sum256([]byte(token))
	if _, err := migrator.Exec(ctx, `
		INSERT INTO student_card_tokens (student_id, version, token_sha256)
		VALUES ($2, 'v1', $1)
		ON CONFLICT (student_id, version) DO UPDATE
		SET token_sha256 = EXCLUDED.token_sha256, updated_at = now()`, hash[:], studentID); err != nil {
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

	var gotRollNo, name, branch, section, batch string
	if err := migrator.QueryRow(ctx, `
		SELECT s.roll_no, s.name, b.code, s.section, s.batch
		FROM student_card_tokens t
		JOIN students s ON s.id = t.student_id
		JOIN branches b ON b.id = s.branch_id
		WHERE t.version = 'v1' AND t.token_sha256 = decode($1, 'hex')`, hex.EncodeToString(hash[:])).
		Scan(&gotRollNo, &name, &branch, &section, &batch); err != nil {
		t.Fatalf("resolve token fingerprint: %v", err)
	}
	if gotRollNo != rollNo || name != "ATDD Card Student" || branch != "CSE" || section != "A" || batch != "2024" {
		t.Fatalf("resolved student = %s/%s/%s/%s/%s", gotRollNo, name, branch, section, batch)
	}
}

func TestCardTokenServiceSealsAuditsIdempotentlyAndRejectsUnknownTokens(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	studentID := uuid.NewString()
	rollNo := uniqueRollNo(studentID)
	seedStudent(t, ctx, migrator, studentID, rollNo)

	keyring, err := svccardtoken.NewKeyring("v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	svc := svccardtoken.NewService(svccardtoken.NewSQLCReader(runtime), svccardtoken.NewSQLCLedger(runtime), store.New(runtime), keyring)
	actor := svccardtoken.Actor{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"}

	first, err := svc.Seal(ctx, actor, svccardtoken.SealInput{StudentID: studentID})
	if err != nil {
		t.Fatalf("first seal: %v", err)
	}
	second, err := svc.Seal(ctx, actor, svccardtoken.SealInput{StudentID: studentID})
	if err != nil {
		t.Fatalf("repeat seal: %v", err)
	}
	if first.Token != second.Token || first.Version != "v1" {
		t.Fatalf("repeat seal returned %q/%q then %q/%q", first.Version, first.Token, second.Version, second.Token)
	}

	var ledgerRows int
	if err := migrator.QueryRow(ctx,
		`SELECT count(*) FROM student_card_tokens WHERE student_id = $1 AND version = 'v1'`,
		studentID,
	).Scan(&ledgerRows); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("[P0] ledger rows for student/version = %d, want 1", ledgerRows)
	}

	hash := sha256.Sum256([]byte(first.Token))
	var storedHash []byte
	if err := migrator.QueryRow(ctx,
		`SELECT token_sha256 FROM student_card_tokens WHERE student_id = $1 AND version = 'v1'`,
		studentID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("read stored fingerprint: %v", err)
	}
	if !bytes.Equal(storedHash, hash[:]) {
		t.Fatalf("stored fingerprint = %x, want SHA256(token) %x", storedHash, hash)
	}

	var auditRows int
	if err := migrator.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE action = $1
		  AND target_table = 'student_card_tokens'
		  AND target_id = $2`,
		string(domain.ActionCardTokenSealed), studentID,
	).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditRows != 1 {
		t.Fatalf("[P0] card-token seal audit rows = %d, want exactly one changed write", auditRows)
	}

	resolved, err := svc.Resolve(ctx, actor, svccardtoken.ResolveInput{Version: first.Version, Token: first.Token})
	if err != nil {
		t.Fatalf("resolve known token: %v", err)
	}
	if resolved.StudentID != studentID || resolved.RollNo != rollNo {
		t.Fatalf("resolved = %+v, want student %s/%s", resolved, studentID, rollNo)
	}

	_, err = svc.Resolve(ctx, actor, svccardtoken.ResolveInput{Version: "v1", Token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeCARDNOTRECOGNIZED {
		t.Fatalf("unknown token error = %v, want CARD_NOT_RECOGNIZED", err)
	}
}

func seedStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID, rollNo string) {
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
		VALUES ($2, $3, 'ATDD Card Student',
		        $1, 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch`, branchID, studentID, rollNo); err != nil {
		t.Fatalf("seed student: %v", err)
	}
}

func uniqueRollNo(studentID string) string {
	return "CB.SC.U4CSE" + strings.ToUpper(studentID[:8])
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
