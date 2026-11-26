//go:build integration

package student

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	envMigratorDSN = "DATABASE_URL_MIGRATOR"
	envRuntimeDSN  = "DATABASE_URL_RUNTIME"
	adminUserID    = "94000000-0000-4000-8000-000000000001"
	branchID       = "94000000-0000-4000-8000-000000000002"
)

func TestImportPersistsRosterSummaryAndAuditAndRerunIsUnchanged(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())
	prefix := uniquePrefix()
	csv := fmt.Sprintf("Roll No,Name,Branch,Section,Batch\n%s001,First Student,CSE,A,2024\n%s002,Second Student,CSE,A,2024\n", prefix, prefix)

	first, err := svc.Import(ctx, adminActor(), studentsvc.ImportInput{FileName: "students.csv", Content: strings.NewReader(csv)})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.InsertedRows != 2 || first.UpdatedRows != 0 || first.UnchangedRows != 0 || first.InvalidRows != 0 {
		t.Fatalf("first counts = %+v", first)
	}
	assertImportAudit(t, ctx, migrator, first.ImportID)

	second, err := svc.Import(ctx, adminActor(), studentsvc.ImportInput{FileName: "students.csv", Content: strings.NewReader(csv)})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.InsertedRows != 0 || second.UpdatedRows != 0 || second.UnchangedRows != 2 || second.InvalidRows != 0 {
		t.Fatalf("second counts = %+v", second)
	}
	if second.DatabaseCount != first.DatabaseCount {
		t.Fatalf("database count changed on exact rerun: first=%d second=%d", first.DatabaseCount, second.DatabaseCount)
	}
	assertImportAudit(t, ctx, migrator, second.ImportID)

	var stored int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM students WHERE roll_no IN ($1, $2)`, prefix+"001", prefix+"002").Scan(&stored); err != nil {
		t.Fatalf("count imported students: %v", err)
	}
	if stored != 2 {
		t.Fatalf("stored students = %d, want 2", stored)
	}
}

func TestImportCommitsValidRowsWithInvalidRowErrors(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())
	prefix := uniquePrefix()
	csv := fmt.Sprintf("Roll No,Name,Branch,Section,Batch\n%s010,Valid Student,CSE,A,2024\n%s011,Unknown Student,ECE,A,2024\n", prefix, prefix)

	result, err := svc.Import(ctx, adminActor(), studentsvc.ImportInput{FileName: "mixed.csv", Content: strings.NewReader(csv)})
	if err != nil {
		t.Fatalf("mixed import: %v", err)
	}
	if result.InsertedRows != 1 || result.InvalidRows != 1 || len(result.Errors) != 1 {
		t.Fatalf("mixed counts = %+v", result)
	}
	if result.Errors[0].Field != "branch" || result.Errors[0].Code != "unknown" {
		t.Fatalf("row error = %+v", result.Errors[0])
	}
	assertImportAudit(t, ctx, migrator, result.ImportID)
}

func TestImportRollsBackWhenAuditInsertFails(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	expected := errors.New("audit unavailable")
	failingStore := store.New(runtime, store.WithAuditInserter(func(context.Context, pgx.Tx, store.AuditEvent) error {
		return expected
	}))
	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), failingStore, studentsvc.NewSQLCWriter())
	roll := uniquePrefix() + "020"

	_, err := svc.Import(ctx, adminActor(), studentsvc.ImportInput{
		FileName: "rollback.csv",
		Content:  strings.NewReader(fmt.Sprintf("Roll No,Name,Branch,Section,Batch\n%s,Rollback Student,CSE,A,2024\n", roll)),
	})
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want audit failure", err)
	}

	var students, imports int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM students WHERE roll_no = $1`, roll).Scan(&students); err != nil {
		t.Fatalf("count rollback student: %v", err)
	}
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM student_roster_imports WHERE source_filename = 'rollback.csv'`).Scan(&imports); err != nil {
		t.Fatalf("count rollback imports: %v", err)
	}
	if students != 0 || imports != 0 {
		t.Fatalf("rollback left students=%d imports=%d", students, imports)
	}
}

func TestRosterSchemaRejectsNonCanonicalReferenceData(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	seed(t, ctx, migrator)

	if _, err := migrator.Exec(ctx, `
		INSERT INTO branches (id, code, name)
		VALUES ('94000000-0000-4000-8000-000000000099', 'cse', 'Lowercase CSE')
	`); err == nil {
		t.Fatal("expected lowercase branch code to be rejected")
	}

	if _, err := migrator.Exec(ctx, `
		INSERT INTO students (roll_no, name, branch_id, section, batch)
		VALUES ('cb.sc.u4cse99999', 'Lowercase Roll', $1, 'A', '2024')
	`, branchID); err == nil {
		t.Fatal("expected lowercase student roll number to be rejected")
	}
}

func assertImportAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, importID string) {
	t.Helper()
	var count int
	var payloadText string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), coalesce(max(payload::text), '{}')
		FROM audit_log
		WHERE action = 'student_roster.imported'
		  AND target_table = 'student_roster_imports'
		  AND target_id = $1`, importID).Scan(&count, &payloadText); err != nil {
		t.Fatalf("count import audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit rows for import %s = %d, want 1", importID, count)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode import audit payload: %v (%s)", err, payloadText)
	}
	for _, key := range []string{"total_rows", "valid_rows", "inserted_rows", "updated_rows", "unchanged_rows", "invalid_rows", "database_count"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("audit payload missing %s: %#v", key, payload)
		}
	}
}

func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, 'student-import-admin', 'student-import-admin@amrita.edu', 'Student Import Admin', 'admin')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role
	`, adminUserID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO branches (id, code, name)
		VALUES ($1, 'CSE', 'Computer Science and Engineering')
		ON CONFLICT (id) DO UPDATE SET code = EXCLUDED.code, name = EXCLUDED.name
	`, branchID); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
}

func adminActor() studentsvc.Actor {
	return studentsvc.Actor{UserID: adminUserID, Email: "student-import-admin@amrita.edu", Role: "admin"}
}

func uniquePrefix() string {
	return fmt.Sprintf("CB.SC.U4CSE%08d", time.Now().UnixNano()%100000000)
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
		t.Skipf("%s not set", env)
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
