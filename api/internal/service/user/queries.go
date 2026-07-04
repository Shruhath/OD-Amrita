package user

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// sqlcProvisioner is the production Provisioner: it runs the sqlc-generated
// ProvisionUser upsert bound to the caller's transaction (sqlc.New(tx)) and maps
// the row to a domain User. Binding to tx — never the pool — is what keeps the
// user write and its audit row in the SAME transaction (the WithAudit contract).
type sqlcProvisioner struct{}

// NewSQLCProvisioner returns the production Provisioner wired in cmd/api. It is
// stateless: the transaction is supplied per call, so one instance is reused.
func NewSQLCProvisioner() Provisioner { return sqlcProvisioner{} }

func (sqlcProvisioner) ProvisionUser(ctx context.Context, tx pgx.Tx, arg ProvisionUserParams) (User, error) {
	row, err := sqlc.New(tx).ProvisionUser(ctx, sqlc.ProvisionUserParams{
		SupabaseSubject: arg.SupabaseSubject,
		Email:           arg.Email,
		Name:            textOrNull(arg.Name),
		Role:            string(arg.Role),
	})
	if err != nil {
		return User{}, err
	}
	return User{
		ID:              row.ID,
		SupabaseSubject: row.SupabaseSubject,
		Email:           row.Email,
		Name:            row.Name.String, // "" when SQL NULL (Valid == false)
		Role:            Role(row.Role),
		Inserted:        row.Inserted,
	}, nil
}

// textOrNull maps an empty display name to SQL NULL (absent) rather than an
// empty string, keeping "no name" distinct from "present but blank".
func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
