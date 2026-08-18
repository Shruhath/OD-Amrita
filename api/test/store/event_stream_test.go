//go:build integration

package storetest

// event_stream_test.go — the domain event stream's ORDERING guarantees, proven
// against a real transaction.
//
// These belong here rather than in a unit test because the whole claim is about
// what happens either side of a real COMMIT and a real ROLLBACK. A fake
// transaction cannot fail to commit for the reasons a real one does, and the bug
// this is guarding against — announcing a mark that was rolled back — is
// precisely a bug about the boundary between those two.
//
// INTEGRATION tests: `//go:build integration` + DSN-gated, so `go test ./...`
// (no DB) skips them cleanly.
//
// Priority tags: [P0] critical gate · [P1] important · [P2] guard.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/events"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// recordingPublisher captures what the store put on the stream.
type recordingPublisher struct {
	mu        sync.Mutex
	published []events.Envelope
}

func (p *recordingPublisher) Publish(_ context.Context, envelope events.Envelope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, envelope)
}

func (p *recordingPublisher) types() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.published))
	for _, envelope := range p.published {
		out = append(out, envelope.Type)
	}
	return out
}

func (p *recordingPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.published)
}

// TestStream_PublishesOnlyAfterACommit — [P0] the load-bearing test.
//
// The event stream tells the rest of the system that something happened. If it
// could publish before or without a commit, a consumer would act on a mark that
// the database rolled back — a live board showing a student present who is not,
// with no row anywhere to explain it. Here the business write fails AFTER
// inserting, so the transaction really does roll back real work.
func TestStream_NothingIsPublishedWhenTheTransactionRollsBack(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	publisher := &recordingPublisher{}
	s := store.New(runtimePool, store.WithPublisher(publisher))

	boom := errors.New("business write failed")
	err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "doomed"); err != nil {
				return false, err
			}
			return true, boom
		})
	if err == nil {
		t.Fatal("[P0] expected the business-write error to surface")
	}

	if got := probeRowCount(t, ctx, runtimePool, probe); got != 0 {
		t.Fatalf("[P0] harness check: expected the row to be rolled back, got %d", got)
	}
	if got := publisher.count(); got != 0 {
		t.Errorf("[P0] published %d event(s) for a rolled-back transaction — consumers would act on a mark that does not exist", got)
	}
}

// TestStream_PublishesTheCommittedEvent — [P0]. The other direction.
func TestStream_PublishesTheCommittedEvent(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	publisher := &recordingPublisher{}
	s := store.New(runtimePool, store.WithPublisher(publisher))

	err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			ct, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "row-1")
			if err != nil {
				return false, err
			}
			return ct.RowsAffected() == 1, nil
		})
	if err != nil {
		t.Fatalf("[P0] WithAudit returned %v", err)
	}

	types := publisher.types()
	if len(types) != 1 || types[0] != string(domain.ActionODCreated) {
		t.Fatalf("[P0] published %v, want exactly [od.created]", types)
	}
	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 1 {
		t.Errorf("[P0] the ledger must still hold exactly 1 row, got %d", got)
	}
}

// TestStream_ADedupNoOpPublishesNothing — [P1].
//
// The stream and the ledger must agree on what counted as an event. WithAudit
// writes no audit row when the write reports changed=false, and the publish is
// governed by the SAME `if`, so a duplicate scan that changed nothing announces
// nothing.
func TestStream_ADedupNoOpPublishesNothing(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	publisher := &recordingPublisher{}
	s := store.New(runtimePool, store.WithPublisher(publisher))

	write := func(ctx context.Context, tx pgx.Tx) (bool, error) {
		ct, err := tx.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1) ON CONFLICT (k) DO NOTHING`, probe), "same-key")
		if err != nil {
			return false, err
		}
		return ct.RowsAffected() == 1, nil
	}
	if err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID), write); err != nil {
		t.Fatalf("[P1] first write: %v", err)
	}
	if err := s.WithAudit(ctx, auditEvent(domain.ActionODCreated, probe, runID), write); err != nil {
		t.Fatalf("[P1] second (dedup) write: %v", err)
	}

	if got := publisher.count(); got != 1 {
		t.Errorf("[P1] published %d events across one real write and one dedup no-op, want 1", got)
	}
	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 1 {
		t.Errorf("[P1] the ledger holds %d rows, want 1 — the stream and the ledger must agree on what counted", got)
	}
}

// TestStream_ExtraAuditRowsAreAlsoPublished — [P0].
//
// Approving a missed-slot dispute writes TWO auditable facts: the decision, and
// the attendance record it created. The second goes through Store.Audit inside
// the caller's transaction. If only the primary event reached the stream, the
// marks topic would be missing every dispute-created record — a silent, partial
// stream, which is worse than no stream because it looks complete.
func TestStream_ExtraAuditRowsAreAlsoPublished(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	publisher := &recordingPublisher{}
	s := store.New(runtimePool, store.WithPublisher(publisher))

	err := s.WithAudit(ctx, auditEvent(domain.ActionDisputeResolved, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (k) VALUES ($1)`, probe), "resolved"); err != nil {
				return false, err
			}
			// The second fact, in the SAME transaction.
			return true, s.Audit(ctx, tx, auditEvent(domain.ActionODCreated, probe, runID))
		})
	if err != nil {
		t.Fatalf("[P0] WithAudit returned %v", err)
	}

	types := publisher.types()
	if len(types) != 2 {
		t.Fatalf("[P0] published %v, want both the decision and the record it created", types)
	}
	seen := map[string]bool{}
	for _, eventType := range types {
		seen[eventType] = true
	}
	for _, want := range []string{string(domain.ActionDisputeResolved), string(domain.ActionODCreated)} {
		if !seen[want] {
			t.Errorf("[P0] %q never reached the stream; published %v", want, types)
		}
	}
}

// TestStream_ExtraAuditRowsAreDiscardedOnRollback — [P0]. The same trap as the
// primary event, on the path that holds events across the transaction.
func TestStream_ExtraAuditRowsAreDiscardedOnRollback(t *testing.T) {
	ctx := testCtx(t)
	migratorPool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtimePool := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	probe := newProbeTable(t, ctx, migratorPool)
	runID := randHex(t)

	publisher := &recordingPublisher{}
	s := store.New(runtimePool, store.WithPublisher(publisher))

	boom := errors.New("failed after the second audit row")
	err := s.WithAudit(ctx, auditEvent(domain.ActionDisputeResolved, probe, runID),
		func(ctx context.Context, tx pgx.Tx) (bool, error) {
			if err := s.Audit(ctx, tx, auditEvent(domain.ActionODCreated, probe, runID)); err != nil {
				return false, err
			}
			return true, boom
		})
	if err == nil {
		t.Fatal("[P0] expected the error to surface")
	}
	if got := publisher.count(); got != 0 {
		t.Errorf("[P0] published %d event(s) from a rolled-back transaction", got)
	}
	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 0 {
		t.Errorf("[P0] harness check: %d audit rows survived the rollback", got)
	}
}

// TestStream_AbsentPublisherLeavesTheWritePathUnchanged — [P0].
//
// This is the production configuration: store.New(pool) with no publisher. The
// existing suite already covers the write itself; what this adds is the explicit
// statement that the default construction still works with the stream code
// compiled in — which is the promise the whole change is being merged on.
func TestStream_AbsentPublisherLeavesTheWritePathUnchanged(t *testing.T) {
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
		t.Fatalf("[P0] the default (no-publisher) write path returned %v", err)
	}
	if got := auditRowCountForRun(t, ctx, runtimePool, runID); got != 1 {
		t.Errorf("[P0] expected 1 audit row, got %d", got)
	}
}
