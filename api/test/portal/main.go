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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/config"
	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	handlercardtoken "github.com/Shruhath/OD-Amrita/api/internal/handler/cardtoken"
	handlerdispute "github.com/Shruhath/OD-Amrita/api/internal/handler/dispute"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	handlerodrecord "github.com/Shruhath/OD-Amrita/api/internal/handler/od_record"
	handlerpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/handler/publicattendance"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	handlerteacher "github.com/Shruhath/OD-Amrita/api/internal/handler/teacher"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcdispute "github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcmarking "github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	svcodrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	svcpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	svcteacher "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	adminToken       = "portal-admin-token"
	studentToken     = "portal-student-token"
	coordinatorToken = "portal-coordinator-token"
	teacherToken     = "portal-teacher-token"
	adminSubject     = "portal-admin-subject"
	studentSub       = "portal-student-subject"
	coordinatorSub   = "portal-coordinator-subject"
	teacherSub       = "portal-teacher-subject"
)

// teacherPrincipalID/teacherPrincipalEmail are the ALREADY-teacher-role
// principal Story 0.9's search-mark journeys sign in as, seeded with a CSE
// branch association below. This is distinct from teacherFixtureEmail further
// down, which stays role='student' between runs so the 2.3 elevation journeys
// have something to promote — the two must never collapse into one row.
const (
	teacherPrincipalID    = "90000000-0000-4000-8000-000000000004"
	teacherPrincipalEmail = "portal-teacher@amrita.edu"
)

// The marking fixtures. The coordinator surface needs a principal that
// production never creates on its own — every first-time user is provisioned as
// `student`, and Story 0.7 is the supported way to elevate one — plus an event
// whose RANGE covers today, because odrecord.ActiveSlot asks whether today falls
// between events.event_date and events.end_date in Asia/Kolkata. An event whose
// range excludes today makes every slot assertion vacuously "no slot open",
// which is a suite that passes while proving nothing.
//
// The range is yesterday -> tomorrow rather than today -> today on purpose: a
// single-day event would still pass if containment were implemented as the old
// date equality, so the harness would not notice the feature was missing.
const (
	markingEventID    = "93000000-0000-4000-8000-000000000001"
	markingCategoryID = "94000000-0000-4000-8000-000000000001"
	markingEventName  = "Browser Marking Event"
)

// The Story 0.10 public-dashboard fixtures.
//
// This journey READS and never marks, so unlike every other marking fixture
// these od_records have to be pre-seeded — and seeded LAST, after both of
// seed()'s od_records deletes (the CB.SC.U4CSE%/ECE% sweep near the top and the
// marking-event sweep near the bottom), or they are wiped before the first test.
//
// Both events are dated TODAY on purpose. The dashboard groups by event date, so
// putting them on the same day is what makes one twelve-slot list rather than
// two — which is both the realistic shape (a festival day with two events) and
// what keeps "the row for slot N" unambiguous.
//
// The students are disjoint from every pool another spec owns: 24001-003 belong
// to coordinator-marking, 24004-005 to capture-credits, 24006-007 to
// teacher-admin-attendance. Story 0.9 lost CI rounds to exactly this collision.
const (
	publicEventID          = "93000000-0000-4000-8000-000000000002"
	publicCategoryID       = "94000000-0000-4000-8000-000000000002"
	publicEventName        = "Browser Public Event"
	publicArchivedEventID  = "93000000-0000-4000-8000-000000000003"
	publicArchivedCategory = "94000000-0000-4000-8000-000000000003"
	publicArchivedName     = "Browser Public Archived Event"
	publicStudentID        = "95000000-0000-4000-8000-000000000009"
	publicStudentRoll      = "CB.SC.U4CSE24008"
	publicUnmarkedID       = "95000000-0000-4000-8000-000000000010"
	publicUnmarkedRoll     = "CB.SC.U4CSE24009"
	disputeEventID         = "93000000-0000-4000-8000-000000000004"
	disputeCategoryID      = "94000000-0000-4000-8000-000000000004"
	disputeEventName       = "Browser Dispute Event"
	disputeStudentID       = "95000000-0000-4000-8000-000000000011"
	disputeStudentRoll     = "CB.SC.U4CSE24010"

	// The multi-day fixtures own their OWN event id and their OWN student, both
	// disjoint from every pool above.
	//
	// A separate event rather than a reuse of the public-dashboard one because
	// web-deploy.yml asserts an EXACT record count for the two Story 0.10 event
	// ids: adding this journey's rows to those events would turn a conservation
	// gate red for a reason that has nothing to do with what it guards, and the
	// tempting fix would be to loosen the number.
	multiDayEventID     = "93000000-0000-4000-8000-000000000006"
	multiDayCategoryID  = "94000000-0000-4000-8000-000000000006"
	multiDayEventName   = "Browser Multi Day Event"
	multiDayStudentID   = "95000000-0000-4000-8000-000000000012"
	multiDayStudentRoll = "CB.SC.U4CSE24011"
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
	case teacherToken:
		return svcauth.VerifiedIdentity{Subject: teacherSub, Email: teacherPrincipalEmail, Name: "Portal Teacher", SessionID: uuid.NewString(), IssuedAt: now}, nil
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
	portalMarkReads := svcmarking.NewPortalMarkReadService(runtimePool)
	markService := svcodrecord.NewService(
		svcodrecord.NewSQLCReader(runtimePool),
		svcodrecord.NewCardTokenResolver(svccardtoken.NewSQLCLedger(runtimePool), cardKeyring),
		svcodrecord.NewSQLCWriter(),
		st,
		nil,
		svcmarking.NewRoleAwareMarkGuard(markingPolicy, runtimePool),
		creditLedger,
	)
	studentService.WithSearchReader(svcstudent.NewSQLCSearchReader(runtimePool))
	publicAttendanceService := svcpublicattendance.NewSQLCService(runtimePool)
	disputeRepo := svcdispute.NewSQLCRepository(runtimePool)
	disputeService := svcdispute.NewService(disputeRepo, disputeRepo, st, "amrita.edu")
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
	// Same self-reference as cmd/api: the preflight's allowed methods are read
	// back off the routes registered below, so the harness advertises exactly
	// what the real server does rather than a list that can drift from it.
	router.Use(corsmw.Middleware([]string{"http://127.0.0.1:4174"}, router))
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(fixedVerifier{}, "amrita.edu", auditor))
	api.Use(mwauth.RevocationGuard(svcauth.NewSQLCRevocationChecker(runtimePool)))
	api.GET("/auth/me", handlerauth.MeHandler(userService, auditor))

	principalLoader := rbac.NewSQLCPrincipalLoader(runtimePool)
	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(principalLoader))
	handlerodrecord.RegisterRoutes(markAPI, markService)
	handlerodrecord.RegisterPortalMarkRoutes(markAPI, portalMarkReads)
	// Mirrors cmd/api: the teacher-reachable live attendance read. A harness that
	// omits a production route is a harness whose green means less than it looks.
	handlerodrecord.RegisterPortalAttendanceRoutes(markAPI, portalMarkReads)
	handlerdispute.RegisterRoutes(markAPI, disputeService)
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
	handlerodrecord.RegisterAdminAttendanceRoutes(admin, portalMarkReads)
	handlerteacher.RegisterAdminRoutes(admin, teacherService)
	handlerstudent.RegisterAdminRoutes(admin, studentService)
	handlercardtoken.RegisterAdminRoutes(admin, cardTokenService)

	// The UNAUTHENTICATED public dashboard (Story 0.10), composed EXACTLY as
	// cmd/api composes it: a group off the ROOT router, never a child of `api`.
	// If this harness mounted it anywhere else, the real-stack spec's "answers an
	// anonymous request" assertion would be testing the harness's arrangement
	// rather than production's.
	//
	// The limit is raised far above the production default here on purpose: the
	// portal-integration job runs every spec from ONE client IP, so a realistic
	// 30/minute would let one suite's debounced typing throttle another's
	// assertions. The limiter's real behaviour is proven deterministically in
	// api/internal/middleware/ratelimit with an injected clock.
	publicLimiter, err := ratelimit.New(ratelimit.Config{PerMinute: 6000, Burst: 600})
	if err != nil {
		log.Fatal(err)
	}
	publicGroup := router.Group("/api/v1/public")
	publicGroup.Use(publicLimiter.Middleware())
	handlerpublicattendance.RegisterPublicRoutes(publicGroup, studentService, publicAttendanceService)
	publicDisputeLimiter, err := ratelimit.New(ratelimit.Config{PerMinute: 6000, Burst: 600})
	if err != nil {
		log.Fatal(err)
	}
	handlerdispute.RegisterPublicRoutes(publicGroup, portalPublicDisputeSurface{
		submitter: disputeService,
		events:    publicAttendanceService,
	}, publicDisputeLimiter.Middleware())

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
		DELETE FROM disputes
		WHERE event_id IN (
			SELECT id FROM events
			WHERE name LIKE ANY (SELECT prefix || '%' FROM unnest($1::text[]) AS prefix)
		)
	`, fixtureEventPrefixes); err != nil {
		return fmt.Errorf("delete event dispute fixtures: %w", err)
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
			DELETE FROM disputes
			WHERE student_id IN (
				SELECT id FROM students WHERE roll_no LIKE 'CB.SC.U4CSE%' OR roll_no LIKE 'CB.SC.U4ECE%'
			)
		`); err != nil {
		return fmt.Errorf("delete student dispute fixtures: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM od_records
			WHERE student_id IN (
				SELECT id FROM students WHERE roll_no LIKE 'CB.SC.U4CSE%' OR roll_no LIKE 'CB.SC.U4ECE%'
			)
		`); err != nil {
		return fmt.Errorf("delete student od record fixtures: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM student_card_tokens
			WHERE student_id IN (
				SELECT id FROM students WHERE roll_no LIKE 'CB.SC.U4CSE%' OR roll_no LIKE 'CB.SC.U4ECE%'
			)
		`); err != nil {
		return fmt.Errorf("delete student card token fixtures: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			DELETE FROM students
			WHERE roll_no LIKE 'CB.SC.U4CSE%' OR roll_no LIKE 'CB.SC.U4ECE%'
		`); err != nil {
		return fmt.Errorf("delete student roster fixtures: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, supabase_subject, email, name, role)
			VALUES
			('90000000-0000-4000-8000-000000000001', $1, 'portal-admin@amrita.edu', 'Portal Admin', 'admin'),
			('90000000-0000-4000-8000-000000000002', $2, 'portal-student@amrita.edu', 'Portal Student', 'student'),
			('90000000-0000-4000-8000-000000000003', $3, 'portal-coordinator@amrita.edu', 'Portal Coordinator', 'coordinator'),
			($4, $5, $6, 'Portal Teacher', 'teacher')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role
	`, adminSubject, studentSub, coordinatorSub, teacherPrincipalID, teacherSub, teacherPrincipalEmail); err != nil {
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
	// Story 0.9's teacher search-mark journeys need this principal already
	// branch-scoped to CSE, re-asserted on every start (not ON CONFLICT DO
	// NOTHING on a row that could go missing) so a run that ever removed it
	// does not silently leave the teacher with zero branches, which would turn
	// every "should succeed" assertion into an RBAC_FORBIDDEN red herring. Must
	// run AFTER the branches insert above — user_branches.branch_id is a real FK.
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_branches (user_id, branch_id)
		VALUES ($1, '91000000-0000-4000-8000-000000000001')
		ON CONFLICT DO NOTHING
	`, teacherPrincipalID); err != nil {
		return fmt.Errorf("associate teacher principal branch: %w", err)
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
			('95000000-0000-4000-8000-000000000003', 'CB.SC.U4CSE24003', 'Nikhil Menon', '91000000-0000-4000-8000-000000000001', 'B', '2024'),
			('95000000-0000-4000-8000-000000000004', 'CB.SC.U4CSE24004', 'Isha Nair', '91000000-0000-4000-8000-000000000001', 'A', '2024'),
			('95000000-0000-4000-8000-000000000005', 'CB.SC.U4CSE24005', 'Rahul Iyer', '91000000-0000-4000-8000-000000000001', 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`); err != nil {
		return fmt.Errorf("upsert card populator students: %w", err)
	}
	// An ECE student outside the seeded teacher principal's CSE branch, so
	// Story 0.9's real-stack "teacher outside student branch" journey has a
	// student to search for that must be refused rather than marked.
	if _, err := tx.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ('95000000-0000-4000-8000-000000000006', 'CB.SC.U4ECE24001', 'Vivek Pillai', '91000000-0000-4000-8000-000000000002', 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`); err != nil {
		return fmt.Errorf("upsert out-of-branch student fixture: %w", err)
	}
	// Dedicated to the 0.9 teacher search-mark journey — CB.SC.U4CSE24001-003
	// belongs to coordinator-marking.spec.ts and 24004-005 to
	// capture-credits.spec.ts; reusing either pool put two legitimate marks in
	// the same unfiltered admin attendance view and broke a strict-mode locator.
	// Two rolls, matching the "different student per retry" convention used
	// elsewhere, even though this config currently runs with retries: 0.
	if _, err := tx.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES
			('95000000-0000-4000-8000-000000000007', 'CB.SC.U4CSE24006', 'Teacher Fixture Student One', '91000000-0000-4000-8000-000000000001', 'A', '2024'),
			('95000000-0000-4000-8000-000000000008', 'CB.SC.U4CSE24007', 'Teacher Fixture Student Two', '91000000-0000-4000-8000-000000000001', 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`); err != nil {
		return fmt.Errorf("upsert teacher search-mark student fixtures: %w", err)
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
	rangeStart := today.AddDate(0, 0, -1)
	rangeEnd := today.AddDate(0, 0, 1)
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, name, event_date, end_date, status)
		VALUES ($1, $2, $3, $4, 'active')
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, event_date = EXCLUDED.event_date, end_date = EXCLUDED.end_date,
		    status = 'active', archived_at = NULL
	`, markingEventID, markingEventName, rangeStart, rangeEnd); err != nil {
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

	if err := seedPublicDashboard(ctx, tx, today); err != nil {
		return err
	}
	if err := seedDisputeJourney(ctx, tx); err != nil {
		return err
	}
	if err := seedMultiDayJourney(ctx, tx, today); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type portalPublicDisputeSurface struct {
	submitter interface {
		Submit(context.Context, svcdispute.SubmitInput) (svcdispute.Receipt, error)
	}
	events interface {
		PublicEvents(context.Context) (svcpublicattendance.EventCollection, error)
	}
}

func (s portalPublicDisputeSurface) Submit(ctx context.Context, input svcdispute.SubmitInput) (svcdispute.Receipt, error) {
	return s.submitter.Submit(ctx, input)
}

func (s portalPublicDisputeSurface) PublicEvents(ctx context.Context) (svcpublicattendance.EventCollection, error) {
	return s.events.PublicEvents(ctx)
}

func seedDisputeJourney(ctx context.Context, tx pgx.Tx) error {
	eventDate := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1, $2, 'Browser Dispute Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`, disputeStudentID, disputeStudentRoll); err != nil {
		return fmt.Errorf("upsert dispute student fixture: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, name, event_date, end_date, status)
		VALUES ($1, $2, $3, $3, 'active')
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, event_date = EXCLUDED.event_date, end_date = EXCLUDED.end_date,
		    status = 'active', archived_at = NULL, archived_by_user_id = NULL
	`, disputeEventID, disputeEventName, eventDate); err != nil {
		return fmt.Errorf("upsert dispute event fixture: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO event_branches (event_id, branch_id)
		VALUES ($1, '91000000-0000-4000-8000-000000000001')
		ON CONFLICT DO NOTHING
	`, disputeEventID); err != nil {
		return fmt.Errorf("associate dispute event branch: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO categories (id, event_id, name)
		VALUES ($1, $2, 'Procession')
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
	`, disputeCategoryID, disputeEventID); err != nil {
		return fmt.Errorf("upsert dispute category fixture: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM disputes WHERE event_id = $1`, disputeEventID); err != nil {
		return fmt.Errorf("clear dispute rows fixture: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM od_records WHERE event_id = $1`, disputeEventID); err != nil {
		return fmt.Errorf("clear dispute event od records: %w", err)
	}
	for _, slot := range []int{1, 2, 4} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO od_records
				(student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, server_received_at)
			VALUES ($1, $2, $3, $4, $5, 'present', 'search', '90000000-0000-4000-8000-000000000004', 'teacher', now())
		`, disputeStudentID, disputeEventID, disputeCategoryID, slot, eventDate); err != nil {
			return fmt.Errorf("seed dispute od record slot %d: %w", slot, err)
		}
	}
	return nil
}

// seedMultiDayJourney seeds one event spanning THREE days and one student
// marked on each of them.
//
// Three days rather than two because two is the smallest number that can be
// produced by accident: a grouping still keyed on the event would collapse all
// three to one heading, and a grouping keyed on receipt time would collapse them
// to one as well (the seed runs in a single transaction), so only a grouping on
// od_date yields three. The days are explicit, never derived from now(), or the
// assertion agrees with whatever the code chose.
//
// Runs LAST, after the CB.SC.U4CSE%% sweep and after every od_records delete
// above, for the same reason seedPublicDashboard does.
func seedMultiDayJourney(ctx context.Context, tx pgx.Tx, today time.Time) error {
	// The range brackets today so the event is live while the journey runs, and
	// every seeded day falls inside it — od_records carries a database trigger
	// that refuses a day outside the event's range.
	rangeStart := today.AddDate(0, 0, -1)
	rangeEnd := today.AddDate(0, 0, 1)

	if _, err := tx.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1, $2, 'Multi Day Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`, multiDayStudentID, multiDayStudentRoll); err != nil {
		return fmt.Errorf("upsert multi-day student fixture: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, name, event_date, end_date, status)
		VALUES ($1, $2, $3, $4, 'active')
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, event_date = EXCLUDED.event_date, end_date = EXCLUDED.end_date,
		    status = 'active', archived_at = NULL, archived_by_user_id = NULL
	`, multiDayEventID, multiDayEventName, rangeStart, rangeEnd); err != nil {
		return fmt.Errorf("upsert multi-day event fixture: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO event_branches (event_id, branch_id)
		VALUES ($1, '91000000-0000-4000-8000-000000000001')
		ON CONFLICT DO NOTHING
	`, multiDayEventID); err != nil {
		return fmt.Errorf("associate multi-day event branch: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO categories (id, event_id, name)
		VALUES ($1, $2, 'Multi Day Procession')
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
	`, multiDayCategoryID, multiDayEventID); err != nil {
		return fmt.Errorf("upsert multi-day category: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM od_records WHERE event_id = $1`, multiDayEventID); err != nil {
		return fmt.Errorf("clear multi-day od records: %w", err)
	}

	// The SAME slot on all three days. That is the FR10 change stated as a
	// fixture: before this story the unique index refused the second and third
	// rows outright, so a seed that varied the slot would still load against the
	// old schema and prove nothing.
	for _, day := range []time.Time{rangeStart, today, rangeEnd} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO od_records
				(student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, server_received_at)
			VALUES ($1, $2, $3, 6, $4, 'present', 'nfc', '90000000-0000-4000-8000-000000000003', 'coordinator', now())
		`, multiDayStudentID, multiDayEventID, multiDayCategoryID, day); err != nil {
			return fmt.Errorf("seed multi-day od record for %s: %w", day.Format("2006-01-02"), err)
		}
	}
	return nil
}

// seedPublicDashboard seeds the read-only Story 0.10 fixtures.
//
// Called at the END of seed(), after every od_records delete, because this
// journey never marks: its records must already exist when the first assertion
// runs, and an earlier insert would be swept by the CB.SC.U4CSE% cleanup.
//
// Slots 1, 2 and 4 of the public event are present and slot 3 is deliberately
// EMPTY. That gap is PRD Journey 3 verbatim — a student sees slots 1, 2 and 4
// and notices 3 is missing — and it is the whole reason the screen renders all
// twelve slots instead of only the marked ones.
func seedPublicDashboard(ctx context.Context, tx pgx.Tx, eventDate time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES
			($1, $2, 'Public Dashboard Student', '91000000-0000-4000-8000-000000000001', 'A', '2024'),
			($3, $4, 'Public Unmarked Student', '91000000-0000-4000-8000-000000000001', 'A', '2024')
		ON CONFLICT (id) DO UPDATE
		SET roll_no = EXCLUDED.roll_no, name = EXCLUDED.name, branch_id = EXCLUDED.branch_id,
		    section = EXCLUDED.section, batch = EXCLUDED.batch
	`, publicStudentID, publicStudentRoll, publicUnmarkedID, publicUnmarkedRoll); err != nil {
		return fmt.Errorf("upsert public dashboard student fixtures: %w", err)
	}

	// Both events are reset to ACTIVE first. The archived one is archived again
	// at the end: `events` blocks child mutations on an archived event, so its
	// od_records row has to be inserted while it is still active. seed() runs as
	// od_migrator, which the guard exempts, but the archive-state CHECK
	// constraint applies to every role — which is why status, archived_at and
	// archived_by_user_id always move together below.
	for _, event := range []struct{ id, name, categoryID, categoryName string }{
		{publicEventID, publicEventName, publicCategoryID, "Procession"},
		{publicArchivedEventID, publicArchivedName, publicArchivedCategory, "Cultural"},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (id, name, event_date, end_date, status)
			VALUES ($1, $2, $3, $3, 'active')
			ON CONFLICT (id) DO UPDATE
			SET name = EXCLUDED.name, event_date = EXCLUDED.event_date, end_date = EXCLUDED.end_date,
			    status = 'active', archived_at = NULL, archived_by_user_id = NULL
		`, event.id, event.name, eventDate); err != nil {
			return fmt.Errorf("upsert public event %s: %w", event.name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO event_branches (event_id, branch_id)
			VALUES ($1, '91000000-0000-4000-8000-000000000001')
			ON CONFLICT DO NOTHING
		`, event.id); err != nil {
			return fmt.Errorf("associate public event branch %s: %w", event.name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO categories (id, event_id, name)
			VALUES ($1, $2, $3)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
		`, event.categoryID, event.id, event.categoryName); err != nil {
			return fmt.Errorf("upsert public category %s: %w", event.name, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM od_records WHERE event_id = $1`, event.id); err != nil {
			return fmt.Errorf("clear public od records %s: %w", event.name, err)
		}
	}

	// Slot 3 is absent on purpose — see the doc comment.
	for _, record := range []struct {
		eventID    string
		categoryID string
		slot       int
	}{
		{publicEventID, publicCategoryID, 1},
		{publicEventID, publicCategoryID, 2},
		{publicEventID, publicCategoryID, 4},
		{publicArchivedEventID, publicArchivedCategory, 5},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO od_records
				(student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, server_received_at)
			VALUES ($1, $2, $3, $4, $5, 'present', 'nfc', '90000000-0000-4000-8000-000000000003', 'coordinator', now())
		`, publicStudentID, record.eventID, record.categoryID, record.slot, eventDate); err != nil {
			return fmt.Errorf("seed public od record slot %d: %w", record.slot, err)
		}
	}

	// Archived LAST, once its record exists.
	if _, err := tx.Exec(ctx, `
		UPDATE events
		SET status = 'archived', archived_at = now(), archived_by_user_id = '90000000-0000-4000-8000-000000000001'
		WHERE id = $1
	`, publicArchivedEventID); err != nil {
		return fmt.Errorf("archive public event: %w", err)
	}
	return nil
}
