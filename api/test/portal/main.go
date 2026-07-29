// Command portal serves the real event HTTP stack with deterministic identities
// for the browser/Postgres acceptance job. It is test infrastructure only: the
// production API continues to verify Supabase JWTs in cmd/api.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/config"
	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	handlercardtoken "github.com/Shruhath/OD-Amrita/api/internal/handler/cardtoken"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	handlerteacher "github.com/Shruhath/OD-Amrita/api/internal/handler/teacher"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	svcteacher "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	adminToken   = "portal-admin-token"
	studentToken = "portal-student-token"
	adminSubject = "portal-admin-subject"
	studentSub   = "portal-student-subject"
)

// teacherFixtureEmail is the student-role row the teacher-profile browser
// journeys elevate (web/tests/integration/teacher-profiles.spec.ts). Reused
// exactly as-is on every run (no per-run suffix): D2 makes a second elevation
// of the same user a 409, so seed() resets its role back to 'student' and
// clears any prior branch associations on every start, matching the spec's
// own reasoning for why this fixture is stable rather than suffixed.
const teacherFixtureEmail = "teacher-fixture-student@amrita.edu"

// fixtureEventPrefixes are the events the browser journeys create. seed()
// removes them (and their children) so a run is repeatable; keep this in step
// with web/tests/integration/*.spec.ts.
//
// These are PREFIXES. The browser journeys suffix these with a
// per-run token, because events carry no unique name constraint and the specs
// create their events through the UI: a second run against a long-lived harness
// would otherwise leave two identically-named events, making the row locators
// ambiguous and the audit-row counts wrong. Matching on the prefix means seed()
// still clears every previous run's leftovers.
var fixtureEventPrefixes = []string{"Browser Persisted Event", "Browser Category Event"}

type fixedVerifier struct{}

func (fixedVerifier) Verify(_ context.Context, token string) (svcauth.VerifiedIdentity, error) {
	now := time.Now().UTC()
	switch token {
	case adminToken:
		return svcauth.VerifiedIdentity{Subject: adminSubject, Email: "portal-admin@amrita.edu", Name: "Portal Admin", SessionID: uuid.NewString(), IssuedAt: now}, nil
	case studentToken:
		return svcauth.VerifiedIdentity{Subject: studentSub, Email: "portal-student@amrita.edu", Name: "Portal Student", SessionID: uuid.NewString(), IssuedAt: now}, nil
	default:
		return svcauth.VerifiedIdentity{}, domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid."}
	}
}

func main() {
	runtimeDSN := requiredEnv("DATABASE_URL_RUNTIME")
	migratorDSN := requiredEnv("DATABASE_URL_MIGRATOR")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runtimePool := connect(ctx, runtimeDSN, "runtime")
	defer runtimePool.Close()
	migratorPool := connect(ctx, migratorDSN, "migrator")
	if err := seed(ctx, migratorPool); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Fatal("seed acceptance database: timed out")
		}
		log.Fatal(fmt.Errorf("seed acceptance database: %w", err))
	}
	migratorPool.Close()

	st := store.New(runtimePool)
	userService := svcuser.NewService(st, nil, svcuser.NewSQLCProvisioner())
	auditor := svcauth.NewEventAuditor(st)
	eventService := svcevent.NewService(svcevent.NewSQLCReader(runtimePool), st, svcevent.NewSQLCWriter())
	teacherService := svcteacher.NewService(svcteacher.NewSQLCReader(runtimePool), st, svcteacher.NewSQLCWriter())
	studentService := svcstudent.NewService(svcstudent.NewSQLCReader(runtimePool), st, svcstudent.NewSQLCWriter())
	cardSeal, err := config.LoadCardSealFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cardKeyring, err := svccardtoken.NewKeyring(cardSeal.ActiveVersion, cardSeal.HMACKeys)
	if err != nil {
		log.Fatal(err)
	}
	cardTokenService := svccardtoken.NewService(svccardtoken.NewSQLCReader(runtimePool), svccardtoken.NewSQLCLedger(runtimePool), st, cardKeyring)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(errormw.Handler())
	router.Use(corsmw.Middleware([]string{"http://127.0.0.1:4174"}))
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(fixedVerifier{}, "amrita.edu", auditor))
	api.Use(mwauth.RevocationGuard(svcauth.NewSQLCRevocationChecker(runtimePool)))
	api.GET("/auth/me", handlerauth.MeHandler(userService, auditor))

	admin := api.Group("/admin")
	admin.Use(rbac.RequireAdmin(rbac.NewSQLCPrincipalLoader(runtimePool)))
	// Shared with cmd/api so this harness can never serve a different admin
	// route set than production — the divergence that turns a missing route into
	// a browser-journey 404 that reads as a frontend bug.
	handlerevent.RegisterAdminRoutes(admin, eventService)
	handlerteacher.RegisterAdminRoutes(admin, teacherService)
	handlerstudent.RegisterAdminRoutes(admin, studentService)
	handlercardtoken.RegisterAdminRoutes(admin, cardTokenService)

	port := os.Getenv("PORTAL_API_PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("portal acceptance API listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func connect(ctx context.Context, dsn, label string) *pgxpool.Pool {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect %s database: %v", label, err)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping %s database: %v", label, err)
	}
	return pool
}

func seed(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Every event the browser journeys create, matched by PREFIX so a previous
	// run's uniquely-suffixed fixtures are cleared too. Each spec owns a DISTINCT
	// prefix: two specs sharing one would make the row locators ambiguous and
	// double the audit-row counts the CI assertions check.
	if _, err := tx.Exec(ctx, `
		DELETE FROM event_branches
		WHERE event_id IN (
			SELECT id FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		)
	`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete event branches: %w", err)
	}
	// categories.event_id is ON DELETE RESTRICT, so an un-cleaned category from a
	// previous run makes the event delete below fail and the whole harness refuse
	// to start. This runs as od_migrator, which the archived-event guard exempts,
	// so it works even after a journey archives its fixture event.
	if _, err := tx.Exec(ctx, `
		DELETE FROM categories
		WHERE event_id IN (
			SELECT id FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		)
	`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete categories: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete events: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM student_roster_imports
			WHERE source_filename IN (
				'student-roster.csv',
				'mixed.csv',
				'students.csv',
				'mixed-students.csv'
			)
		`); err != nil {
		return fmt.Errorf("delete student roster imports: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM students
			WHERE roll_no LIKE 'CB.SC.U4CSE%'
		`); err != nil {
		return fmt.Errorf("delete student roster fixtures: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, supabase_subject, email, name, role)
			VALUES
			('90000000-0000-4000-8000-000000000001', $1, 'portal-admin@amrita.edu', 'Portal Admin', 'admin'),
			('90000000-0000-4000-8000-000000000002', $2, 'portal-student@amrita.edu', 'Portal Student', 'student')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role
	`, adminSubject, studentSub); err != nil {
		return fmt.Errorf("upsert users: %w", err)
	}
	// The teacher-profile browser journeys elevate this row (student -> teacher).
	// Reset its role back to student and clear any branch associations from a
	// previous run FIRST, then upsert — D2 makes a second elevation a 409, so a
	// stable, re-seeded fixture is what lets the spec re-run cleanly against a
	// long-lived harness (mirrors why this fixture is NOT suffixed per-run, unlike
	// the events/categories fixtures above).
	if _, err := tx.Exec(ctx, `
		DELETE FROM user_branches WHERE user_id = '92000000-0000-4000-8000-000000000001'
	`); err != nil {
		return fmt.Errorf("clear teacher fixture branch associations: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ('92000000-0000-4000-8000-000000000001', 'portal-teacher-fixture-subject', $1, 'Teacher Fixture', 'student')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = 'student'
	`, teacherFixtureEmail); err != nil {
		return fmt.Errorf("upsert teacher fixture user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO branches (id, code, name)
		VALUES
			('91000000-0000-4000-8000-000000000001', 'CSE', 'Computer Science and Engineering'),
			('91000000-0000-4000-8000-000000000002', 'ECE', 'Electronics and Communication Engineering')
		ON CONFLICT (id) DO UPDATE SET code = EXCLUDED.code, name = EXCLUDED.name
	`); err != nil {
		return fmt.Errorf("upsert branches: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
