package user

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// LoginTouchInterval is how stale last_login_at may get before a returning
// login is allowed to write again.
//
// WHY THIS CONSTANT EXISTS AT ALL. Provision used to run its upsert on EVERY
// /auth/me — and /auth/me is called on every Supabase auth-state change, not
// once per sign-in. Production measured 2,157 ProvisionUser calls averaging 8ms
// for 17 seconds of database time: the single most expensive query in the
// system, and ~95% of it re-writing last_login_at to a value a few seconds
// newer than the one already there. Each was a row update, a WAL record and its
// own transaction, against a read that answers the same question for 0ms.
//
// Fifteen minutes keeps last_login_at meaningful — it still answers "when was
// this person last here" to a resolution nobody reads more finely — while
// collapsing a burst of token refreshes into at most one write.
const LoginTouchInterval = 15 * time.Minute

// ErrNotProvisioned reports that no users row exists for the subject yet, so the
// caller must take the write path. It is a sentinel rather than a zero value
// because "absent" and "present but empty" must not be confused here: the first
// means insert (and audit), the second would silently skip a first login.
var ErrNotProvisioned = errors.New("user: subject not provisioned")

// Current is the already-provisioned row, read without a transaction.
type Current struct {
	ID          string
	Email       string
	Name        string
	Role        Role
	LastLoginAt time.Time // zero when SQL NULL
}

// CurrentReader is the cheap read seam consulted BEFORE the write path.
//
// Separate from Provisioner on purpose: Provisioner binds to a transaction
// because its write must co-commit with an audit row, and this one must NOT —
// it is a plain pooled read whose entire value is that it costs no transaction.
type CurrentReader interface {
	CurrentUser(ctx context.Context, subject string) (Current, error)
}

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
	current         CurrentReader
	clock           func() time.Time
}

// NewService wires the provisioning service. bootstrapAdmins is the lowercased
// allowlist from config (empty → no bootstrap admins). queries is the DB seam
// (NewSQLCProvisioner() in production).
func NewService(store TxRunner, bootstrapAdmins map[string]struct{}, queries Provisioner) *Service {
	return &Service{store: store, bootstrapAdmins: bootstrapAdmins, queries: queries, clock: time.Now}
}

// WithCurrentReader opts this service into skipping the write when nothing has
// changed.
//
// A setter rather than a constructor parameter, matching WithSlotCeiling in
// service/od_record: the reader is optional and every caller that does not pass
// one must keep the behaviour it has today — an unconditional upsert. Adding it
// to NewService would have made "did somebody remember to pass this?" a
// question, and the wrong answer here is a silent extra write per login, not a
// visible failure.
func (s *Service) WithCurrentReader(reader CurrentReader) *Service {
	s.current = reader
	return s
}

// withClock pins time for tests that assert the staleness window.
func (s *Service) withClock(now func() time.Time) *Service {
	s.clock = now
	return s
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
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
	// Trim the display name at the source too: a whitespace-only name must map to
	// SQL NULL (absent), not a blank string, so textOrNull's empty check sees "".
	name := strings.TrimSpace(id.Name)
	role := ResolveRole(email, s.bootstrapAdmins)
	roleStr := string(role)
	targetTable := "users"

	// ── READ BEFORE WRITE ─────────────────────────────────────────────────────
	//
	// The overwhelmingly common case is a returning user whose row is already
	// exactly right. Answering that from a plain SELECT costs one pooled read;
	// answering it the old way cost a transaction, a row update and a WAL record.
	//
	// The role is deliberately NOT compared. ProvisionUser sets role only on
	// INSERT — "a returning admin/coordinator is never reset to their first-login
	// role" — so a stored role that differs from ResolveRole is the CORRECT state
	// after an admin elevates someone, and treating it as drift would write on
	// every single login for exactly those users.
	if s.current != nil {
		cur, err := s.current.CurrentUser(ctx, id.Subject)
		switch {
		case err == nil:
			if !s.needsWrite(cur, email, name) {
				return User{
					ID:              cur.ID,
					SupabaseSubject: id.Subject,
					Email:           cur.Email,
					Name:            cur.Name,
					Role:            cur.Role,
					// Never true here by construction: a row we just read cannot
					// have been inserted by this call, so no audit row is owed.
					Inserted: false,
				}, nil
			}
		case errors.Is(err, ErrNotProvisioned):
			// First contact — fall through to the insert, which earns its audit row.
		default:
			// A failed read must NOT silently downgrade into the write path: that
			// would turn a transient blip into the expensive behaviour we just
			// removed, invisibly. Surface it.
			return User{}, err
		}
	}

	// targetID is filled by the write below. WithAudit captures evt BEFORE
	// running write, but evt.TargetID is a pointer into this variable, so the id
	// the write learns from RETURNING is visible when the audit row is inserted
	// (only on a real change, after write). No client-generated id is needed.
	var targetID string
	var provisioned User
	payload, err := json.Marshal(map[string]string{
		"email":            email,
		"name":             name,
		"resolved_role":    roleStr,
		"supabase_subject": id.Subject,
	})
	if err != nil {
		return User{}, err
	}

	evt := store.AuditEvent{
		Action:       domain.ActionUserProvisioned,
		ActorAccount: &email,
		ActorRole:    &roleStr,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		u, err := s.queries.ProvisionUser(ctx, tx, ProvisionUserParams{
			SupabaseSubject: id.Subject,
			Email:           email,
			Name:            name,
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

// needsWrite reports whether the stored row actually differs from what this
// login would write, or has gone stale enough to be worth touching.
//
// Email and name track Supabase and must follow it — a person who changes their
// display name upstream should see it change here on their next login, not in
// fifteen minutes. Those two therefore write immediately; only the last_login_at
// bump is rate-limited.
func (s *Service) needsWrite(cur Current, email, name string) bool {
	if cur.Email != email || cur.Name != name {
		return true
	}
	// A NULL last_login_at is a row that has never recorded one. Write it, so the
	// column converges rather than staying null forever.
	if cur.LastLoginAt.IsZero() {
		return true
	}
	return s.now().Sub(cur.LastLoginAt) >= LoginTouchInterval
}
