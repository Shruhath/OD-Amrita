package store

// audit.go — the immutable audit-log write helper used by the same-transaction
// co-commit helper (tx.go). It builds on the sqlc-generated InsertAuditLog so the
// insert can never drift from the audit_log schema.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// AuditEvent is the typed payload of one audit-ledger row. Action is the ONLY
// required field (it comes from the domain.Action registry and is validated by
// WithAudit); every other field is optional, mirroring audit_log's all-nullable
// (except action/created_at) shape — the infallible-insert contract from 0001.
//
// Optional string fields are *string so "absent" (nil -> SQL NULL) is distinct
// from "present but empty". Payload is raw JSON for the jsonb column.
type AuditEvent struct {
	Action       domain.Action
	ActorAccount *string
	ActorRole    *string
	TargetTable  *string
	TargetID     *string
	Payload      []byte
}

// AuditInserter inserts one audit row inside the caller's transaction. It is the
// fault-injection seam (see WithAuditInserter): tests substitute a failing
// inserter to exercise the "audit-insert failure rolls back the business write"
// rollback direction without adding a schema constraint the real insert could
// trip. Production uses defaultAuditInserter.
type AuditInserter func(ctx context.Context, tx pgx.Tx, evt AuditEvent) error

// defaultAuditInserter is the production audit insert: it runs the sqlc
// InsertAuditLog query bound to the caller's tx, so the audit row co-commits with
// the business write. pgx.Tx satisfies sqlc's DBTX, so sqlc.New(tx) scopes the
// query to the transaction.
func defaultAuditInserter(ctx context.Context, tx pgx.Tx, evt AuditEvent) error {
	return sqlc.New(tx).InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		Action:       string(evt.Action),
		ActorAccount: text(evt.ActorAccount),
		ActorRole:    text(evt.ActorRole),
		TargetTable:  text(evt.TargetTable),
		TargetID:     text(evt.TargetID),
		Payload:      evt.Payload,
	})
}

// text converts an optional Go string into a pgtype.Text: nil -> SQL NULL,
// non-nil -> the value (even if empty).
func text(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
