package cardtoken

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type SQLCReader struct {
	queries *sqlc.Queries
}

func NewSQLCReader(pool *pgxpool.Pool) *SQLCReader {
	return &SQLCReader{queries: sqlc.New(pool)}
}

func (r *SQLCReader) StudentByID(ctx context.Context, id string) (Student, error) {
	studentID, err := uuid.Parse(id)
	if err != nil {
		return Student{}, err
	}
	row, err := r.queries.GetCardTokenStudentByID(ctx, dbUUID(studentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Student{}, ErrStudentNotFound
	}
	if err != nil {
		return Student{}, err
	}
	return mapStudent(row.ID, row.RollNo, row.Name, row.Branch, row.Section, row.Batch)
}

func mapStudent(id pgtype.UUID, rollNo, name, branch, section, batch string) (Student, error) {
	if !id.Valid {
		return Student{}, errors.New("cardtoken: student row has invalid id")
	}
	return Student{
		ID: uuid.UUID(id.Bytes).String(), RollNo: rollNo, DisplayName: name,
		Branch: branch, Section: section, Batch: batch,
	}, nil
}
