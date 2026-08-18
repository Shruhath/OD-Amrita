// Package store owns database access: the sqlc-generated queries plus the
// transaction and audit helpers that wrap them. The runtime connection pool
// (pool.go) plus the same-transaction write helper (this file) and the audit
// insert (audit.go) are the ONLY write path into the database — no endpoint or
// worker job writes without co-committing an audit row.
package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// ErrUnregisteredAction is returned by WithAudit when the event's Action is empty
// or not in the domain.AllActions registry. It is the runtime half of the
// "no free-text audit events" guarantee (the compile-time half is that only a
// domain.Action can be passed) — a story that adds an auditable action but
// forgets to register it fails loudly here instead of writing an unnamed row.
var ErrUnregisteredAction = errors.New("store: unregistered audit action")

// Store is the write boundary. It holds the runtime pool and the audit inserter
// (swappable for tests via WithAuditInserter). It is framework-free — it takes a
// pgx pool, not any gin type — so cmd/api and cmd/worker (the Epic 3 River sync
// path) reuse the identical helper.
type Store struct {
	pool          *pgxpool.Pool
	auditInserter AuditInserter
	// publisher mirrors committed events onto the domain event stream. It is
	// events.Nop unless WithPublisher was passed, so the write path is unchanged
	// for every existing construction and every test. See publish.go.
	publisher events.Publisher

	// pending holds the EXTRA audit events an in-flight transaction has recorded
	// via Audit, so they can be published after that transaction commits rather
	// than at the moment they were written.
	//
	// Keyed on the transaction because that is exactly the scope the question is
	// asked at: "did the tx these belong to commit?". A field on the Store would
	// be wrong under concurrency — two coordinators marking at once would publish
	// each other's events — and passing a collector down through the write
	// callback would change the signature every service implements.
	pendingMu sync.Mutex
	pending   map[pgx.Tx][]AuditEvent
}

// Option configures a Store at construction (functional-options pattern).
type Option func(*Store)

// WithAuditInserter overrides the audit inserter. This is the fault-injection
// seam: a test passes a failing inserter to prove that an audit-insert failure
// rolls back the co-committed business write. Production omits it and gets
// defaultAuditInserter (the sqlc-backed insert).
func WithAuditInserter(fn AuditInserter) Option {
	return func(s *Store) { s.auditInserter = fn }
}

// New constructs a Store over the runtime pool. The pool MUST be the od_runtime
// (DML-only) pool — the app never writes as od_migrator.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{
		pool:          pool,
		auditInserter: defaultAuditInserter,
		publisher:     events.Nop{},
		pending:       make(map[pgx.Tx][]AuditEvent),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithAudit runs a business write and its audit row in ONE transaction, so the
// audit ledger can never diverge from the state it records (NFR20). It:
//
//   - rejects an empty/unregistered evt.Action up front (no tx is even opened, so
//     no business write can commit under an unnamed action);
//   - begins one tx on the runtime pool and runs write(ctx, tx);
//   - inserts the audit row ONLY when write reports changed == true — a dedup
//     no-op (e.g. INSERT … ON CONFLICT DO NOTHING affecting 0 rows) commits the
//     business tx with NO audit row (a non-event is not audited);
//   - commits on success, and rolls back EVERYTHING on any error — whether the
//     business write failed (no orphan audit row) or the audit insert failed
//     (the business write is undone too).
//
// changed lets the caller signal whether the write actually changed state; it is
// the RowsAffected()==N>0 signal for conditional writes.
// Audit writes ONE additional audit row inside a transaction the caller already
// opened — normally the one WithAudit is running.
//
// Some writes are two auditable facts rather than one. Approving a missed-slot
// dispute is the case that forced this: the decision (dispute.resolved) and the
// attendance record it created (od.created) are different events about different
// tables, and collapsing them into a single row would leave an od_records row
// with nothing in the ledger explaining where it came from.
//
// It applies the SAME registry gate and payload normalisation WithAudit does, so
// a second row cannot slip in under an unregistered action, and it takes the
// caller's tx so it co-commits or rolls back with the business write.
func (s *Store) Audit(ctx context.Context, tx pgx.Tx, evt AuditEvent) error {
	if !evt.Action.Registered() {
		return fmt.Errorf("%w: %q", ErrUnregisteredAction, string(evt.Action))
	}
	if evt.PayloadFunc == nil {
		payload, err := normalizeAuditPayload(evt.Payload)
		if err != nil {
			return err
		}
		evt.Payload = payload
	}
	if err := s.auditInserter(ctx, tx, evt); err != nil {
		return err
	}
	// Held, not published: this row is inside a transaction that has not
	// committed and may still roll back. WithAudit publishes it if and when the
	// commit succeeds.
	s.holdPending(tx, evt)
	return nil
}

func (s *Store) WithAudit(
	ctx context.Context,
	evt AuditEvent,
	write func(ctx context.Context, tx pgx.Tx) (changed bool, err error),
) error {
	// Registry gate, before any DB work: an unregistered/empty action must never
	// result in a committed business write (AC 5 enforcement).
	if !evt.Action.Registered() {
		return fmt.Errorf("%w: %q", ErrUnregisteredAction, string(evt.Action))
	}
	if evt.PayloadFunc == nil {
		payload, err := normalizeAuditPayload(evt.Payload)
		if err != nil {
			return err
		}
		evt.Payload = payload
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	// Safe no-op once the tx is committed; guarantees rollback on every error
	// path below (including a panic in write). The pending drop is paired with it
	// for the same reason: on any path out of this function that is not a
	// successful commit, the held events describe writes that never happened.
	defer tx.Rollback(ctx)
	defer s.dropPending(tx)

	changed, err := write(ctx, tx)
	if err != nil {
		return fmt.Errorf("store: business write: %w", err)
	}

	// Only a real state change earns an audit row. On a no-op the business tx
	// still commits cleanly (below) — it simply carries no audit row.
	if changed {
		if evt.PayloadFunc != nil {
			payload, err := evt.PayloadFunc()
			if err != nil {
				return fmt.Errorf("store: audit payload: %w", err)
			}
			payload, err = normalizeAuditPayload(payload)
			if err != nil {
				return err
			}
			evt.Payload = payload
		}
		if err := s.auditInserter(ctx, tx, evt); err != nil {
			return fmt.Errorf("store: audit insert: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}

	// COMMITTED. Only now does anything leave this process. The events are
	// facts about durable rows, and publishing them cannot fail the write that
	// already succeeded — see publish.go for why that ordering is not optional.
	if changed {
		s.publish(ctx, evt)
	}
	for _, extra := range s.takePending(tx) {
		s.publish(ctx, extra)
	}
	return nil
}

// holdPending records an extra audit event against its transaction.
func (s *Store) holdPending(tx pgx.Tx, evt AuditEvent) {
	if s.publisher == nil || s.pending == nil {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.pending[tx] = append(s.pending[tx], evt)
}

// takePending removes and returns a transaction's held events.
func (s *Store) takePending(tx pgx.Tx) []AuditEvent {
	if s.pending == nil {
		return nil
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	held := s.pending[tx]
	delete(s.pending, tx)
	return held
}

// dropPending discards a transaction's held events without publishing them. It
// runs on EVERY exit from WithAudit, so a rolled-back transaction leaves nothing
// behind — both because those events did not happen, and because a map keyed on
// transactions that is only cleaned on the success path is a slow leak.
func (s *Store) dropPending(tx pgx.Tx) {
	if s.pending == nil {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	delete(s.pending, tx)
}
