package auth

// revoke_adapters.go — the production (sqlc-backed) implementations of the
// revocation seams the service and the auth middleware depend on: the deny-list
// writer, the user directory, and the next-request revocation checker. Unit tests
// use fakes instead; these are wired in cmd/api.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// ErrActorNotProvisioned is returned by UserDirectory.ResolveActor when the actor
// has no users row; the service maps it to RBAC_FORBIDDEN (DoD 8).
var ErrActorNotProvisioned = errors.New("auth: actor is not provisioned")

// ErrTargetNotFound is returned by UserDirectory.TargetSubject when the URL user
// id is malformed or matches no user; the service maps it to VALIDATION_FAILED.
var ErrTargetNotFound = errors.New("auth: target user not found")

// RevocationChecker answers "is this verified token revoked?" for the auth
// middleware guard (DoD 6). The production impl consults the local deny-list; a
// unit-test fake returns a fixed verdict.
type RevocationChecker interface {
	IsRevoked(ctx context.Context, id VerifiedIdentity) (bool, error)
}

// --- deny-list writer -------------------------------------------------------

// sqlcRevocationWriter runs the sqlc InsertRevocation upsert bound to the
// caller's transaction (sqlc.New(tx)), keeping the deny-list write and its audit
// row in the SAME transaction (the WithAudit contract).
type sqlcRevocationWriter struct{}

// NewSQLCRevocationWriter returns the production RevocationWriter wired in
// cmd/api. It is stateless: the transaction is supplied per call.
func NewSQLCRevocationWriter() RevocationWriter { return sqlcRevocationWriter{} }

func (sqlcRevocationWriter) InsertRevocation(ctx context.Context, tx pgx.Tx, arg InsertRevocationParams) (Revocation, error) {
	sessionID, err := optionalUUID(arg.SessionID)
	if err != nil {
		return Revocation{}, err
	}
	revokedBy, err := requiredUUID(arg.RevokedByUserID)
	if err != nil {
		return Revocation{}, err
	}
	row, err := sqlc.New(tx).InsertRevocation(ctx, sqlc.InsertRevocationParams{
		SupabaseSubject: arg.Subject,
		SessionID:       sessionID,
		Scope:           string(arg.Scope),
		Reason:          arg.Reason,
		RevokedByUserID: revokedBy,
	})
	if err != nil {
		return Revocation{}, err
	}
	return Revocation{ID: row.ID, Inserted: row.Inserted}, nil
}

// --- user directory ---------------------------------------------------------

// sqlcUserDirectory resolves actor/target rows read-only on the runtime pool
// (these are lookups, not state changes, so they don't go through WithAudit).
type sqlcUserDirectory struct {
	pool *pgxpool.Pool
}

// NewSQLCUserDirectory returns the production UserDirectory over the runtime pool.
func NewSQLCUserDirectory(pool *pgxpool.Pool) UserDirectory {
	return sqlcUserDirectory{pool: pool}
}

func (d sqlcUserDirectory) ResolveActor(ctx context.Context, subject string) (string, string, error) {
	row, err := sqlc.New(d.pool).GetActorBySubject(ctx, subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrActorNotProvisioned
		}
		return "", "", err
	}
	return row.ID, row.Role, nil
}

func (d sqlcUserDirectory) TargetSubject(ctx context.Context, userID string) (string, error) {
	id, err := requiredUUID(userID)
	if err != nil {
		// A malformed id can never match a real user — treat it as not-found
		// (VALIDATION_FAILED) rather than a DB cast error.
		return "", ErrTargetNotFound
	}
	subject, err := sqlc.New(d.pool).GetUserSubjectByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrTargetNotFound
		}
		return "", err
	}
	return subject, nil
}

// --- revocation checker -----------------------------------------------------

// sqlcRevocationChecker consults the local deny-list read-only on the runtime
// pool. It runs on EVERY protected request, so it stays a single indexed SELECT.
type sqlcRevocationChecker struct {
	pool *pgxpool.Pool
}

// NewSQLCRevocationChecker returns the production RevocationChecker over the
// runtime pool.
func NewSQLCRevocationChecker(pool *pgxpool.Pool) RevocationChecker {
	return sqlcRevocationChecker{pool: pool}
}

func (c sqlcRevocationChecker) IsRevoked(ctx context.Context, id VerifiedIdentity) (bool, error) {
	// A malformed session_id must not fail the whole check: leave it NULL so the
	// scope=session branch can't match, while the scope=all branch (keyed on iat)
	// still applies. The verifier already requires session_id to be present.
	sessionID, _ := optionalUUID(&id.SessionID)
	return sqlc.New(c.pool).IsTokenRevoked(ctx, sqlc.IsTokenRevokedParams{
		SupabaseSubject: id.Subject,
		SessionID:       sessionID,
		IssuedAt:        pgtype.Timestamptz{Time: id.IssuedAt, Valid: true},
	})
}

// --- uuid helpers -----------------------------------------------------------

// requiredUUID parses a non-empty uuid string into a valid pgtype.UUID.
func requiredUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

// optionalUUID maps a nil/empty pointer to a NULL pgtype.UUID, otherwise parses
// it. A non-nil but malformed value returns an error.
func optionalUUID(s *string) (pgtype.UUID, error) {
	if s == nil || *s == "" {
		return pgtype.UUID{}, nil
	}
	return requiredUUID(*s)
}
