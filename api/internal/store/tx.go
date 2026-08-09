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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	if s == nil || s.pool == nil {
		return fmt.Errorf("store: database pool is nil")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	// Safe no-op once the tx is committed; guarantees rollback on every error
	// path below (including a panic in write).
	defer tx.Rollback(ctx)

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
	return nil
}
