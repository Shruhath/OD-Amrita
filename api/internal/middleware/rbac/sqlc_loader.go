package rbac

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type SQLCPrincipalLoader struct {
	pool *pgxpool.Pool
}

func NewSQLCPrincipalLoader(pool *pgxpool.Pool) *SQLCPrincipalLoader {
	return &SQLCPrincipalLoader{pool: pool}
}

func (l *SQLCPrincipalLoader) Load(ctx context.Context, subject string) (Principal, error) {
	row, err := sqlc.New(l.pool).GetUserBySubject(ctx, subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, ErrPrincipalNotFound
		}
		return Principal{}, err
	}
	if !row.ID.Valid {
		return Principal{}, errors.New("rbac: provisioned principal has invalid id")
	}
	principal := Principal{
		UserID:  uuid.UUID(row.ID.Bytes).String(),
		Subject: row.SupabaseSubject,
		Email:   row.Email,
		Role:    row.Role,
	}
	if row.Name.Valid {
		principal.Name = row.Name.String
	}
	return principal, nil
}
