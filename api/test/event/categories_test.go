//go:build integration

package eventtest

// categories_test.go — Story 2.2 coverage for AC 1-4, 6 and 8.
// REAL-POSTGRES integration: build tag `integration`.
//
// Invoked by: api-ci -> integration job
//   go test -tags integration -p 1 ./test/db/... ./test/store/... ./test/event/... ./test/roundtrip/...
//
// Most cases speak SQL directly to a real database, so they assert the
// migration's guarantees rather than the service's opinion of them.
//
// Isolation: every case runs against its own throwaway database that is dropped
// on cleanup, so the append-only audit_log never accumulates fixture rows in a
// shared CI database. Nothing here deletes an audit row, because nothing can.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// ---------------------------------------------------------------------------
// AC 3 + AC 8 — schema, constraints and the generalized guard
// ---------------------------------------------------------------------------

func TestCategoriesMigrationRoundTripsCleanlyAndRestoresTheBranchGuard(t *testing.T) {
	ctx := testContext(t)
	migrator, _, migratorDSN := isolatedCategoryDatabase(t, ctx)

	// Forward state: the generic guard exists and event_branches has been
	// re-pointed at it.
	assertFunctionExists(t, ctx, migrator, "require_active_event_for_child_mutation", true)
	assertFunctionExists(t, ctx, migrator, "require_active_event_for_branch_mutation", false)
	if got := triggerFunction(t, ctx, migrator, "event_branches"); got != "require_active_event_for_child_mutation" {
		t.Errorf("event_branches trigger uses %q, want the generalized guard", got)
	}

	// A down migration that does not restore 0005's exact state leaves the
	// database unable to roll forward again — the failure mode that makes an
	// incident worse at the moment you most need to revert.
	// down-to 5, not "down": this test is about 0006's down script, and a bare
	// `down` reverts whatever the NEWEST migration happens to be. Naming the
	// target version keeps it pointed at 0006 no matter what lands after it.
	runGoose(t, ctx, migratorDSN, "down-to", "5")

	assertTableExists(t, ctx, migrator, "categories", false)
	assertFunctionExists(t, ctx, migrator, "require_active_event_for_child_mutation", false)
	assertFunctionExists(t, ctx, migrator, "require_active_event_for_branch_mutation", true)
	if got := triggerFunction(t, ctx, migrator, "event_branches"); got != "require_active_event_for_branch_mutation" {
		t.Errorf("after down, event_branches trigger uses %q, want 0005's function restored", got)
	}

	runGoose(t, ctx, migratorDSN, "up")
	assertTableExists(t, ctx, migrator, "categories", true)
	assertFunctionExists(t, ctx, migrator, "require_active_event_for_child_mutation", true)
}

func TestCategoryNameIsUniquePerEventCaseInsensitivelyAndTrimNormalized(t *testing.T) {
	ctx := testContext(t)
	migrator, _, _ := isolatedCategoryDatabase(t, ctx)
	firstEvent := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")
	secondEvent := seedActiveEvent(t, ctx, migrator, "Sports Day")

	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Procession')`, firstEvent); err != nil {
		t.Fatalf("insert first category: %v", err)
	}

	// The unique index — not the service pre-check — is the guarantee. Each of
	// these is the same category to a human, and two concurrent creates would
	// race straight past any SELECT-then-INSERT check in Go.
	for _, duplicate := range []string{"Procession", "procession", "PROCESSION", "  Procession  ", "  pRoCeSsIoN"} {
		_, err := migrator.Exec(ctx,
			`INSERT INTO categories (event_id, name) VALUES ($1, $2)`, firstEvent, duplicate)
		if err == nil {
			t.Errorf("insert %q was accepted; the unique index must reject it", duplicate)
			continue
		}
		var pgErr *pgconn.PgError
		if !asPgError(err, &pgErr) || pgErr.Code != "23505" {
			t.Errorf("insert %q error = %v, want a 23505 unique violation", duplicate, err)
			continue
		}
		// The service maps this constraint name specifically, so the name is
		// part of the contract between the migration and the service.
		if pgErr.ConstraintName != "uq_categories_event_id_name" {
			t.Errorf("insert %q violated %q, want uq_categories_event_id_name", duplicate, pgErr.ConstraintName)
		}
	}

	// AC 3: the same name under a DIFFERENT event is legitimate and common —
	// every event has a "Procession".
	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Procession')`, secondEvent); err != nil {
		t.Errorf("the same name under a different event must be allowed: %v", err)
	}

	// A blank or whitespace-only name is not a name.
	for _, blank := range []string{"", "   ", "\t"} {
		if _, err := migrator.Exec(ctx,
			`INSERT INTO categories (event_id, name) VALUES ($1, $2)`, secondEvent, blank); err == nil {
			t.Errorf("blank name %q was accepted; the CHECK constraint must reject it", blank)
		}
	}
}

func TestCategoryBelongsToExactlyOneExistingEvent(t *testing.T) {
	ctx := testContext(t)
	migrator, _, _ := isolatedCategoryDatabase(t, ctx)
	eventID := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")

	// A category under an event that does not exist is orphaned data the read
	// path could never scope correctly.
	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Orphan')`, uuid.New()); err == nil {
		t.Error("a category under an unknown event must violate the foreign key")
	}

	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Procession')`, eventID); err != nil {
		t.Fatalf("insert category: %v", err)
	}

	// ON DELETE RESTRICT: coordinator assignments, OD records, disputes and
	// tallies will all reference categories, so an event may not be deleted out
	// from under them even by the migrator.
	if _, err := migrator.Exec(ctx, `DELETE FROM events WHERE id = $1`, eventID); err == nil {
		t.Error("deleting an event that still has categories must be restricted")
	}
}

func TestCategoryListOrderIsDeterministic(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	eventID := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")

	// Inserted deliberately out of order, with a duplicate-prefix pair, so a
	// missing tiebreaker shows up as an unstable result rather than a lucky one.
	for _, name := range []string{"Stage Setup", "Procession", "Registration", "Stage Crew"} {
		if _, err := migrator.Exec(ctx,
			`INSERT INTO categories (event_id, name) VALUES ($1, $2)`, eventID, name); err != nil {
			t.Fatalf("insert %q: %v", name, err)
		}
	}

	want := []string{"Procession", "Registration", "Stage Crew", "Stage Setup"}
	// Repeat the read: an ORDER BY without a total tiebreaker can return rows in
	// a different order on a later plan, which is how "it worked yesterday"
	// pagination bugs are born.
	for attempt := range 3 {
		rows, err := runtime.Query(ctx,
			`SELECT name FROM categories WHERE event_id = $1 ORDER BY name ASC, id ASC`, eventID)
		if err != nil {
			t.Fatalf("attempt %d list categories: %v", attempt, err)
		}
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("scan: %v", err)
			}
			got = append(got, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate: %v", err)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("attempt %d order = %v, want %v", attempt, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// AC 4 — the archived event is a closed aggregate at the DATABASE boundary
// ---------------------------------------------------------------------------

func TestArchivedEventRefusesCategoryWritesEvenWhenTheServiceIsBypassed(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	activeEvent := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")
	archivedEvent := seedArchivedEvent(t, ctx, migrator, "Last Year Onam")

	// This is the assertion AC 4 actually claims. It issues the write DIRECTLY
	// on the runtime pool, so a passing result cannot be credited to the service
	// pre-check — only the trigger can produce it.
	_, err := runtime.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Procession')`, archivedEvent)
	if err == nil {
		t.Fatal("inserting a category under an archived event must be refused by the database")
	}
	var pgErr *pgconn.PgError
	if !asPgError(err, &pgErr) || pgErr.Code != "55000" {
		t.Fatalf("error = %v, want SQLSTATE 55000", err)
	}

	// The guard must not be a blanket denial: active events still accept writes.
	if _, err := runtime.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Procession')`, activeEvent); err != nil {
		t.Fatalf("an active event must still accept categories: %v", err)
	}

	// Re-parenting a category INTO an archived event is the same violation
	// wearing a different hat, and it is the case a naive INSERT-only trigger
	// misses. 0005's guard checked both old and new event ids; the generalized
	// version must keep doing so.
	if _, err := runtime.Exec(ctx,
		`UPDATE categories SET event_id = $1 WHERE event_id = $2`, archivedEvent, activeEvent); err == nil {
		t.Error("re-parenting a category into an archived event must be refused")
	}

	// The migrator keeps its escape hatch, or cleanup and future migrations
	// would be unable to touch historical rows.
	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Migrator Fixup')`, archivedEvent); err != nil {
		t.Errorf("od_migrator must retain the escape hatch: %v", err)
	}
}

func TestRePointedGuardLeavesEventBranchImmutabilityUnchanged(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	archivedEvent := seedArchivedEvent(t, ctx, migrator, "Last Year Onam")
	activeEvent := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")
	branchID := seedBranch(t, ctx, migrator, "CSE", "Computer Science")

	// Story 2.1 proved this behavior; 0006 re-points the trigger at a shared
	// function. This case is the safety net for that refactor — the one change
	// in this story that can silently weaken an existing guarantee.
	_, err := runtime.Exec(ctx,
		`INSERT INTO event_branches (event_id, branch_id) VALUES ($1, $2)`, archivedEvent, branchID)
	if err == nil {
		t.Fatal("event_branches under an archived event must still be refused after re-pointing")
	}
	var pgErr *pgconn.PgError
	if !asPgError(err, &pgErr) || pgErr.Code != "55000" {
		t.Fatalf("error = %v, want SQLSTATE 55000", err)
	}

	if _, err := runtime.Exec(ctx,
		`INSERT INTO event_branches (event_id, branch_id) VALUES ($1, $2)`, activeEvent, branchID); err != nil {
		t.Fatalf("an active event must still accept branch membership: %v", err)
	}
	if _, err := runtime.Exec(ctx,
		`UPDATE event_branches SET event_id = $1 WHERE event_id = $2`, archivedEvent, activeEvent); err == nil {
		t.Error("re-parenting branch membership into an archived event must still be refused")
	}
}

// ---------------------------------------------------------------------------
// AC 8 — runtime role stays DML-only on the new table
// ---------------------------------------------------------------------------

func TestRuntimeRoleCannotDeleteCategoriesOrChangeTheirSchema(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	eventID := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")
	if _, err := migrator.Exec(ctx,
		`INSERT INTO categories (event_id, name) VALUES ($1, 'Procession')`, eventID); err != nil {
		t.Fatalf("seed category: %v", err)
	}

	// Categories are referenced by later stories' records, so the same
	// no-hard-delete posture events already have applies from day one — and it is
	// enforced by a REVOKE, not by remembering not to write the query.
	if _, err := runtime.Exec(ctx, `DELETE FROM categories WHERE event_id = $1`, eventID); err == nil {
		t.Error("od_runtime must not be able to delete a category")
	}

	for _, statement := range []string{
		`ALTER TABLE categories ADD COLUMN injected text`,
		`DROP TABLE categories`,
		`CREATE INDEX injected_idx ON categories (name)`,
	} {
		if _, err := runtime.Exec(ctx, statement); err == nil {
			t.Errorf("od_runtime must not be able to run DDL: %q succeeded", statement)
		}
	}

	// The privileges are asserted directly too, so a future migration that
	// re-grants DELETE fails here rather than silently widening the blast radius.
	var canDelete, canInsert, canSelect bool
	if err := migrator.QueryRow(ctx, `
		SELECT has_table_privilege('od_runtime', 'categories', 'DELETE'),
		       has_table_privilege('od_runtime', 'categories', 'INSERT'),
		       has_table_privilege('od_runtime', 'categories', 'SELECT')`).
		Scan(&canDelete, &canInsert, &canSelect); err != nil {
		t.Fatalf("read privileges: %v", err)
	}
	if canDelete {
		t.Error("od_runtime must not hold DELETE on categories")
	}
	if !canInsert || !canSelect {
		t.Error("od_runtime must hold SELECT and INSERT on categories")
	}
}

// ---------------------------------------------------------------------------
// AC 1 + AC 6 — persistence through the service, and audit conservation
// ---------------------------------------------------------------------------

func TestCategoryCreateConservesExactlyOneAuditRowPerPersistedCategory(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	actorID := seedAdmin(t, ctx, migrator, "event-admin@amrita.edu")
	activeEvent := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")
	archivedEvent := seedArchivedEvent(t, ctx, migrator, "Last Year Onam")

	actor := eventsvc.Actor{UserID: actorID.String(), Email: "EVENT-ADMIN@amrita.edu", Role: "admin"}
	newService := func() *eventsvc.Service {
		return eventsvc.NewService(eventsvc.NewSQLCReader(runtime), store.New(runtime), eventsvc.NewSQLCWriter())
	}

	// AC 2: repeating the command is how several categories are added, and
	// each one is a separate persisted row.
	for _, name := range []string{"Procession", "Stage Setup", "Registration"} {
		if _, err := newService().CreateCategory(ctx, actor, eventsvc.CreateCategoryInput{
			EventID: activeEvent.String(), Name: name,
		}); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
	}

	// A FRESH service is the reload oracle for AC 1: no request- or
	// service-local state can satisfy this read.
	listed, err := newService().ListCategories(ctx, actor, activeEvent.String())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed = %#v, want three persisted categories", listed)
	}
	// The server's promise is name ASC, id ASC — not insertion order.
	if listed[0].Name != "Procession" || listed[1].Name != "Registration" || listed[2].Name != "Stage Setup" {
		t.Errorf("listed = %#v, want the rows in server order", listed)
	}

	// Every rejected create must contribute NOTHING — not a category row and
	// not an audit row. This is the half a happy-path test never checks.
	rejections := []struct {
		name    string
		eventID string
		input   string
	}{
		{name: "duplicate", eventID: activeEvent.String(), input: "  procession  "},
		{name: "archived", eventID: archivedEvent.String(), input: "Procession"},
		{name: "unknown event", eventID: uuid.NewString(), input: "Procession"},
		{name: "blank", eventID: activeEvent.String(), input: "   "},
	}
	for _, tc := range rejections {
		if _, err := newService().CreateCategory(ctx, actor, eventsvc.CreateCategoryInput{
			EventID: tc.eventID, Name: tc.input,
		}); err == nil {
			t.Errorf("%s: create was accepted, want a rejection", tc.name)
		}
	}

	// The conservation property: business rows and ledger rows move together
	// and only together.
	var categories, auditRows int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM categories`).Scan(&categories); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'category.created'`).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if categories != 3 || auditRows != 3 {
		t.Fatalf("categories = %d, category.created audit rows = %d, want 3 and 3", categories, auditRows)
	}

	// The ledger must name the right table and the right rows.
	var targetsMatch bool
	if err := runtime.QueryRow(ctx, `
		SELECT count(*) = 3
		FROM audit_log AS audit
		JOIN categories AS category ON audit.target_id = category.id::text
		WHERE audit.action = 'category.created'
		  AND audit.target_table = 'categories'
		  AND audit.actor_account = 'event-admin@amrita.edu'`).Scan(&targetsMatch); err != nil {
		t.Fatalf("join audit rows to categories: %v", err)
	}
	if !targetsMatch {
		t.Error("every category.created row must target an existing category with the normalized actor")
	}

	// AC 3 on real rows: the same name under a DIFFERENT event is legitimate,
	// and the read stays scoped to the event that was asked for.
	secondEvent := seedActiveEvent(t, ctx, migrator, "Sports Day")
	if _, err := newService().CreateCategory(ctx, actor, eventsvc.CreateCategoryInput{
		EventID: secondEvent.String(), Name: "Procession",
	}); err != nil {
		t.Fatalf("the same name under another event must be allowed: %v", err)
	}
	scoped, err := newService().ListCategories(ctx, actor, secondEvent.String())
	if err != nil {
		t.Fatalf("list second event categories: %v", err)
	}
	if len(scoped) != 1 || scoped[0].EventID != secondEvent.String() {
		t.Errorf("listed = %#v, want only the second event's single category", scoped)
	}

	// Archived is a CLOSED aggregate, not a hidden one: the read must survive.
	if _, err := newService().ListCategories(ctx, actor, archivedEvent.String()); err != nil {
		t.Errorf("listing an archived event's categories must succeed: %v", err)
	}
}

// The raced duplicate, end to end through the real service and the real index.
//
// This was previously proven in two halves that never met: a unit test that
// hand-built a *pgconn.PgError with the constraint name, and an integration test
// that provoked the real 23505 with raw SQL. Neither one runs Service.Create
// PAST its pre-check and INTO the constraint, so renaming the index in a later
// migration would leave both green while production started answering 500 for an
// ordinary duplicate name.
//
// The pre-check is defeated the way concurrency defeats it: the row is inserted
// behind the service's back, after the service has already read the list.
func TestRacedDuplicateReachesTheIndexAndStillReturnsTheTypedFieldError(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	actorID := seedAdmin(t, ctx, migrator, "event-admin@amrita.edu")
	activeEvent := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")
	actor := eventsvc.Actor{UserID: actorID.String(), Email: "event-admin@amrita.edu", Role: "admin"}

	// A reader that inserts the competing row AFTER answering the duplicate
	// pre-check with an empty list — the exact interleaving of two admins
	// submitting the same name at the same moment.
	racing := &racingCategoryReader{
		Reader: eventsvc.NewSQLCReader(runtime),
		onList: func() {
			if _, err := migrator.Exec(ctx,
				`INSERT INTO categories (event_id, name) VALUES ($1, $2)`,
				activeEvent, "Procession",
			); err != nil {
				t.Fatalf("seed the racing row: %v", err)
			}
		},
	}
	svc := eventsvc.NewService(racing, store.New(runtime), eventsvc.NewSQLCWriter())

	_, err := svc.CreateCategory(ctx, actor, eventsvc.CreateCategoryInput{
		EventID: activeEvent.String(), Name: "  procession  ",
	})
	if err == nil {
		t.Fatal("[P0] a raced duplicate must be rejected, not persisted twice")
	}

	// The loser of the race is owed the same answer as the caller who simply
	// typed a name that already existed — never an opaque 500.
	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("[P0] error = %v, want a typed DomainError", err)
	}
	if string(domainErr.Code) != "VALIDATION_FAILED" {
		t.Errorf("[P0] code = %s, want VALIDATION_FAILED", domainErr.Code)
	}
	if len(domainErr.Details) != 1 ||
		domainErr.Details[0].Field != "name" || domainErr.Details[0].Code != "duplicate" {
		t.Errorf("[P0] details = %#v, want a single name/duplicate field detail", domainErr.Details)
	}

	// Conservation still holds: the rejected create wrote no second category and
	// no audit row.
	var categories int
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM categories WHERE event_id = $1`, activeEvent,
	).Scan(&categories); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if categories != 1 {
		t.Errorf("[P0] categories = %d, want the single raced row", categories)
	}

	var audits int
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'category.created'`,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 0 {
		t.Errorf("[P0] audit rows = %d, want none — the raced insert bypassed the service", audits)
	}
}

// racingCategoryReader delegates everything to the real reader and runs a hook
// immediately after the duplicate pre-check has read the list.
type racingCategoryReader struct {
	eventsvc.Reader
	onList func()
	fired  bool
}

func (r *racingCategoryReader) ListCategories(ctx context.Context, eventID uuid.UUID) ([]eventsvc.Category, error) {
	existing, err := r.Reader.ListCategories(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !r.fired && r.onList != nil {
		r.fired = true
		r.onList()
	}
	return existing, nil
}

func TestFailedAuditInsertRollsTheCategoryWriteBack(t *testing.T) {
	// Mirrors api/test/event/audit_rollback_test.go for the category path. The
	// business row and its ledger entry share one transaction, so a ledger
	// failure must leave NO category behind. A category without an audit row
	// is exactly the divergence NFR20/NFR24/NFR25 exist to prevent.
	ctx := testContext(t)
	migrator, runtime, _ := isolatedCategoryDatabase(t, ctx)
	actorID := seedAdmin(t, ctx, migrator, "event-admin@amrita.edu")
	eventID := seedActiveEvent(t, ctx, migrator, "Onam Celebrations")

	boom := errors.New("injected audit failure")
	failing := store.New(runtime, store.WithAuditInserter(func(context.Context, pgx.Tx, store.AuditEvent) error {
		return boom
	}))
	svc := eventsvc.NewService(eventsvc.NewSQLCReader(runtime), failing, eventsvc.NewSQLCWriter())

	_, err := svc.CreateCategory(ctx, eventsvc.Actor{
		UserID: actorID.String(), Email: "event-admin@amrita.edu", Role: "admin",
	}, eventsvc.CreateCategoryInput{EventID: eventID.String(), Name: "Procession"})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the injected audit failure to fail the whole create", err)
	}

	var categories int
	if err := runtime.QueryRow(ctx,
		`SELECT count(*) FROM categories WHERE event_id = $1`, eventID).Scan(&categories); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if categories != 0 {
		t.Errorf("categories after a failed audit = %d, want 0 (the insert must roll back)", categories)
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// isolatedCategoryDatabase gives each case its own migrated database and returns
// the migrator pool, the runtime pool, and the migrator DSN (which the migration
// round-trip case needs in order to drive goose). It intentionally mirrors
// isolatedEventDatabase in lifecycle_test.go and reuses its databaseDSN and
// connectPool helpers rather than re-implementing them.
func isolatedCategoryDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Pool, string) {
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
	databaseName := "od_category_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		// Dropping the whole database is the only cleanup that works here:
		// audit_log is append-only for every role including the owner.
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

// runGoose runs one goose command. Extra arguments are passed through so a test
// can say `down-to`, `<version>` — targeting a VERSION rather than saying "one
// step down", which silently starts meaning something else the moment a later
// migration is added.
func runGoose(t *testing.T, ctx context.Context, dsn, command string, args ...string) {
	t.Helper()
	migrations, err := filepath.Abs(filepath.Join("..", "..", "db", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations: %v", err)
	}
	argv := append([]string{"-dir", migrations, "postgres", dsn, command}, args...)
	cmd := exec.CommandContext(ctx, "goose", argv...)
	if output, err := cmd.CombinedOutput(); err != nil {
		// A PL/pgSQL body without -- +goose StatementBegin/StatementEnd fencing
		// fails here with SQLSTATE 42601. That is what made 0005 unrunnable, and
		// it is the single most likely way to break 0006.
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func seedAdmin(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, role)
		VALUES ($1, $2, $3, 'admin')`, id, "category-admin-"+id.String(), email); err != nil {
		t.Fatalf("seed admin: %v", err)
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

func seedActiveEvent(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrator.Exec(ctx,
		`INSERT INTO events (id, name, event_date) VALUES ($1, $2, '2026-08-18')`, id, name); err != nil {
		t.Fatalf("seed active event: %v", err)
	}
	return id
}

func seedArchivedEvent(t *testing.T, ctx context.Context, migrator *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	actorID := seedAdmin(t, ctx, migrator, "archiver-"+uuid.NewString()+"@amrita.edu")
	id := uuid.New()
	// events_archive_state_ck requires archived_at and archived_by_user_id to be
	// set together with the status, so the fixture must be a coherent archived
	// row rather than a status flip.
	if _, err := migrator.Exec(ctx, `
		INSERT INTO events (id, name, event_date, status, archived_at, archived_by_user_id)
		VALUES ($1, $2, '2025-08-18', 'archived', now(), $3)`, id, name, actorID); err != nil {
		t.Fatalf("seed archived event: %v", err)
	}
	return id
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

func assertFunctionExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, function string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_proc AS proc
			JOIN pg_namespace AS namespace ON namespace.oid = proc.pronamespace
			WHERE namespace.nspname = 'public' AND proc.proname = $1)`, function).Scan(&exists); err != nil {
		t.Fatalf("check function %s: %v", function, err)
	}
	if exists != want {
		t.Errorf("function %s exists = %v, want %v", function, exists, want)
	}
}

func triggerFunction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) string {
	t.Helper()
	var function string
	if err := pool.QueryRow(ctx, `
		SELECT proc.proname
		FROM pg_trigger AS trigger
		JOIN pg_class AS class ON class.oid = trigger.tgrelid
		JOIN pg_proc AS proc ON proc.oid = trigger.tgfoid
		WHERE class.relname = $1 AND NOT trigger.tgisinternal
		LIMIT 1`, table).Scan(&function); err != nil {
		t.Fatalf("read %s trigger function: %v", table, err)
	}
	return function
}

// asPgError keeps the errors.As ceremony out of the assertions above.
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
