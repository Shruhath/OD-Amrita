package auth

// event_audit.go — the auth-event audit service (Story 1.7). It owns the two NEW
// auth events the hybrid model still records in OUR immutable ledger:
// auth.login_succeeded (first backend contact at GET /auth/me, after provisioning)
// and auth.domain_rejected (the auth middleware's EnforceDomain failure branch,
// after a VALID token). Forced logout (auth.session_revoked) is already audited by
// Story 1.5c's RevocationService and is deliberately NOT duplicated here.
//
// The subtlety it encodes: an auth event has NO business-table row — the audit row
// IS its only artifact. So the store.WithAudit write callback is a no-op that
// returns changed=true, and WithAudit's registry gate + same-tx co-commit +
// single-write-boundary invariants all still hold unchanged (see auditOnly).

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// TxRunner is the narrow write boundary the auditor depends on — the store.WithAudit
// seam. Narrowed to an interface so unit tests inject a fake that captures the built
// AuditEvent + the changed signal without a database. *store.Store satisfies it (it
// is the same shape RevocationStore uses in revoke_service.go).
type TxRunner interface {
	WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error
}

// EventAuditor writes auth events to the immutable ledger. It holds only the
// TxRunner seam — no DB type — so the same value serves the /auth/me handler and
// the auth middleware.
type EventAuditor struct {
	store TxRunner
}

// NewEventAuditor wires the auditor. In production store=*store.Store.
func NewEventAuditor(store TxRunner) *EventAuditor {
	return &EventAuditor{store: store}
}

// LoginSucceeded audits one successful login (FR48: role, device, timestamp).
// It fires at the /auth/me login round-trip AFTER provisioning, so the account +
// role are the provisioned ones. The email is lowercased/trimmed (mirroring the
// provisioning + revocation services); the payload carries the device, the
// Supabase session id (a dedup anchor) and subject. timestamp is the
// server-authoritative audit_log.created_at, so no field is passed.
func (a *EventAuditor) LoginSucceeded(ctx context.Context, actorEmail, actorRole, subject, sessionID, device string) error {
	account := strings.ToLower(strings.TrimSpace(actorEmail))
	role := actorRole
	payload := authEventPayload(map[string]any{
		"subject":    subject,
		"session_id": sessionID,
	}, device)
	return a.auditOnly(ctx, store.AuditEvent{
		Action:       domain.ActionAuthLoginSucceeded,
		ActorAccount: &account,
		ActorRole:    &role,
		Payload:      payload,
	})
}

// DomainRejected audits one OAuth domain rejection (FR48: account, timestamp). The
// token has already verified when this fires, so the email is a trusted account to
// record — lowercased/trimmed like LoginSucceeded. A rejected identity was never
// provisioned, so ActorRole is nil (SQL NULL, distinct from empty). The payload
// carries the device + rejection reason; it has NO session_id (no session was
// established).
func (a *EventAuditor) DomainRejected(ctx context.Context, actorEmail, device, reason string) error {
	account := strings.ToLower(strings.TrimSpace(actorEmail))
	payload := authEventPayload(map[string]any{
		"reason": reason,
	}, device)
	return a.auditOnly(ctx, store.AuditEvent{
		Action:       domain.ActionAuthDomainRejected,
		ActorAccount: &account,
		ActorRole:    nil,
		Payload:      payload,
	})
}

// auditOnly co-commits an audit-only row: an auth event has no business-table
// write, so the write callback does nothing and reports changed=true. WithAudit
// still runs the registry gate, opens one tx, inserts the audit row and
// commits/rolls back atomically — every 1.4 guarantee applies unchanged. A write
// failure propagates so the call site can fail closed ("no auth event path can
// complete without its audit row").
func (a *EventAuditor) auditOnly(ctx context.Context, evt store.AuditEvent) error {
	return a.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		// No business-table write: an auth event's only artifact IS its audit row.
		// Return changed=true so WithAudit inserts it.
		return true, nil
	})
}

// authEventPayload builds the jsonb audit payload from base fields, adding the
// device only when it is non-blank (a client that sent no User-Agent is omitted
// rather than recorded as an empty string). The map is closed and JSON-safe, so a
// marshal error is impossible; on the theoretical error it returns nil (SQL NULL
// payload) rather than propagate — mirroring revocationPayload.
func authEventPayload(base map[string]any, device string) []byte {
	if d := strings.TrimSpace(device); d != "" {
		base["device"] = d
	}
	b, err := json.Marshal(base)
	if err != nil {
		return nil
	}
	return b
}
