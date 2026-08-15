//go:build integration

package teachertest

// teacher_test.go — Story 2.3 coverage for AC 1-5 and 7.
// REAL-POSTGRES integration: build tag `integration`.
//
// Invoked by: api-ci -> integration job, once wired per Task 6:
//   go test -tags integration -p 1 ./test/db/... ./test/store/... ./test/event/...
//     ./test/roundtrip/... ./test/teacher/... -v
//
// New package (teacher profiles are not part of the event aggregate, so this
// does not fold into api/test/event). Mirrors isolatedCategoryDatabase's
// shape from api/test/event/categories_test.go but is self-contained: Go test
// helpers are unexported, so a second package cannot reuse the originals
// directly, and re-declaring them here (rather than importing internal test
// code across packages) is the same call Story 2.2 made for its own harness.
//
// Cases that only need migration 0008 (schema, FK, privileges, goose
// round-trip) are EXECUTABLE TODAY — they speak SQL directly and need no
// Go teacher package. Cases that exercise the real service (audit
// conservation, the raced-elevation path end to end) are activation-ready:
// they need service/teacher, which Task 4 creates.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	teachersvc "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// ---------------------------------------------------------------------------
// AC 3 — schema: table shape, FK, and the composite primary key
// ---------------------------------------------------------------------------

func TestUserBranchesMigrationRoundTripsCleanly(t *testing.T) {
	ctx := testContext(t)
	migrator, _, migratorDSN := isolatedTeacherDatabase(t, ctx)

	assertTableExists(t, ctx, migrator, "user_branches", true)

	// down-to 7, not "down": names the target version so this stays pointed at
	// 0008 no matter what lands after it — the exact "clean revert" gate Story
	// 2.2's git history (d0204ab) fixed for the CI rollback job.
	runGoose(t, ctx, migratorDSN, "down-to", "7")
	assertTableExists(t, ctx, migrator, "user_branches", false)

	runGoose(t, ctx, migratorDSN, "up")
	assertTableExists(t, ctx, migrator, "user_branches", true)
}

func TestUserBranchesEnforcesForeignKeysAndTheCompositePrimaryKey(t *testing.T) {
	ctx := testContext(t)
	migrator, _, _ := isolatedTeacherDatabase(t, ctx)
	userID := seedUser(t, ctx, migrator, "teacher-fixture@amrita.edu", "student")
	branchID := seedBranch(t, ctx, migrator, "CSE", "Computer Science")

	if _, err := migrator.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, userID, branchID); err != nil {
		t.Fatalf("[P0] insert user_branches row: %v", err)
	}

	// The composite primary key: the same (user, branch) pair twice is a
	// duplicate, not a second association.
	if _, err := migrator.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, userID, branchID); err == nil {
		t.Error("[P0] a duplicate (user_id, branch_id) pair must violate the primary key")
	}

	if _, err := migrator.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, uuid.New(), branchID); err == nil {
		t.Error("[P0] an association to an unknown user must violate the foreign key")
	}
	if _, err := migrator.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, userID, uuid.New()); err == nil {
		t.Error("[P0] an association to an unknown branch must violate the foreign key")
	}

	// ON DELETE RESTRICT, mirroring event_branches: a user or branch that still
	// has associations may not be deleted out from under them.
	if _, err := migrator.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err == nil {
		t.Error("[P0] deleting a user that still has branch associations must be restricted")
	}
	if _, err := migrator.Exec(ctx, `DELETE FROM branches WHERE id = $1`, branchID); err == nil {
		t.Error("[P0] deleting a branch that still has teacher associations must be restricted")
	}
}

// ---------------------------------------------------------------------------
// AC 3 — the od_migrator/od_runtime privilege posture, WITHOUT explicit grants
// ---------------------------------------------------------------------------

// TestRuntimeRoleHasExactlyTheInheritedPrivilegesWithNoExplicitGrant is the
// actual proof that skipping explicit GRANT/REVOKE statements in 0008 was
// correct, not an assumption — mirroring the Story 2.2 review's privilege-guard
// pattern (0007_runtime_privilege_defaults.sql's ALTER DEFAULT PRIVILEGES rule
// should apply automatically to any table od_migrator creates after it).
func TestRuntimeRoleHasExactlyTheInheritedPrivilegesWithNoExplicitGrant(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedTeacherDatabase(t, ctx)
	userID := seedUser(t, ctx, migrator, "privilege-fixture@amrita.edu", "student")
	branchID := seedBranch(t, ctx, migrator, "CSE", "Computer Science")
	if _, err := migrator.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, userID, branchID); err != nil {
		t.Fatalf("seed association: %v", err)
	}

	var canSelect, canInsert, canUpdate, canDelete bool
	if err := migrator.QueryRow(ctx, `
		SELECT has_table_privilege('od_runtime', 'user_branches', 'SELECT'),
		       has_table_privilege('od_runtime', 'user_branches', 'INSERT'),
		       has_table_privilege('od_runtime', 'user_branches', 'UPDATE'),
		       has_table_privilege('od_runtime', 'user_branches', 'DELETE')`).
		Scan(&canSelect, &canInsert, &canUpdate, &canDelete); err != nil {
		t.Fatalf("[P0] read privileges: %v", err)
	}
	if !canSelect || !canInsert || !canUpdate {
		t.Errorf("[P0] od_runtime must hold SELECT, INSERT and UPDATE via the 0007 default — got select=%v insert=%v update=%v", canSelect, canInsert, canUpdate)
	}
	if canDelete {
		t.Error("[P0] od_runtime must NOT hold DELETE on user_branches — no hard delete of a teacher's branch association")
	}

	// Behavioral proof, not just the catalog check above.
	if _, err := runtime.Exec(ctx, `DELETE FROM user_branches WHERE user_id = $1`, userID); err == nil {
		t.Error("[P0] od_runtime must not be able to delete a user_branches row")
	}
	for _, statement := range []string{
		`ALTER TABLE user_branches ADD COLUMN injected text`,
		`DROP TABLE user_branches`,
		`CREATE INDEX injected_idx ON user_branches (branch_id)`,
	} {
		if _, err := runtime.Exec(ctx, statement); err == nil {
			t.Errorf("[P0] od_runtime must not be able to run DDL: %q succeeded", statement)
		}
	}
	if _, err := runtime.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id) VALUES ($1, $2)`, userID, seedBranch(t, ctx, migrator, "ECE", "Electronics")); err != nil {
		t.Errorf("[P0] od_runtime must still hold INSERT: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC 3 — GetUserByEmail's deterministic email-collision resolution
// ---------------------------------------------------------------------------

// TestGetUserByEmailPicksTheMostRecentlyActiveRowOnACollision proves D5/AC 3's
// documented default at the SQL boundary this story's query.sql will wrap:
// users.email is NOT UNIQUE (0003's own comment), so a recycled address across
// two identities must resolve deterministically rather than by whatever order
// Postgres happens to return rows.
func TestGetUserByEmailPicksTheMostRecentlyActiveRowOnACollision(t *testing.T) {
	ctx := testContext(t)
	migrator, _, _ := isolatedTeacherDatabase(t, ctx)
	shared := "collision-fixture@amrita.edu"

	older := seedUserWithActivity(t, ctx, migrator, shared, "student", timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	newer := seedUserWithActivity(t, ctx, migrator, shared, "student", timePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)))

	var resolved uuid.UUID
	if err := migrator.QueryRow(ctx, `
		SELECT id FROM users WHERE lower(email) = lower($1)
		ORDER BY last_login_at DESC NULLS LAST, created_at DESC LIMIT 1`, shared).Scan(&resolved); err != nil {
		t.Fatalf("[P1] GetUserByEmail-equivalent query: %v", err)
	}
	if resolved != newer {
		t.Errorf("[P1] resolved = %s, want the most-recently-active row %s (older=%s)", resolved, newer, older)
	}
}

// ---------------------------------------------------------------------------
// AC 1 + AC 4 — persistence through the service, and audit conservation
// ---------------------------------------------------------------------------

// TestTeacherCreateConservesExactlyOneAuditRowPerElevation mirrors
// categories_test.go's audit-conservation case: Δusers.role='teacher' must
// equal Δ teacher.profile_created audit rows, and every rejected create
// (unknown email, already-elevated, unknown branch) must contribute zero of
// either.
func TestTeacherCreateConservesExactlyOneAuditRowPerElevation(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedTeacherDatabase(t, ctx)
	adminID := seedUser(t, ctx, migrator, "teacher-admin@amrita.edu", "admin")
	studentID := seedUser(t, ctx, migrator, "elevate-me@amrita.edu", "student")
	seedUser(t, ctx, migrator, "already-teacher@amrita.edu", "teacher")
	branchOne := seedBranch(t, ctx, migrator, "CSE", "Computer Science")
	branchTwo := seedBranch(t, ctx, migrator, "ECE", "Electronics")
	categoryOne := seedTeacherCategory(t, ctx, migrator, "Teacher Category One")
	categoryTwo := seedTeacherCategory(t, ctx, migrator, "Teacher Category Two")

	actor := teachersvc.Actor{UserID: adminID.String(), Email: "TEACHER-ADMIN@amrita.edu", Role: "admin"}
	newService := func() *teachersvc.Service {
		return teachersvc.NewService(teachersvc.NewSQLCReader(runtime), store.New(runtime), teachersvc.NewSQLCWriter())
	}

	created, err := newService().Create(ctx, actor, teachersvc.CreateInput{
		Email:       "elevate-me@amrita.edu",
		BranchIDs:   []string{branchOne.String(), branchTwo.String()},
		CategoryIDs: []string{categoryOne.String(), categoryTwo.String()},
	})
	if err != nil {
		t.Fatalf("create teacher profile: %v", err)
	}
	if created.UserID != studentID.String() || len(created.Branches) != 2 {
		t.Fatalf("created = %#v, want the elevated student with both branches", created)
	}

	// Every rejected create must contribute NOTHING — not a role change and not
	// an audit row.
	rejections := []struct {
		name      string
		email     string
		branchIDs []string
		catIDs    []string
	}{
		{name: "unknown email", email: "never-signed-in@amrita.edu", branchIDs: []string{branchOne.String()}, catIDs: []string{categoryOne.String()}},
		{name: "already elevated", email: "already-teacher@amrita.edu", branchIDs: []string{branchOne.String()}, catIDs: []string{categoryOne.String()}},
		{name: "unknown branch", email: "elevate-me@amrita.edu", branchIDs: []string{uuid.NewString()}, catIDs: []string{categoryOne.String()}},
	}
	for _, tc := range rejections {
		if _, err := newService().Create(ctx, actor, teachersvc.CreateInput{Email: tc.email, BranchIDs: tc.branchIDs, CategoryIDs: tc.catIDs}); err == nil {
			t.Errorf("%s: create was accepted, want a rejection", tc.name)
		}
	}

	var teachers, auditRows int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'teacher'`).Scan(&teachers); err != nil {
		t.Fatalf("count teacher-role users: %v", err)
	}
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'teacher.profile_created'`).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	// alreadyTeacherID was seeded directly as 'teacher' (never through Create),
	// so it must not appear in the audit ledger — only the ONE real elevation.
	if teachers != 2 || auditRows != 1 {
		t.Fatalf("teacher-role users = %d, teacher.profile_created audit rows = %d, want 2 and 1", teachers, auditRows)
	}

	var targetsMatch bool
	if err := runtime.QueryRow(ctx, `
		SELECT count(*) = 1
		FROM audit_log AS audit
		JOIN users AS teacher ON audit.target_id = teacher.id::text
		WHERE audit.action = 'teacher.profile_created'
		  AND audit.target_table = 'users'
		  AND audit.actor_account = 'teacher-admin@amrita.edu'
		  AND teacher.id = $1`, studentID).Scan(&targetsMatch); err != nil {
		t.Fatalf("join audit row to the elevated user: %v", err)
	}
	if !targetsMatch {
		t.Error("the teacher.profile_created row must target the elevated user with the normalized actor")
	}

	var branchAssociations int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM user_branches WHERE user_id = $1`, studentID).Scan(&branchAssociations); err != nil {
		t.Fatalf("count branch associations: %v", err)
	}
	if branchAssociations != 2 {
		t.Errorf("branch associations = %d, want 2", branchAssociations)
	}
}

// TestAssignTeacherRoleReturnsZeroRowsAgainstANonStudentRoleRatherThanAnError
// proves D5/AC 2's race backstop directly at the SQL layer: UPDATE users SET
// role = 'teacher' ... WHERE id = @id AND role = 'student' RETURNING ... must
// return pgx.ErrNoRows (not an error) when the row's role is already
// non-student — proven here rather than only through the service's pre-check.
func TestAssignTeacherRoleReturnsZeroRowsAgainstANonStudentRoleRatherThanAnError(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedTeacherDatabase(t, ctx)
	teacherID := seedUser(t, ctx, migrator, "already-teacher@amrita.edu", "teacher")

	queries := sqlc.New(runtime)
	_, err := queries.AssignTeacherRole(ctx, pgtype.UUID{Bytes: teacherID, Valid: true})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("[P0] AssignTeacherRole against a non-student role = %v, want pgx.ErrNoRows", err)
	}

	var role string
	if err := runtime.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, teacherID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != "teacher" {
		t.Errorf("[P0] role = %q, want unchanged teacher", role)
	}
}

// TestRacedConcurrentElevationOfTheSameUserYieldsExactlyOneWinner runs two
// concurrent POSTs for the same student email: exactly one must succeed
// (role -> teacher, its branches persisted, one audit row); the other must
// receive ROLE_ALREADY_ASSIGNED and write nothing. The AssignTeacherRole
// AND role = 'student' guard is the real backstop; the service pre-check is
// only a courtesy that cannot itself prevent this race.
func TestRacedConcurrentElevationOfTheSameUserYieldsExactlyOneWinner(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedTeacherDatabase(t, ctx)
	adminID := seedUser(t, ctx, migrator, "race-admin@amrita.edu", "admin")
	seedUser(t, ctx, migrator, "race-fixture@amrita.edu", "student")
	branchID := seedBranch(t, ctx, migrator, "CSE", "Computer Science")
	categoryID := seedTeacherCategory(t, ctx, migrator, "Race Teacher Category")
	actor := teachersvc.Actor{UserID: adminID.String(), Email: "race-admin@amrita.edu", Role: "admin"}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := teachersvc.NewService(teachersvc.NewSQLCReader(runtime), store.New(runtime), teachersvc.NewSQLCWriter())
			_, err := svc.Create(ctx, actor, teachersvc.CreateInput{
				Email: "race-fixture@amrita.edu", BranchIDs: []string{branchID.String()}, CategoryIDs: []string{categoryID.String()},
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		var domainErr domain.DomainError
		if errors.As(err, &domainErr) && string(domainErr.Code) == "ROLE_ALREADY_ASSIGNED" {
			conflicts++
			continue
		}
		t.Fatalf("[P0] unexpected error from a racing elevation: %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("[P0] successes=%d conflicts=%d, want exactly one winner and one ROLE_ALREADY_ASSIGNED", successes, conflicts)
	}

	var teachers, auditRows, branchAssociations, categoryAssociations int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'teacher'`).Scan(&teachers); err != nil {
		t.Fatalf("count teacher-role users: %v", err)
	}
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'teacher.profile_created'`).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM user_branches`).Scan(&branchAssociations); err != nil {
		t.Fatalf("count branch associations: %v", err)
	}
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM user_category_assignments`).Scan(&categoryAssociations); err != nil {
		t.Fatalf("count category associations: %v", err)
	}
	if teachers != 1 || auditRows != 1 || branchAssociations != 1 || categoryAssociations != 1 {
		t.Fatalf("[P0] teachers=%d audit=%d branches=%d categories=%d, want exactly 1 of each — the loser must write nothing",
			teachers, auditRows, branchAssociations, categoryAssociations)
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// isolatedTeacherDatabase gives each case its own migrated, throwaway
// database — audit_log is append-only for every role including the owner, so
// a shared CI database would accumulate fixture rows forever.
func isolatedTeacherDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Pool, string) {
	t.Helper()
	superDSN := os.Getenv("SUPERUSER_DSN")
	migratorBase := os.Getenv("DATABASE_URL_MIGRATOR")
	runtimeBase := os.Getenv("DATABASE_URL_RUNTIME")
	if superDSN == "" || migratorBase == "" || runtimeBase == "" {
		t.Skip("SUPERUSER_DSN and both application DSNs are required for the isolated database test")
	}
	if _, err := exec.LookPath("goose"); err != nil {
		t.Skip("goose is required for the isolated database test")
	}

	super, err := pgxpool.New(ctx, superDSN)
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	if err := super.Ping(ctx); err != nil {
		super.Close()
		t.Fatalf("ping superuser: %v", err)
	}
	databaseName := "od_teacher_" + uuidNoDashes()
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := super.Exec(ctx, "CREATE DATABASE "+identifier+" OWNER od_migrator"); err != nil {
		super.Close()
		t.Fatalf("create isolated database: %v", err)
	}

	var migrator, runtime *pgxpool.Pool
	t.Cleanup(func() {
		if runtime != nil {
			runtime.Close()
		}
		if migrator != nil {
			migrator.Close()
		}
		cleanupCtx := context.Background()
		if _, err := super.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		super.Close()
	})

	migratorDSN := databaseDSN(t, migratorBase, databaseName)
	runtimeDSN := databaseDSN(t, runtimeBase, databaseName)
	runGoose(t, ctx, migratorDSN, "up")

	migrator = connectPool(t, ctx, migratorDSN, "migrator")
	runtime = connectPool(t, ctx, runtimeDSN, "runtime")
	return migrator, runtime, migratorDSN
}

func runGoose(t *testing.T, ctx context.Context, dsn, command string, args ...string) {
	t.Helper()
	migrations, err := filepath.Abs(filepath.Join("..", "..", "db", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations: %v", err)
	}
	argv := append([]string{"-dir", migrations, "postgres", dsn, command}, args...)
	cmd := exec.CommandContext(ctx, "goose", argv...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func databaseDSN(t *testing.T, base, database string) string {
	t.Helper()
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse database DSN: %v", err)
	}
	parsed.Path = "/" + database
	dsn := parsed.String()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("re-parse rewritten DSN: %v", err)
	}
	if config.ConnConfig.Database != database {
		t.Fatalf("rewritten DSN targets database %q, want %q", config.ConnConfig.Database, database)
	}
	return dsn
}

func connectPool(t *testing.T, ctx context.Context, dsn, label string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect %s: %v", label, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping %s: %v", label, err)
	}
	return pool
}

func seedUser(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, email, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, role)
		VALUES ($1, $2, $3, $4)`, id, "teacher-fixture-"+id.String(), email, role); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedUserWithActivity(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, email, role string, lastLoginAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, role, last_login_at)
		VALUES ($1, $2, $3, $4, $5)`, id, "teacher-collision-"+id.String(), email, role, lastLoginAt); err != nil {
		t.Fatalf("seed user with activity: %v", err)
	}
	return id
}

func seedBranch(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, code, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrator.Exec(ctx,
		`INSERT INTO branches (id, code, name) VALUES ($1, $2, $3)`, id, code, name); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	return id
}

func seedTeacherCategory(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	eventID := uuid.New()
	categoryID := uuid.New()
	if _, err := migrator.Exec(ctx,
		`INSERT INTO events (id, name, event_date, end_date, status)
		 VALUES ($1, $2, CURRENT_DATE, CURRENT_DATE, 'active')`, eventID, name+" Event"); err != nil {
		t.Fatalf("seed teacher category event: %v", err)
	}
	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (id, event_id, name) VALUES ($1, $2, $3)`, categoryID, eventID, name); err != nil {
		t.Fatalf("seed teacher category: %v", err)
	}
	return categoryID
}

func assertTableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if exists != want {
		t.Errorf("table %s exists = %v, want %v", table, exists, want)
	}
}

func timePtr(v time.Time) *time.Time { return &v }

func uuidNoDashes() string {
	id := uuid.New().String()
	out := make([]byte, 0, len(id))
	for _, r := range id {
		if r != '-' {
			out = append(out, byte(r))
		}
	}
	return string(out)
}

// asPgError keeps the errors.As ceremony out of the assertions above, for any
// future case that needs to inspect a constraint violation directly.
func asPgError(err error, target **pgconn.PgError) bool {
	for err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok {
			*target = pgErr
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
