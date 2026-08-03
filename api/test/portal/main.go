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
	handlerodrecord "github.com/Shruhath/OD-Amrita/api/internal/handler/od_record"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	handlerteacher "github.com/Shruhath/OD-Amrita/api/internal/handler/teacher"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcmarking "github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	svcodrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	svcteacher "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	adminToken       = "portal-admin-token"
	studentToken     = "portal-student-token"
	coordinatorToken = "portal-coordinator-token"
	adminSubject     = "portal-admin-subject"
	studentSub       = "portal-student-subject"
	coordinatorSub   = "portal-coordinator-subject"
)

// The marking fixtures. The coordinator surface needs a principal that
// production never creates on its own — every first-time user is provisioned as
// `student`, and Story 0.7 is the supported way to elevate one — plus an event
// dated TODAY, because odrecord.ActiveSlot compares against events.event_date in
// Asia/Kolkata. An event dated any other day makes every slot assertion
// vacuously "no slot open", which is a suite that passes while proving nothing.
const (
	markingEventID    = "93000000-0000-4000-8000-000000000001"
	markingCategoryID = "94000000-0000-4000-8000-000000000001"
	markingEventName  = "Browser Marking Event"
)

// teacherFixtureEmail is the student-role row the teacher-profile browser
// journeys elevate (web/tests/integration/teacher-profiles.spec.ts). Reused
// exactly as-is on every run (no per-run suffix): D2 makes a second elevation
// of the same user a 409, so seed() resets its role back to 'student' and
// clears any prior branch associations on every start, matching the spec's
// own reasoning for why this fixture is stable rather than suffixed.
const teacherFixtureEmail = "teacher-fixture-student@amrita.edu"

// coordinatorFixtureEmail is the provisioned student row the coordinator
// assignment browser journeys promote. It is reset on every harness start so a
// rerun begins with the same eligible candidate state.
const coordinatorFixtureEmail = "coordinator-fixture-student@amrita.edu"

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
var fixtureEventPrefixes = []string{"Browser Persisted Event", "Browser Category Event", "Browser Coordinator Event"}

type fixedVerifier struct{}

func (fixedVerifier) Verify(_ context.Context, token string) (svcauth.VerifiedIdentity, error) {
	now := time.Now().UTC()
	switch token {
	case adminToken:
		return svcauth.VerifiedIdentity{Subject: adminSubject, Email: "portal-admin@amrita.edu", Name: "Portal Admin", SessionID: uuid.NewString(), IssuedAt: now}, nil
	case studentToken:
		return svcauth.VerifiedIdentity{Subject: studentSub, Email: "portal-student@amrita.edu", Name: "Portal Student", SessionID: uuid.NewString(), IssuedAt: now}, nil
	case coordinatorToken:
		return svcauth.VerifiedIdentity{Subject: coordinatorSub, Email: "portal-coordinator@amrita.edu", Name: "Portal Coordinator", SessionID: uuid.NewString(), IssuedAt: now}, nil
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
	eventReader := svcevent.NewSQLCReader(runtimePool)
	creditLedger := svcodrecord.NewSQLCCreditLedger(runtimePool)
	markingPolicy := svcmarking.NewPolicy(svcmarking.NewEventReader(eventReader))
	markService := svcodrecord.NewService(
		svcodrecord.NewSQLCReader(runtimePool),
		svcodrecord.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtimePool), cardKeyring),
		svcodrecord.NewSQLCWriter(),
		st,
		nil,
		svcmarking.NewMarkGuard(markingPolicy),
		creditLedger,
	)
	studentService.WithSearchReader(svcstudent.NewSQLCSearchReader(runtimePool))
	markingReads := svcmarking.NewReadService(
		markingPolicy,
		eventReader,
		studentService,
		svcodrecord.NewStatusService(svcodrecord.NewSQLCReader(runtimePool), svcodrecord.NewSQLCCounter(runtimePool), creditLedger, nil),
	)

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

	principalLoader := rbac.NewSQLCPrincipalLoader(runtimePool)
	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(principalLoader))
	handlerodrecord.RegisterRoutes(markAPI, markService)
	// Shared with cmd/api, exactly like the admin routes below: a harness that
	// mounts the marking reads on a different group than production turns a real
	// route-composition bug into a browser-journey 404 that reads as a frontend
	// defect.
	handlerodrecord.RegisterMarkingReadRoutes(markAPI, markingReads)

	admin := api.Group("/admin")
	admin.Use(rbac.RequireAdmin(principalLoader))
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

// campusLocation resolves the campus timezone the slot table is defined in.
// Asia/Kolkata is in the standard tzdata set; a machine without it cannot run
// the marking path correctly at all, so failing loudly beats silently marking in
// UTC and being 5h30m out.
func campusLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		log.Fatalf("load campus timezone: %v", err)
	}
	return loc
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
		DELETE FROM event_capture_credit_usage
		WHERE event_id IN (
			SELECT id FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		)
	`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete event capture credit usage: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM event_capture_credit_config
		WHERE event_id IN (
			SELECT id FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		)
	`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete event capture credit config: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM event_coordinator_assignments
		WHERE event_id IN (
			SELECT id FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		)
	`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete event coordinator assignments: %w", err)
	}
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
	// student_card_tokens and od_records both reference students ON DELETE
	// RESTRICT, so the students delete below fails — and the whole harness
	// refuses to start — if a previous run's populator journey sealed a card or
	// a marking journey wrote a record. Clear the dependents first so the
	// harness is restartable after any journey, not only after a clean one.
	if _, err := tx.Exec(ctx, `
			DELETE FROM od_records
			WHERE student_id IN (SELECT id FROM students WHERE roll_no LIKE 'CB.SC.U4CSE%')
		`); err != nil {
		return fmt.Errorf("delete student od record fixtures: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM student_card_tokens
			WHERE student_id IN (SELECT id FROM students WHERE roll_no LIKE 'CB.SC.U4CSE%')
		`); err != nil {
		return fmt.Errorf("delete student card token fixtures: %w", err)
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
			('90000000-0000-4000-8000-000000000002', $2, 'portal-student@amrita.edu', 'Portal Student', 'student'),
			('90000000-0000-4000-8000-000000000003', $3, 'portal-coordinator@amrita.edu', 'Portal Coordinator', 'coordinator')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role
	`, adminSubject, studentSub, coordinatorSub); err != nil {
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
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ('92000000-0000-4000-8000-000000000002', 'portal-coordinator-fixture-subject', $1, 'Coordinator Fixture', 'student')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = 'student'
	`, coordinatorFixtureEmail); err != nil {
		return fmt.Errorf("upsert coordinator fixture user: %w", err)
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

	// Card-populator roster fixtures. Seeded AFTER the CB.SC.U4CSE% delete above
	// so each harness start gets a known section, and after branches because
	// students.branch_id is a RESTRICT foreign key.
	//
	// Deliberately inserted out of roll order: the populator journey asserts the
	// DATABASE returns them roll-ordered, which an already-sorted fixture could
	// not prove.
	if _, err := tx.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES
			('95000000-0000-4000-8000-000000000002', 'CB.SC.U4CSE24002', 'Madhav Rao', '91000000-0000-4000-8000-000000000001', 'A', '2024'),
			('95000000-0000-4000-8000-000000000001', 'CB.SC.U4CSE24001', 'Ananya Rao', '91000000-0000-4000-8000-000000000001', 'A', '2024'),
			('95000000-0000-4000-8000-000000000003', 'CB.SC.U4CSE24003', 'Nikhil Menon', '91000000-0000-4000-8000-000000000001', 'B', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`); err != nil {
		return fmt.Errorf("upsert card populator students: %w", err)
	}

	// The marking fixtures, seeded LAST so branches and students already exist.
	//
	// event_date is TODAY in Asia/Kolkata, recomputed on every harness start. A
	// hardcoded date would go stale overnight and quietly turn every slot
	// assertion into "no slot open" — a suite that passes while proving nothing.
	// Outside 08:00-18:15 IST there is genuinely no open slot, and the real-stack
	// spec says so explicitly rather than pretending otherwise.
	campusNow := time.Now().In(campusLocation())
	today := time.Date(campusNow.Year(), campusNow.Month(), campusNow.Day(), 0, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, name, event_date, status)
		VALUES ($1, $2, $3, 'active')
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, event_date = EXCLUDED.event_date, status = 'active', archived_at = NULL
	`, markingEventID, markingEventName, today); err != nil {
		return fmt.Errorf("upsert marking event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO event_branches (event_id, branch_id)
		VALUES ($1, '91000000-0000-4000-8000-000000000001')
		ON CONFLICT DO NOTHING
	`, markingEventID); err != nil {
		return fmt.Errorf("associate marking event branch: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO categories (id, event_id, name)
		VALUES ($1, $2, 'Procession')
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
	`, markingCategoryID, markingEventID); err != nil {
		return fmt.Errorf("upsert marking category: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO event_coordinator_assignments (user_id, event_id, created_by_user_id)
		VALUES ('90000000-0000-4000-8000-000000000003', $1, '90000000-0000-4000-8000-000000000001')
		ON CONFLICT (user_id, event_id) DO NOTHING
	`, markingEventID); err != nil {
		return fmt.Errorf("upsert marking coordinator assignment: %w", err)
	}
	// Marks from a previous run would make the counter assertions cumulative, so
	// the journey starts from a known zero every time.
	if _, err := tx.Exec(ctx, `
			DELETE FROM od_records WHERE event_id = $1
	`, markingEventID); err != nil {
		return fmt.Errorf("clear marking od records: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM event_capture_credit_usage WHERE event_id = $1
	`, markingEventID); err != nil {
		return fmt.Errorf("clear marking capture credit usage: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM event_capture_credit_config WHERE event_id = $1
	`, markingEventID); err != nil {
		return fmt.Errorf("clear marking capture credit config: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
