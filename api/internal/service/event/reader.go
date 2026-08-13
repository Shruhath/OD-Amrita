package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	return mapEvent(row.ID, row.Name, row.EventDate, row.EndDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
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
		event, mapErr := mapEvent(row.ID, row.Name, row.EventDate, row.EndDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
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
		category, mapErr := mapCategory(row.ID, row.EventID, row.Name, row.VisibleOnPublicDashboard, row.CreatedAt)
		if mapErr != nil {
			return nil, mapErr
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func (r *SQLCReader) GetCategory(ctx context.Context, id uuid.UUID) (Category, error) {
	row, err := r.queries.GetCategoryByID(ctx, dbUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Category{}, ErrCategoryNotFound
		}
		return Category{}, err
	}
	return mapCategory(row.ID, row.EventID, row.Name, row.VisibleOnPublicDashboard, row.CreatedAt)
}

func (r *SQLCReader) ListCoordinatorAssignments(ctx context.Context, eventID uuid.UUID) ([]CoordinatorAssignment, error) {
	rows, err := r.queries.ListCoordinatorAssignmentsByEvent(ctx, dbUUID(eventID))
	if err != nil {
		return nil, err
	}
	assignments := make([]CoordinatorAssignment, 0, len(rows))
	for _, row := range rows {
		categories, categoryErr := parseCategoriesJSON(row.CategoriesJson)
		if categoryErr != nil {
			return nil, categoryErr
		}
		assignment, mapErr := mapCoordinatorAssignment(
			row.ID, row.UserID, row.Email, row.Name, row.EventID, categories, row.CreatedAt,
		)
		if mapErr != nil {
			return nil, mapErr
		}
		assignments = append(assignments, assignment)
	}
	return assignments, nil
}

func (r *SQLCReader) GetCoordinatorAssignment(ctx context.Context, eventID, userID uuid.UUID) (CoordinatorAssignment, error) {
	row, err := r.queries.GetCoordinatorAssignmentByEventAndUser(ctx, sqlc.GetCoordinatorAssignmentByEventAndUserParams{
		EventID: dbUUID(eventID),
		UserID:  dbUUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CoordinatorAssignment{}, ErrCoordinatorUserNotFound
		}
		return CoordinatorAssignment{}, err
	}
	categories, categoryErr := parseCategoriesJSON(row.CategoriesJson)
	if categoryErr != nil {
		return CoordinatorAssignment{}, categoryErr
	}
	return mapCoordinatorAssignment(row.ID, row.UserID, row.Email, row.Name, row.EventID, categories, row.CreatedAt)
}

func (r *SQLCReader) GetCaptureCreditConfig(ctx context.Context, eventID uuid.UUID) (CaptureCreditConfig, error) {
	row, err := r.queries.GetCaptureCreditConfig(ctx, dbUUID(eventID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CaptureCreditConfig{}, ErrEventNotFound
		}
		return CaptureCreditConfig{}, err
	}
	return mapCaptureCreditConfig(row.EventID, row.CreditLimit, row.UpdatedByUserID, row.UpdatedAt)
}

func (r *SQLCReader) ListCaptureCreditUsageByEvent(ctx context.Context, eventID uuid.UUID, usageDate time.Time) ([]CaptureCreditUsage, error) {
	rows, err := r.queries.ListCaptureCreditUsageByEvent(ctx, sqlc.ListCaptureCreditUsageByEventParams{
		EventID:   dbUUID(eventID),
		UsageDate: dbDate(usageDate),
	})
	if err != nil {
		return nil, err
	}
	usage := make([]CaptureCreditUsage, 0, len(rows))
	for _, row := range rows {
		item, mapErr := mapCaptureCreditUsage(row.EventID, row.UserID, row.Email, row.Name, row.CreditLimit, row.Consumed, row.Remaining, row.Exhausted)
		if mapErr != nil {
			return nil, mapErr
		}
		usage = append(usage, item)
	}
	return usage, nil
}

func (r *SQLCReader) GetCaptureCreditUsageForCoordinator(ctx context.Context, eventID, userID uuid.UUID, usageDate time.Time) (CaptureCreditUsage, error) {
	row, err := r.queries.GetCaptureCreditUsageForCoordinator(ctx, sqlc.GetCaptureCreditUsageForCoordinatorParams{
		EventID:   dbUUID(eventID),
		UserID:    dbUUID(userID),
		UsageDate: dbDate(usageDate),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CaptureCreditUsage{}, ErrCoordinatorUserNotFound
		}
		return CaptureCreditUsage{}, err
	}
	return mapCaptureCreditUsage(row.EventID, row.UserID, row.Email, row.Name, row.CreditLimit, row.Consumed, row.Remaining, row.Exhausted)
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
		event, mapErr := mapEvent(row.ID, row.Name, row.EventDate, row.EndDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
		if mapErr != nil {
			return nil, mapErr
		}
		events = append(events, event)
	}
	return events, nil
}

func (r *SQLCReader) ListActiveEventsForMarking(ctx context.Context) ([]Event, error) {
	rows, err := r.queries.ListAdminPortalMarkEvents(ctx)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, mapErr := mapEvent(row.ID, row.Name, row.EventDate, row.EndDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
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

func (r *SQLCReader) CoordinatorAssignedToEventCategory(ctx context.Context, userID, eventID, categoryID uuid.UUID) (bool, error) {
	return r.queries.CoordinatorAssignedToEventCategory(ctx, sqlc.CoordinatorAssignedToEventCategoryParams{
		UserID:     dbUUID(userID),
		EventID:    dbUUID(eventID),
		CategoryID: dbUUID(categoryID),
	})
}

func mapCategory(id, eventID pgtype.UUID, name string, visible bool, createdAt pgtype.Timestamptz) (Category, error) {
	if !id.Valid || !eventID.Valid || !createdAt.Valid {
		return Category{}, errors.New("event: category row contains invalid required values")
	}
	return Category{
		ID:                       uuid.UUID(id.Bytes).String(),
		EventID:                  uuid.UUID(eventID.Bytes).String(),
		Name:                     name,
		VisibleOnPublicDashboard: visible,
		CreatedAt:                createdAt.Time,
	}, nil
}

func mapCoordinatorAssignment(
	id, userID pgtype.UUID,
	email string,
	name pgtype.Text,
	eventID pgtype.UUID,
	categories []Category,
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
		ID:         uuid.UUID(id.Bytes).String(),
		UserID:     uuid.UUID(userID.Bytes).String(),
		Email:      email,
		Name:       displayName,
		EventID:    uuid.UUID(eventID.Bytes).String(),
		Categories: categories,
		CreatedAt:  createdAt.Time,
	}, nil
}

func parseCategoriesJSON(value any) ([]Category, error) {
	var raw []struct {
		ID                       string    `json:"id"`
		EventID                  string    `json:"event_id"`
		Name                     string    `json:"name"`
		VisibleOnPublicDashboard bool      `json:"visible_on_public_dashboard"`
		CreatedAt                time.Time `json:"created_at"`
	}
	switch v := value.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &raw); err != nil {
			return nil, err
		}
	case []byte:
		if err := json.Unmarshal(v, &raw); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("event: unsupported categories json type %T", value)
	}
	categories := make([]Category, 0, len(raw))
	for _, item := range raw {
		categories = append(categories, Category{
			ID: item.ID, EventID: item.EventID, Name: item.Name,
			VisibleOnPublicDashboard: item.VisibleOnPublicDashboard,
			CreatedAt:                item.CreatedAt,
		})
	}
	return categories, nil
}

func mapCaptureCreditConfig(id pgtype.UUID, limit int32, updatedBy pgtype.UUID, updatedAt pgtype.Timestamptz) (CaptureCreditConfig, error) {
	if !id.Valid || !updatedAt.Valid {
		return CaptureCreditConfig{}, errors.New("event: capture credit config row contains invalid required values")
	}
	var updatedByUserID *string
	if updatedBy.Valid {
		value := uuid.UUID(updatedBy.Bytes).String()
		updatedByUserID = &value
	}
	return CaptureCreditConfig{
		EventID:         uuid.UUID(id.Bytes).String(),
		Limit:           int(limit),
		UpdatedByUserID: updatedByUserID,
		UpdatedAt:       updatedAt.Time,
	}, nil
}

func mapCaptureCreditUsage(
	eventID, userID pgtype.UUID,
	email string,
	name pgtype.Text,
	limit, consumed, remaining int32,
	exhausted bool,
) (CaptureCreditUsage, error) {
	if !eventID.Valid || !userID.Valid {
		return CaptureCreditUsage{}, errors.New("event: capture credit usage row contains invalid required values")
	}
	var displayName *string
	if name.Valid {
		value := name.String
		displayName = &value
	}
	return CaptureCreditUsage{
		EventID:   uuid.UUID(eventID.Bytes).String(),
		UserID:    uuid.UUID(userID.Bytes).String(),
		Email:     email,
		Name:      displayName,
		Limit:     int(limit),
		Consumed:  int(consumed),
		Remaining: int(remaining),
		Exhausted: exhausted,
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
	endDate pgtype.Date,
	status string,
	archivedAt pgtype.Timestamptz,
	createdAt pgtype.Timestamptz,
	branchesJSON string,
) (Event, error) {
	// endDate is validated as strictly as eventDate: it is NOT NULL in the
	// database, and a zero-valued one would render as "0001-01-01" on every admin
	// surface rather than failing.
	if !id.Valid || !eventDate.Valid || !endDate.Valid || !createdAt.Valid {
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
		EndDate:   endDate.Time.Format("2006-01-02"),
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
