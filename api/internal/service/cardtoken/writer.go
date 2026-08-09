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

// MarkVerified records that an admin read this card back.
//
// On the pool rather than a transaction: it is a progress note about a read that
// has already happened and has already been answered, not part of any write the
// caller is composing. Nothing else in the request depends on it.
func (l *SQLCLedger) MarkVerified(ctx context.Context, version string, fingerprint [sha256.Size]byte, actorUserID string) error {
	actor, err := uuid.Parse(actorUserID)
	if err != nil {
		return err
	}
	// Zero rows is not an error: the row can only be missing if the token was
	// re-sealed between the resolve and this update, and the newer seal is the
	// truth in that race — silently doing nothing is the correct outcome.
	_, err = l.poolQueries.MarkStudentCardTokenVerified(ctx, sqlc.MarkStudentCardTokenVerifiedParams{
		VerifiedByUserID: dbUUID(actor),
		Version:          version,
		TokenSha256:      fingerprint[:],
	})
	return err
}

// SectionStatus reads one section's durable progress.
func (l *SQLCLedger) SectionStatus(ctx context.Context, version string, filter SectionFilter) ([]CardStatus, error) {
	branch, err := uuid.Parse(filter.BranchID)
	if err != nil {
		return nil, err
	}
	rows, err := l.poolQueries.ListSectionCardStatus(ctx, sqlc.ListSectionCardStatusParams{
		Version:  version,
		BranchID: dbUUID(branch),
		Batch:    filter.Batch,
		Section:  filter.Section,
	})
	if err != nil {
		return nil, err
	}
	out := make([]CardStatus, 0, len(rows))
	for _, row := range rows {
		status := CardStatus{
			StudentID: uuid.UUID(row.StudentID.Bytes).String(),
			Issued:    row.Issued,
		}
		if row.VerifiedAt.Valid {
			at := row.VerifiedAt.Time
			status.VerifiedAt = &at
		}
		out = append(out, status)
	}
	return out, nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
