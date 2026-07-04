package user

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// User is a provisioned application identity (a users-table row) returned to the
// caller. Inserted reports whether THIS provisioning call created the row (true)
// or matched an existing one (false) — the signal WithAudit uses to decide
// whether a user.provisioned audit row is written.
type User struct {
	ID              string
	SupabaseSubject string
	Email           string
	Name            string
	Role            Role
	Inserted        bool
}

// ProvisionUserParams is the input to the provisioning upsert, decoupled from
// the sqlc-generated params so the service layer never imports pgtype.
type ProvisionUserParams struct {
	SupabaseSubject string
	Email           string
	Name            string
	Role            Role
}

// Provisioner runs the provisioning upsert on the caller's transaction and maps
// the row to a domain User. It is the DB seam: the unit test substitutes a fake
// (no database); production uses the sqlc-backed adapter (queries.go). The tx
// MUST be the one WithAudit opened, so the user write and its audit row
// co-commit — never bind to the pool here.
type Provisioner interface {
	ProvisionUser(ctx context.Context, tx pgx.Tx, arg ProvisionUserParams) (User, error)
}

// TxRunner is the write boundary the service depends on — satisfied by
// *store.Store. Narrowing it to an interface lets the unit test inject a fake
// that captures the audit event and the changed signal without a database.
type TxRunner interface {
	WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error
}

// Service provisions verified Supabase identities into application users on
// first login, co-committing a user.provisioned audit row through WithAudit.
type Service struct {
	store           TxRunner
	bootstrapAdmins map[string]struct{}
	queries         Provisioner
}

// NewService wires the provisioning service. bootstrapAdmins is the lowercased
// allowlist from config (empty → no bootstrap admins). queries is the DB seam
// (NewSQLCProvisioner() in production).
func NewService(store TxRunner, bootstrapAdmins map[string]struct{}, queries Provisioner) *Service {
	return &Service{store: store, bootstrapAdmins: bootstrapAdmins, queries: queries}
}

// Provision maps a verified Supabase identity to an application user + role on
// first contact and returns the resulting user. It runs the upsert and its audit
// row in ONE transaction via WithAudit: a genuine first insert reports
// changed=true and earns a user.provisioned audit row; a returning identity
// reports changed=false (a last_login_at bump is not an auditable event) and
// commits with NO audit row. Safe and idempotent to call on every login.
func (s *Service) Provision(ctx context.Context, id auth.VerifiedIdentity) (User, error) {
	// Lowercase the email once: it is both the ResolveRole lookup key (against
	// the lowercased allowlist) and the stored/audited account, mirroring the
	// 1.5a domain-check casing. sub, not email, is the durable identity key.
	email := strings.ToLower(strings.TrimSpace(id.Email))
	role := ResolveRole(email, s.bootstrapAdmins)
	roleStr := string(role)
	targetTable := "users"

	// targetID is filled by the write below. WithAudit captures evt BEFORE
	// running write, but evt.TargetID is a pointer into this variable, so the id
	// the write learns from RETURNING is visible when the audit row is inserted
	// (only on a real change, after write). No client-generated id is needed.
	var targetID string
	var provisioned User

	evt := store.AuditEvent{
		Action:       domain.ActionUserProvisioned,
		ActorAccount: &email,
		ActorRole:    &roleStr,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		// Payload deliberately nil → SQL NULL (no free-form context needed).
	}

	err := s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		u, err := s.queries.ProvisionUser(ctx, tx, ProvisionUserParams{
			SupabaseSubject: id.Subject,
			Email:           email,
			Name:            id.Name,
			Role:            role,
		})
		if err != nil {
			return false, err
		}
		provisioned = u
		targetID = u.ID
		// changed == Inserted: only a genuine first insert earns the audit row; a
		// returning user (last_login_at bump) commits with none.
		return u.Inserted, nil
	})
	if err != nil {
		return User{}, err
	}
	return provisioned, nil
}
