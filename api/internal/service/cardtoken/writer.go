package cardtoken

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type cardTokenQueries interface {
	UpsertStudentCardToken(context.Context, sqlc.UpsertStudentCardTokenParams) (sqlc.UpsertStudentCardTokenRow, error)
	ResolveStudentCardToken(context.Context, sqlc.ResolveStudentCardTokenParams) (sqlc.ResolveStudentCardTokenRow, error)
}

type queryFactory func(pgx.Tx) cardTokenQueries

type SQLCLedger struct {
	poolQueries *sqlc.Queries
	txQueries   queryFactory
}

func NewSQLCLedger(db sqlc.DBTX) *SQLCLedger {
	return newSQLCLedger(sqlc.New(db), func(tx pgx.Tx) cardTokenQueries { return sqlc.New(tx) })
}

func newSQLCLedger(poolQueries *sqlc.Queries, txFactory queryFactory) *SQLCLedger {
	return &SQLCLedger{poolQueries: poolQueries, txQueries: txFactory}
}

func (l *SQLCLedger) Upsert(ctx context.Context, tx pgx.Tx, entry LedgerEntry) (bool, error) {
	row, err := l.txQueries(tx).UpsertStudentCardToken(ctx, sqlc.UpsertStudentCardTokenParams{
		StudentID:   dbUUID(entry.StudentID),
		Version:     entry.Version,
		TokenSha256: entry.TokenSHA256[:],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !row.ID.Valid {
		return false, errors.New("cardtoken: token upsert returned invalid id")
	}
	return true, nil
}

func (l *SQLCLedger) Resolve(ctx context.Context, version string, fingerprint [sha256.Size]byte) (Student, error) {
	row, err := l.poolQueries.ResolveStudentCardToken(ctx, sqlc.ResolveStudentCardTokenParams{
		Version:     version,
		TokenSha256: fingerprint[:],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Student{}, ErrCardNotRecognized
	}
	if err != nil {
		return Student{}, err
	}
	return mapStudent(row.ID, row.RollNo, row.Name, row.Branch, row.Section, row.Batch)
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
