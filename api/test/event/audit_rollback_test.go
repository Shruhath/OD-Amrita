//go:build integration

package eventtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestAuditFailureRollsBackEventCreation(t *testing.T) {
	ctx := testContext(t)
	migrator := poolFromEnv(t, "DATABASE_URL_MIGRATOR")
	runtime := poolFromEnv(t, "DATABASE_URL_RUNTIME")
	branchID := uuid.New()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO branches (id, code, name) VALUES ($1, upper($2), 'Audit rollback fixture')`,
		branchID, "AUDIT-"+branchID.String()[:8]); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = migrator.Exec(context.Background(), `DELETE FROM event_branches WHERE branch_id = $1`, branchID)
		_, _ = migrator.Exec(context.Background(), `DELETE FROM events WHERE name = 'Audit rollback create'`)
		_, _ = migrator.Exec(context.Background(), `DELETE FROM branches WHERE id = $1`, branchID)
	})

	boom := errors.New("injected audit failure")
	st := store.New(runtime, store.WithAuditInserter(func(context.Context, pgx.Tx, store.AuditEvent) error {
		return boom
	}))
	writer := eventsvc.NewSQLCWriter()
	var eventID uuid.UUID
	err := st.WithAudit(ctx, store.AuditEvent{Action: domain.ActionEventCreated}, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var changed bool
		var err error
		eventID, changed, err = writer.Create(ctx, tx, eventsvc.CreateWrite{
			Name:      "Audit rollback create",
			EventDate: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
			BranchIDs: []uuid.UUID{branchID},
		})
		return changed, err
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithAudit error = %v, want injected failure", err)
	}

	var count int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM events WHERE id = $1`, eventID).Scan(&count); err != nil {
		t.Fatalf("count rolled-back event: %v", err)
	}
	if count != 0 {
		t.Errorf("event rows after audit failure = %d, want 0", count)
	}
}

func TestAuditFailureRollsBackEventArchive(t *testing.T) {
	ctx := testContext(t)
	migrator := poolFromEnv(t, "DATABASE_URL_MIGRATOR")
	runtime := poolFromEnv(t, "DATABASE_URL_RUNTIME")
	actorID := uuid.New()
	eventID := uuid.New()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, role)
		VALUES ($1, $2, 'audit-archive@amrita.edu', 'admin')`, actorID, "audit-archive-"+actorID.String()); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := migrator.Exec(ctx, `
		INSERT INTO events (id, name, event_date)
		VALUES ($1, 'Audit rollback archive', DATE '2026-08-16')`, eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = migrator.Exec(context.Background(), `DELETE FROM events WHERE id = $1`, eventID)
		_, _ = migrator.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, actorID)
	})

	boom := errors.New("injected audit failure")
	st := store.New(runtime, store.WithAuditInserter(func(context.Context, pgx.Tx, store.AuditEvent) error {
		return boom
	}))
	writer := eventsvc.NewSQLCWriter()
	err := st.WithAudit(ctx, store.AuditEvent{Action: domain.ActionEventArchived}, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return writer.Archive(ctx, tx, eventID, actorID)
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithAudit error = %v, want injected failure", err)
	}

	var status string
	if err := migrator.QueryRow(ctx, `SELECT status FROM events WHERE id = $1`, eventID).Scan(&status); err != nil {
		t.Fatalf("read rolled-back event: %v", err)
	}
	if status != "active" {
		t.Errorf("event status after audit failure = %q, want active", status)
	}
}
