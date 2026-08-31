package rbac

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
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

// CachedPrincipalLoader wraps a PrincipalLoader with an in-memory singleflight TTL cache.
type CachedPrincipalLoader struct {
	inner PrincipalLoader
	cache *cache.TTL[Principal]
}

// NewCachedPrincipalLoader returns a PrincipalLoader with in-memory TTL caching.
func NewCachedPrincipalLoader(inner PrincipalLoader, ttl time.Duration) PrincipalLoader {
	if ttl <= 0 {
		return inner
	}
	return &CachedPrincipalLoader{
		inner: inner,
		cache: cache.New[Principal](ttl),
	}
}

func (l *CachedPrincipalLoader) Load(ctx context.Context, subject string) (Principal, error) {
	return l.cache.Do(ctx, subject, func(ctx context.Context) (Principal, error) {
		return l.inner.Load(ctx, subject)
	})
}
