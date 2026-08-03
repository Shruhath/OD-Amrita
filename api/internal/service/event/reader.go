package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

func (r *SQLCReader) ListBranches(ctx context.Context) ([]Branch, error) {
	rows, err := r.queries.ListBranches(ctx)
	if err != nil {
		return nil, err
	}
	branches := make([]Branch, 0, len(rows))
	for _, row := range rows {
		if !row.ID.Valid {
			return nil, errors.New("event: branch row has invalid id")
		}
		branches = append(branches, Branch{ID: uuid.UUID(row.ID.Bytes).String(), Code: row.Code, Name: row.Name})
	}
	return branches, nil
}

func (r *SQLCReader) GetEvent(ctx context.Context, id uuid.UUID) (Event, error) {
	row, err := r.queries.GetEvent(ctx, dbUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Event{}, ErrEventNotFound
		}
		return Event{}, err
	}
	return mapEvent(row.ID, row.Name, row.EventDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
}

func (r *SQLCReader) ListEvents(ctx context.Context, input ListInput) ([]Event, int64, error) {
	total, err := r.queries.CountEvents(ctx, string(input.Status))
	if err != nil {
		return nil, 0, err
	}
	offset, withinRange := pageOffset(total, input.Page, input.PageSize)
	if !withinRange {
		return []Event{}, total, nil
	}
	rows, err := r.queries.ListEvents(ctx, sqlc.ListEventsParams{
		Status:     string(input.Status),
		PageOffset: offset,
		PageSize:   int64(input.PageSize),
	})
	if err != nil {
		return nil, 0, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, mapErr := mapEvent(row.ID, row.Name, row.EventDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
		if mapErr != nil {
			return nil, 0, mapErr
		}
		events = append(events, event)
	}
	return events, total, nil
}

// EventStatus reuses the existing GetEventMutationStatus query rather than
// adding a second status lookup.
func (r *SQLCReader) EventStatus(ctx context.Context, id uuid.UUID) (Status, error) {
	status, err := r.queries.GetEventMutationStatus(ctx, dbUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrEventNotFound
		}
		return "", err
	}
	if status != string(StatusActive) && status != string(StatusArchived) {
		return "", fmt.Errorf("event: row contains invalid status %q", status)
	}
	return Status(status), nil
}

func (r *SQLCReader) ListCategories(ctx context.Context, eventID uuid.UUID) ([]Category, error) {
	rows, err := r.queries.ListCategoriesByEvent(ctx, dbUUID(eventID))
	if err != nil {
		return nil, err
	}
	categories := make([]Category, 0, len(rows))
	for _, row := range rows {
		category, mapErr := mapCategory(row.ID, row.EventID, row.Name, row.CreatedAt)
		if mapErr != nil {
			return nil, mapErr
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func (r *SQLCReader) ListCoordinatorAssignments(ctx context.Context, eventID uuid.UUID) ([]CoordinatorAssignment, error) {
	rows, err := r.queries.ListCoordinatorAssignmentsByEvent(ctx, dbUUID(eventID))
	if err != nil {
		return nil, err
	}
	assignments := make([]CoordinatorAssignment, 0, len(rows))
	for _, row := range rows {
		assignment, mapErr := mapCoordinatorAssignment(
			row.ID, row.UserID, row.Email, row.Name, row.EventID, row.CreatedAt,
		)
		if mapErr != nil {
			return nil, mapErr
		}
		assignments = append(assignments, assignment)
	}
	return assignments, nil
}

func (r *SQLCReader) GetCoordinatorCandidateByEmail(ctx context.Context, email string) (CoordinatorCandidate, error) {
	row, err := r.queries.GetCoordinatorCandidateByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CoordinatorCandidate{}, ErrCoordinatorUserNotFound
		}
		return CoordinatorCandidate{}, err
	}
	return mapCoordinatorCandidate(row.ID, row.Email, row.Name, row.Role)
}

func (r *SQLCReader) CoordinatorAssignmentExists(ctx context.Context, userID, eventID uuid.UUID) (bool, error) {
	return r.queries.CoordinatorAssignmentExists(ctx, sqlc.CoordinatorAssignmentExistsParams{
		UserID:  dbUUID(userID),
		EventID: dbUUID(eventID),
	})
}

func (r *SQLCReader) ListAssignedActiveEvents(ctx context.Context, userID uuid.UUID) ([]Event, error) {
	rows, err := r.queries.ListAssignedActiveEvents(ctx, dbUUID(userID))
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, mapErr := mapEvent(row.ID, row.Name, row.EventDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
		if mapErr != nil {
			return nil, mapErr
		}
		events = append(events, event)
	}
	return events, nil
}

func (r *SQLCReader) CoordinatorAssignedToEvent(ctx context.Context, userID, eventID uuid.UUID) (bool, error) {
	return r.queries.CoordinatorAssignedToEvent(ctx, sqlc.CoordinatorAssignedToEventParams{
		UserID:  dbUUID(userID),
		EventID: dbUUID(eventID),
	})
}

func mapCategory(id, eventID pgtype.UUID, name string, createdAt pgtype.Timestamptz) (Category, error) {
	if !id.Valid || !eventID.Valid || !createdAt.Valid {
		return Category{}, errors.New("event: category row contains invalid required values")
	}
	return Category{
		ID:        uuid.UUID(id.Bytes).String(),
		EventID:   uuid.UUID(eventID.Bytes).String(),
		Name:      name,
		CreatedAt: createdAt.Time,
	}, nil
}

func mapCoordinatorAssignment(
	id, userID pgtype.UUID,
	email string,
	name pgtype.Text,
	eventID pgtype.UUID,
	createdAt pgtype.Timestamptz,
) (CoordinatorAssignment, error) {
	if !id.Valid || !userID.Valid || !eventID.Valid || !createdAt.Valid {
		return CoordinatorAssignment{}, errors.New("event: coordinator assignment row contains invalid required values")
	}
	var displayName *string
	if name.Valid {
		value := name.String
		displayName = &value
	}
	return CoordinatorAssignment{
		ID:        uuid.UUID(id.Bytes).String(),
		UserID:    uuid.UUID(userID.Bytes).String(),
		Email:     email,
		Name:      displayName,
		EventID:   uuid.UUID(eventID.Bytes).String(),
		CreatedAt: createdAt.Time,
	}, nil
}

func mapCoordinatorCandidate(id pgtype.UUID, email string, name pgtype.Text, role string) (CoordinatorCandidate, error) {
	if !id.Valid {
		return CoordinatorCandidate{}, errors.New("event: coordinator candidate row contains invalid id")
	}
	var displayName *string
	if name.Valid {
		value := name.String
		displayName = &value
	}
	return CoordinatorCandidate{ID: uuid.UUID(id.Bytes).String(), Email: email, Name: displayName, Role: role}, nil
}

func pageOffset(total int64, page, pageSize int) (int64, bool) {
	if total <= 0 || page < 1 || pageSize < 1 {
		return 0, false
	}
	pageIndex := int64(page - 1)
	size := int64(pageSize)
	if pageIndex > (total-1)/size {
		return 0, false
	}
	return pageIndex * size, true
}

func mapEvent(
	id pgtype.UUID,
	name string,
	eventDate pgtype.Date,
	status string,
	archivedAt pgtype.Timestamptz,
	createdAt pgtype.Timestamptz,
	branchesJSON string,
) (Event, error) {
	if !id.Valid || !eventDate.Valid || !createdAt.Valid {
		return Event{}, errors.New("event: row contains invalid required values")
	}
	if status != string(StatusActive) && status != string(StatusArchived) {
		return Event{}, fmt.Errorf("event: row contains invalid status %q", status)
	}
	var branches []Branch
	if err := json.Unmarshal([]byte(branchesJSON), &branches); err != nil {
		return Event{}, fmt.Errorf("event: decode branches: %w", err)
	}
	if branches == nil {
		branches = []Branch{}
	}
	event := Event{
		ID:        uuid.UUID(id.Bytes).String(),
		Name:      name,
		EventDate: eventDate.Time.Format("2006-01-02"),
		Status:    Status(status),
		Branches:  branches,
		CreatedAt: createdAt.Time,
	}
	if archivedAt.Valid {
		value := archivedAt.Time
		event.ArchivedAt = &value
	}
	return event, nil
}
