package odrecord

import (
	"context"
	"errors"
	"strings"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	cardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type SQLCReader struct {
	queries *sqlc.Queries
}

func NewSQLCReader(pool *pgxpool.Pool) *SQLCReader {
	return &SQLCReader{queries: sqlc.New(pool)}
}

func (r *SQLCReader) EventForMark(ctx context.Context, id string) (Event, error) {
	eventID, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return Event{}, err
	}
	row, err := r.queries.GetMarkEvent(ctx, dbUUID(eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrEventNotFound
	}
	if err != nil {
		return Event{}, err
	}
	// EndDate is checked as strictly as EventDate: it is NOT NULL in the database,
	// and a zero-valued one here would make range containment empty, closing the
	// slot gate on every event rather than failing.
	if !row.ID.Valid || !row.EventDate.Valid || !row.EndDate.Valid {
		return Event{}, invalidRowError("event")
	}
	return Event{
		EventID:   uuid.UUID(row.ID.Bytes).String(),
		EventDate: row.EventDate.Time,
		EndDate:   row.EndDate.Time,
		Status:    row.Status,
	}, nil
}

func (r *SQLCReader) CategoryForEvent(ctx context.Context, eventID string, categoryID string) (Category, error) {
	parsedEventID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return Category{}, err
	}
	parsedCategoryID, err := uuid.Parse(strings.TrimSpace(categoryID))
	if err != nil {
		return Category{}, err
	}
	row, err := r.queries.GetMarkCategoryForEvent(ctx, sqlc.GetMarkCategoryForEventParams{
		EventID:    dbUUID(parsedEventID),
		CategoryID: dbUUID(parsedCategoryID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, err
	}
	if !row.ID.Valid || !row.EventID.Valid {
		return Category{}, invalidRowError("category")
	}
	return Category{CategoryID: uuid.UUID(row.ID.Bytes).String(), EventID: uuid.UUID(row.EventID.Bytes).String()}, nil
}

func (r *SQLCReader) StudentForMark(ctx context.Context, id string) (Student, error) {
	studentID, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return Student{}, err
	}
	row, err := r.queries.GetMarkStudentByID(ctx, dbUUID(studentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Student{}, ErrStudentNotFound
	}
	if err != nil {
		return Student{}, err
	}
	return mapStudent(row.ID, row.RollNo, row.Name, row.Branch, row.Section, row.Batch)
}

type CardTokenResolver struct {
	service interface {
		ResolveToken(context.Context, cardtoken.ResolveInput) (cardtoken.ResolvedStudent, error)
	}
}

func NewCardTokenResolver(ledger cardtoken.Ledger, keyring cardtoken.Keyring) *CardTokenResolver {
	return &CardTokenResolver{service: cardtoken.NewService(nil, ledger, nil, keyring)}
}

func (r *CardTokenResolver) Resolve(ctx context.Context, _ Actor, input CardInput) (Student, error) {
	resolved, err := r.service.ResolveToken(ctx, cardtoken.ResolveInput{Version: input.Version, Token: input.Token})
	if err != nil {
		var de domain.DomainError
		if errors.As(err, &de) && de.Code == contracts.ErrorCodeCARDNOTRECOGNIZED {
			return Student{}, ErrCardNotRecognized
		}
		return Student{}, err
	}
	return Student{
		StudentID:   resolved.StudentID,
		RollNo:      resolved.RollNo,
		DisplayName: resolved.DisplayName,
		Branch:      resolved.Branch,
		Section:     resolved.Section,
		Batch:       resolved.Batch,
	}, nil
}

func mapStudent(id pgtype.UUID, rollNo, name, branch, section, batch string) (Student, error) {
	if !id.Valid {
		return Student{}, invalidRowError("student")
	}
	return Student{
		StudentID:   uuid.UUID(id.Bytes).String(),
		RollNo:      rollNo,
		DisplayName: name,
		Branch:      branch,
		Section:     section,
		Batch:       batch,
	}, nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
