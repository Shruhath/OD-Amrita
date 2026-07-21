//go:build integration

package eventtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestEventLifecyclePersistsAcrossReadersAndConservesAuditRows(t *testing.T) {
	ctx := testContext(t)
	migrator, runtime := isolatedEventDatabase(t, ctx)

	actorID, firstBranchID, secondBranchID := uuid.New(), uuid.New(), uuid.New()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, role)
		VALUES ($1, $2, 'event-admin@amrita.edu', 'admin')`, actorID, "event-admin-"+actorID.String()); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := migrator.Exec(ctx, `
		INSERT INTO branches (id, code, name) VALUES
			($1, 'CSE', 'Computer Science'),
			($2, 'ECE', 'Electronics')`, firstBranchID, secondBranchID); err != nil {
		t.Fatalf("seed branches: %v", err)
	}

	actor := eventsvc.Actor{UserID: actorID.String(), Email: "EVENT-ADMIN@amrita.edu", Role: "admin"}
	newService := func() *eventsvc.Service {
		return eventsvc.NewService(eventsvc.NewSQLCReader(runtime), store.New(runtime), eventsvc.NewSQLCWriter())
	}
	created, err := newService().Create(ctx, actor, eventsvc.CreateInput{
		Name:      "  Inter-Campus Robotics Challenge  ",
		EventDate: "2026-08-18",
		BranchIDs: []string{secondBranchID.String(), firstBranchID.String()},
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if created.Name != "Inter-Campus Robotics Challenge" || created.EventDate != "2026-08-18" || created.Status != eventsvc.StatusActive {
		t.Fatalf("created event = %#v", created)
	}
	if len(created.Branches) != 2 || created.Branches[0].Code != "CSE" || created.Branches[1].Code != "ECE" {
		t.Fatalf("created branches = %#v", created.Branches)
	}

	// A fresh reader is the reload oracle: no service- or request-local state can
	// satisfy this assertion.
	listed, err := newService().List(ctx, eventsvc.ListInput{Status: eventsvc.StatusAll, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list after create: %v", err)
	}
	if listed.Total != 1 || len(listed.Data) != 1 || listed.Data[0].ID != created.ID {
		t.Fatalf("list after create = %#v", listed)
	}

	archived, err := newService().Archive(ctx, actor, created.ID)
	if err != nil {
		t.Fatalf("archive event: %v", err)
	}
	if archived.Status != eventsvc.StatusArchived || archived.ArchivedAt == nil {
		t.Fatalf("archived event = %#v", archived)
	}
	firstArchivedAt := *archived.ArchivedAt

	// A retry is a real database no-op: it returns the persisted representation
	// and creates no extra ledger entry.
	repeated, err := newService().Archive(ctx, actor, created.ID)
	if err != nil {
		t.Fatalf("repeat archive: %v", err)
	}
	if repeated.Status != eventsvc.StatusArchived || repeated.ArchivedAt == nil || !repeated.ArchivedAt.Equal(firstArchivedAt) {
		t.Fatalf("repeat archive = %#v", repeated)
	}

	archivedPage, err := newService().List(ctx, eventsvc.ListInput{Status: eventsvc.StatusArchived, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list after archive: %v", err)
	}
	if archivedPage.Total != 1 || len(archivedPage.Data) != 1 || archivedPage.Data[0].ID != created.ID {
		t.Fatalf("archived page = %#v", archivedPage)
	}

	rows, err := runtime.Query(ctx, `
		SELECT action, actor_account, target_table, target_id, payload::text
		FROM audit_log
		WHERE target_table = 'events' AND target_id = $1
		ORDER BY created_at, action`, created.ID)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, account, table, targetID, payload string
		if err := rows.Scan(&action, &account, &table, &targetID, &payload); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		if account != "event-admin@amrita.edu" || table != "events" || targetID != created.ID || payload == "" {
			t.Errorf("audit context = %q %q %q %q", account, table, targetID, payload)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if len(actions) != 2 || actions[0] != string(domain.ActionEventCreated) || actions[1] != string(domain.ActionEventArchived) {
		t.Fatalf("audit actions = %#v, want exactly create + archive", actions)
	}
}

func isolatedEventDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Pool) {
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
	databaseName := "od_event_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	migrations, err := filepath.Abs(filepath.Join("..", "..", "db", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations: %v", err)
	}
	command := exec.CommandContext(ctx, "goose", "-dir", migrations, "postgres", migratorDSN, "up")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("migrate isolated database: %v\n%s", err, output)
	}

	migrator = connectPool(t, ctx, migratorDSN, "migrator")
	runtime = connectPool(t, ctx, runtimeDSN, "runtime")
	return migrator, runtime
}

func databaseDSN(t *testing.T, base, database string) string {
	t.Helper()
	config, err := pgxpool.ParseConfig(base)
	if err != nil {
		t.Fatalf("parse database DSN: %v", err)
	}
	config.ConnConfig.Database = database
	return config.ConnString()
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
