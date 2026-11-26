package auth

// revoke_service.go — the forced-logout / server-side session-revocation service
// It records a revocation in the local deny-list and co-commits an
// auth.session_revoked audit row through store.WithAudit, keeping the ledger and
// the enforcement record in one transaction (NFR20). Supabase-side revocation is
// best-effort; this local deny-list is authoritative for our API's next-request
// guarantee (the auth middleware checks it — see revocation_checker.go).
//
// Scope is deliberately narrow: a small admin-only gate, NOT the general RBAC
// middleware.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// roleAdmin is the only role permitted to force-log-out another user. It mirrors
// user.RoleAdmin but is duplicated as a bare string on purpose: service/user
// imports service/auth, so importing it back here would be a cycle.
const roleAdmin = "admin"

const (
	reasonMinLen = 3
	reasonMaxLen = 500
)

// RevokeScope is the revocation target: a single Supabase session or every
// session for the subject. Its string value is stored verbatim in the deny-list.
type RevokeScope string

const (
	RevokeScopeAll     RevokeScope = "all"
	RevokeScopeSession RevokeScope = "session"
)

// RevokeRequest is the validated forced-logout input (DoD 7). SessionID is
// required + UUID iff Scope is session, and MUST be absent iff Scope is all.
type RevokeRequest struct {
	Scope     RevokeScope
	SessionID string
	Reason    string
}

// Validate enforces the DoD 7 field rules, returning a VALIDATION_FAILED domain
// error (rendered 400 centrally) on any violation. It is pure — no collaborators
// — so both the handler and the service can call it.
func (r RevokeRequest) Validate() error {
	switch r.Scope {
	case RevokeScopeAll:
		if strings.TrimSpace(r.SessionID) != "" {
			return validationFailed("session_id must be absent when scope is \"all\"")
		}
	case RevokeScopeSession:
		if strings.TrimSpace(r.SessionID) == "" {
			return validationFailed("session_id is required when scope is \"session\"")
		}
		if _, err := uuid.Parse(r.SessionID); err != nil {
			return validationFailed("session_id must be a valid UUID")
		}
	default:
		return validationFailed("scope must be \"all\" or \"session\"")
	}
	reason := strings.TrimSpace(r.Reason)
	// Count Unicode code points, not bytes: the contract's minLength/maxLength are
	// character counts, so a multibyte reason must not be over/under-rejected.
	if n := utf8.RuneCountInString(reason); n < reasonMinLen || n > reasonMaxLen {
		return validationFailed("reason must be 3..500 characters")
	}
	return nil
}

// Revocation is the result of recording a deny-list entry. Inserted is false when
// the row already existed (an idempotent duplicate session revoke), the signal
// WithAudit uses to skip the audit row.
type Revocation struct {
	ID       string
	Inserted bool
}

// InsertRevocationParams is the DB-agnostic input to the deny-list write,
// decoupled from the sqlc-generated params (which carry pgtype) so the service
// layer never imports pgtype. SessionID is nil for scope=all.
type InsertRevocationParams struct {
	Subject         string
	SessionID       *string
	Scope           RevokeScope
	Reason          string
	RevokedByUserID string
}

// RevocationWriter runs the deny-list insert on the caller's transaction and maps
// the row to a Revocation. It is the DB seam (sqlc-backed in production; a fake in
// unit tests). The tx MUST be the one WithAudit opened so the write and its audit
// row co-commit.
type RevocationWriter interface {
	InsertRevocation(ctx context.Context, tx pgx.Tx, arg InsertRevocationParams) (Revocation, error)
}

// SupabaseRevoker performs best-effort Supabase-side session revocation. A nil
// return means "attempted"; an error is logged but never fails the authoritative
// local revoke (DoD 5). The disabled adapter is a no-op.
type SupabaseRevoker interface {
	RevokeSessions(ctx context.Context, subject string, sessionID *string, scope RevokeScope) error
}

// UserDirectory resolves the actor's provisioned id + role (for the FK and the
// admin gate) and the target user's Supabase subject (revocations are keyed by
// subject, never email). Role is a bare string to avoid a service/user import
// cycle.
type UserDirectory interface {
	ResolveActor(ctx context.Context, subject string) (userID string, role string, err error)
	TargetSubject(ctx context.Context, userID string) (subject string, err error)
}

// RevocationStore is the write boundary the service depends on — satisfied by
// *store.Store. Narrowed to an interface so unit tests inject a fake that
// captures the audit event and the changed signal without a database.
type RevocationStore interface {
	WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error
}

// RevocationService records forced logouts. It attempts best-effort Supabase
// revocation, then writes the authoritative local deny-list row + audit row in
// one transaction.
type RevocationService struct {
	store   RevocationStore
	writer  RevocationWriter
	revoker SupabaseRevoker
	dir     UserDirectory
}

// NewRevocationService wires the service. In production: store=*store.Store,
// writer=NewSQLCRevocationWriter(), revoker=the Supabase adapter (or a disabled
// no-op), dir=NewSQLCUserDirectory(pool).
func NewRevocationService(store RevocationStore, writer RevocationWriter, revoker SupabaseRevoker, dir UserDirectory) *RevocationService {
	return &RevocationService{store: store, writer: writer, revoker: revoker, dir: dir}
}

// RevokeUserSessions force-logs-out targetUserID's sessions on behalf of actor.
// It: validates the request; enforces the narrow admin-only gate; resolves the
// target's Supabase subject; makes a best-effort Supabase revocation call; then
// co-commits the local deny-list row and an auth.session_revoked audit row. The
// local write is the authoritative success condition — a Supabase failure does
// not roll it back. An idempotent duplicate session revoke commits with no audit
// row (changed=false).
func (s *RevocationService) RevokeUserSessions(ctx context.Context, actor VerifiedIdentity, targetUserID string, req RevokeRequest) (Revocation, error) {
	if err := req.Validate(); err != nil {
		return Revocation{}, err
	}

	// Narrow admin gate (DoD 8): actor must be provisioned AND role == admin.
	actorID, role, err := s.dir.ResolveActor(ctx, actor.Subject)
	if err != nil {
		if errors.Is(err, ErrActorNotProvisioned) {
			return Revocation{}, forbidden()
		}
		return Revocation{}, err // infra error -> centralized 500
	}
	if role != roleAdmin {
		return Revocation{}, forbidden()
	}

	// Revocations are keyed by Supabase subject; resolve it from the URL user id.
	targetSubject, err := s.dir.TargetSubject(ctx, targetUserID)
	if err != nil {
		if errors.Is(err, ErrTargetNotFound) {
			return Revocation{}, validationFailed("user_id does not match a known user")
		}
		return Revocation{}, err // infra error -> centralized 500
	}

	var sessionID *string
	if req.Scope == RevokeScopeSession {
		sid := strings.TrimSpace(req.SessionID)
		sessionID = &sid
	}

	// Best-effort Supabase-side revocation. Its failure is logged, never fatal:
	// the local deny-list below is authoritative for our API (DoD 5). The failure
	// is NOT recorded in the audit payload (it may carry provider detail).
	if err := s.revoker.RevokeSessions(ctx, targetSubject, sessionID, req.Scope); err != nil {
		log.Printf("revoke: best-effort Supabase revocation failed for subject=%s scope=%s (local deny-list is authoritative): %v", targetSubject, req.Scope, err)
	}

	reason := strings.TrimSpace(req.Reason)
	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := roleAdmin
	targetTable := "auth_session_revocations"
	payload := revocationPayload(targetSubject, targetUserID, req.Scope, sessionID, reason)

	// targetID is filled by the write via this pointer; WithAudit captures evt
	// before running write but only inserts the audit row (reading TargetID) on a
	// real change, after write has set it (mirrors service/user provisioning).
	var targetID string
	var result Revocation

	evt := store.AuditEvent{
		Action:       domain.ActionAuthSessionRevoked,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		rev, err := s.writer.InsertRevocation(ctx, tx, InsertRevocationParams{
			Subject:         targetSubject,
			SessionID:       sessionID,
			Scope:           req.Scope,
			Reason:          reason,
			RevokedByUserID: actorID,
		})
		if err != nil {
			return false, err
		}
		result = rev
		targetID = rev.ID
		// Only a genuine first insert earns an audit row; a duplicate session
		// revoke (Inserted=false) commits with none.
		return rev.Inserted, nil
	})
	if err != nil {
		return Revocation{}, err
	}
	return result, nil
}

// revocationPayload is the sanitized, non-secret audit context (DoD 9): who was
// revoked (subject + application user id), the scope, the optional session id,
// and the reason.
func revocationPayload(subject, userID string, scope RevokeScope, sessionID *string, reason string) []byte {
	m := map[string]any{
		"subject": subject,
		"user_id": userID,
		"scope":   string(scope),
		"reason":  reason,
	}
	if sessionID != nil {
		m["session_id"] = *sessionID
	}
	b, err := json.Marshal(m)
	if err != nil {
		// The map is closed and JSON-safe, so marshalling cannot fail; return nil
		// (SQL NULL payload) rather than propagate an impossible error.
		return nil
	}
	return b
}

func validationFailed(msg string) domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeVALIDATIONFAILED, Message: msg}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "You are not permitted to perform this action."}
}
