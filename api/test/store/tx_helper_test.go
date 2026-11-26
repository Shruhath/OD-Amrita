//go:build integration

package storetest

// tx_helper_test.go — acceptance tests for AC 3, 4, 5, 6
// (store.WithAudit: same-transaction co-commit, both rollback directions, dedup
// no-op = no audit row, unregistered-action rejection, conservation invariant).
//
// Every store.* symbol below is defined in store/tx.go and store/audit.go. The
// tests are isolated to this package so they do not affect the api/test/db
// integration suite.
//
// ────────────────────────────────────────────────────────────────────────────
// API SURFACE these tests drive. If the fault-injection seam is renamed, only
// the store.New / store.WithAuditInserter lines need to change:
//
//	type Store
//	func New(pool *pgxpool.Pool, opts ...Option) *Store
//	type AuditInserter func(ctx context.Context, tx pgx.Tx, evt AuditEvent) error
//	func WithAuditInserter(AuditInserter) Option          // the test seam
//	type AuditEvent struct { Action domain.Action; ActorAccount, ActorRole,
//	                         TargetTable, TargetID *string; Payload []byte }
//	func (s *Store) WithAudit(ctx context.Context, evt AuditEvent,
//	    write func(ctx context.Context, tx pgx.Tx) (changed bool, err error)) error
//
// WithAudit begins ONE tx on the runtime pool, runs `write`, inserts the audit
// row ONLY when write reports changed==true, rejects an empty/unregistered
// evt.Action, commits on success, and rolls back everything on any error.
// ────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// auditEvent builds an event tagged with this run's marker (in target_id) so the
// conservation/count assertions can isolate the rows this test wrote.
func auditEvent(action domain.Action, probe, runID string) store.AuditEvent {
	return store.AuditEvent{
		Action:      action,
		TargetTable: &probe,
		TargetID:    &runID,
	}
}

// TestWithAudit_HappyPath_CoCommits — AC 3. A real business write + its audit row
// commit together: after WithAudit, both the probe row and exactly one audit row
// exist.
func TestWithAudit_HappyPath_CoCommits(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	s := store.New(runtimePool)
	err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			ct, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "row-1")
			if err != nil {
				return false, err
			}
			return ct.RowsAffected() == 1, nil
		})
	if err != nil {
		t.Fatalf("[P0] WithAudit happy path returned error: %v", err)
	}

	if got := probeRowCount(t, ctx, runtimePool, probe); got != 1 {
		t.Errorf("[P0] expected 1 business (probe) row, got %d", got)
	}
	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 1 {
		t.Errorf("[P0] expected exactly 1 audit row co-committed, got %d", got)
	}
}

// TestWithAudit_BusinessWriteFails_NoOrphanAudit — AC 3, rollback direction A.
// When the business write reports an error, the whole transaction rolls back:
// no business row AND, critically, NO orphan audit row.
func TestWithAudit_BusinessWriteFails_NoOrphanAudit(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	boom := errors.New("business write failed")
	s := store.New(runtimePool)
	err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			// Write a row, THEN fail — proves the row is rolled back, not merely
			// never attempted.
			if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "doomed"); err != nil {
				return false, err
			}
			return true, boom
		})
	if err == nil {
		t.Fatal("[P0] expected WithAudit to surface the business-write error")
	}

	if got := probeRowCount(t, ctx, runtimePool, probe); got != 0 {
		t.Errorf("[P0] expected the business row rolled back, got %d probe rows", got)
	}
	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 0 {
		t.Errorf("[P0] expected NO orphan audit row after a business failure, got %d", got)
	}
}

// TestWithAudit_AuditInsertFails_RollsBackBusinessWrite — AC 3, rollback
// direction B. Using the fault-injection seam to force the AUDIT insert to fail,
// the BUSINESS write must also roll back — audit and action are inseparable.
func TestWithAudit_AuditInsertFails_RollsBackBusinessWrite(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	failing := store.AuditInserter(func(ctx context.Context, tx pgx.Tx, evt store.AuditEvent) error {
		return errors.New("injected audit-insert failure")
	})
	s := store.New(runtimePool, store.WithAuditInserter(failing))

	err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			ct, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "row-1")
			if err != nil {
				return false, err
			}
			return ct.RowsAffected() == 1, nil
		})
	if err == nil {
		t.Fatal("[P0] expected WithAudit to surface the injected audit-insert failure")
	}

	if got := probeRowCount(t, ctx, runtimePool, probe); got != 0 {
		t.Errorf("[P0] expected the business write ROLLED BACK when the audit insert fails, got %d probe rows", got)
	}
}

// TestWithAudit_DedupNoOp_WritesNoAudit — AC 4. A write that affects 0 rows
// (INSERT ... ON CONFLICT DO NOTHING on an existing key) reports changed=false;
// the transaction still commits cleanly, but NO audit row is written (a non-event
// is not audited).
func TestWithAudit_DedupNoOp_WritesNoAudit(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	// Seed the key AS THE OWNER so the runtime's INSERT hits an ON CONFLICT no-op.
	if _, err := migratorPool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "dup-key"); err != nil {
		t.Fatalf("seed conflict row: %v", err)
	}

	s := store.New(runtimePool)
	err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			ct, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1) ON CONFLICT (k) DO NOTHING`, probe), "dup-key")
			if err != nil {
				return false, err
			}
			return ct.RowsAffected() == 1, nil // 0 affected ⇒ changed=false
		})
	if err != nil {
		t.Fatalf("[P0] dedup no-op should commit cleanly, got error: %v", err)
	}

	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 0 {
		t.Errorf("[P0] expected NO audit row for a dedup no-op (changed=false), got %d", got)
	}
}

// TestWithAudit_UnregisteredAction_Rejected — AC 5 enforcement. events.go /
// AllActions is the SSOT; the helper must reject an action not in the registry
// (and an empty action), writing no audit row and not committing the business
// write. This makes "a change forgot to register its action" fail loudly.
func TestWithAudit_UnregisteredAction_Rejected(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	s := store.New(runtimePool)

	for _, tc := range []struct {
		name   string
		action domain.Action
	}{
		{"unregistered", domain.Action("totally.unregistered")},
		{"empty", domain.Action("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runID := randHex(t)
			err := s.WithAudit(ctx, auditEvent(tc.action, probe, runID),
				func(ctx context.Context, tx pgx.Tx) (bool, error) {
					_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "row-"+runID)
					return true, err
				})
			if err == nil {
				t.Fatalf("[P1] expected WithAudit to REJECT %s action (not in domain.AllActions)", tc.name)
			}
			if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 0 {
				t.Errorf("[P1] expected no audit row for a rejected action, got %d", got)
			}
		})
	}
}

// TestWithAudit_ConservationInvariant — AC 6. Over a mixed workload of N real
// state-changing writes and M dedup no-ops, the number of audit rows equals N
// (the no-ops contribute zero). This proves the helper conserves the invariant
// count(audit) == count(state changes) at the only place a write can happen in
// this layer. (A global reconciliation replay over real domain tables is separate.)
func TestWithAudit_ConservationInvariant(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)
	s := store.New(runtimePool)

	const realWrites = 3
	const noOps = 2

	for i := 0; i < realWrites; i++ {
		key := fmt.Sprintf("real-%d", i)
		err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
			func(ctx context.Context, tx pgx.Tx) (bool, error) {
				ct, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), key)
				if err != nil {
					return false, err
				}
				return ct.RowsAffected() == 1, nil
			})
		if err != nil {
			t.Fatalf("[P0] real write %d failed: %v", i, err)
		}
	}

	// Seed a key, then attempt it M times as dedup no-ops (changed=false each).
	if _, err := migratorPool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "noop-key"); err != nil {
		t.Fatalf("seed no-op key: %v", err)
	}
	for i := 0; i < noOps; i++ {
		err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
			func(ctx context.Context, tx pgx.Tx) (bool, error) {
				ct, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1) ON CONFLICT (k) DO NOTHING`, probe), "noop-key")
				if err != nil {
					return false, err
				}
				return ct.RowsAffected() == 1, nil
			})
		if err != nil {
			t.Fatalf("[P0] no-op %d failed: %v", i, err)
		}
	}

	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != realWrites {
		t.Errorf("[P0] conservation invariant violated: expected count(audit)==%d (the %d no-ops contribute zero), got %d", realWrites, noOps, got)
	}
}
