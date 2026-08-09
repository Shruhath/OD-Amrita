package marking

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type PortalMarkReadService struct {
	queries *sqlc.Queries
}

func NewPortalMarkReadService(pool *pgxpool.Pool) *PortalMarkReadService {
	return &PortalMarkReadService{queries: sqlc.New(pool)}
}

func (s *PortalMarkReadService) Events(ctx context.Context, actor Actor) ([]Event, error) {
	if err := assertTeacherOrAdmin(actor); err != nil {
		return nil, err
	}
	if actor.Role == "admin" {
		rows, err := s.queries.ListAdminPortalMarkEvents(ctx)
		if err != nil {
			return nil, err
		}
		events := make([]Event, 0, len(rows))
		for _, row := range rows {
			event, mapErr := eventFromPortalRow(row.ID, row.Name, row.EventDate, row.EndDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
			if mapErr != nil {
				return nil, mapErr
			}
			events = append(events, event)
		}
		return events, nil
	}
	userID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return nil, forbiddenPortal("Teacher principal is invalid.")
	}
	rows, err := s.queries.ListTeacherPortalMarkEvents(ctx, dbUUID(userID))
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, mapErr := eventFromPortalRow(row.ID, row.Name, row.EventDate, row.EndDate, row.Status, row.ArchivedAt, row.CreatedAt, row.BranchesJson)
		if mapErr != nil {
			return nil, mapErr
		}
		events = append(events, event)
	}
	return events, nil
}

func (s *PortalMarkReadService) Categories(ctx context.Context, actor Actor, eventID string) ([]Category, error) {
	if err := assertTeacherOrAdmin(actor); err != nil {
		return nil, err
	}
	parsedEvent, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return nil, portalValidation("event_id", "invalid_format", "event_id must be a UUID.")
	}
	event, err := s.queries.GetMarkEvent(ctx, dbUUID(parsedEvent))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, AsDomainError(ErrEventNotFound)
	} else if err != nil {
		return nil, err
	}
	if event.Status == "archived" {
		return nil, AsDomainError(ErrEventArchived)
	}
	if actor.Role == "admin" {
		rows, err := s.queries.ListAdminPortalMarkCategories(ctx, dbUUID(parsedEvent))
		if err != nil {
			return nil, err
		}
		return categoriesFromAdminRows(rows)
	}
	parsedUser, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return nil, forbiddenPortal("Teacher principal is invalid.")
	}
	rows, err := s.queries.ListTeacherPortalMarkCategories(ctx, sqlc.ListTeacherPortalMarkCategoriesParams{
		EventID: dbUUID(parsedEvent),
		UserID:  dbUUID(parsedUser),
	})
	if err != nil {
		return nil, err
	}
	return categoriesFromTeacherRows(rows)
}

// Attendance is the admin verification read.
//
// odDate is OPTIONAL and narrows the list to one day of a multi-day event. A nil
// odDate must return every day: every caller written before the range existed
// passes nil, and defaulting to today would silently hide the rest of a
// festival from the surface whose whole job is to show it.
// Attendance reads an event's present marks for review.
//
// SCOPED BY THE CALLER'S ROLE, not by the route that reached it. An admin sees
// the whole event. A TEACHER sees only students in the branches they teach —
// the same rule that already decides which students they may mark, because a
// teacher who cannot mark a student has no reason to read that student's record
// either. The scope is applied in the QUERY rather than by filtering rows here,
// so the total and the page are counted over the same set: filtering after the
// fact would page a teacher through somebody else's data and report a total
// that does not match what they can see.
func (s *PortalMarkReadService) Attendance(ctx context.Context, actor Actor, eventID string, categoryID *string, slot *int, odDate *string, page, pageSize int) (AttendancePage, error) {
	if err := assertTeacherOrAdmin(actor); err != nil {
		return AttendancePage{}, err
	}

	// The viewer id is what turns the scope on. It stays NULL for an admin,
	// which is the unscoped read the admin route has always returned.
	var viewer pgtype.UUID
	if actor.Role != "admin" {
		viewerID, parseErr := uuid.Parse(strings.TrimSpace(actor.UserID))
		if parseErr != nil {
			return AttendancePage{}, forbiddenPortal("Teacher principal is invalid.")
		}
		viewer = dbUUID(viewerID)
	}

	parsedEvent, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return AttendancePage{}, portalValidation("event_id", "invalid_format", "event_id must be a UUID.")
	}
	if _, err := s.queries.GetMarkEvent(ctx, dbUUID(parsedEvent)); errors.Is(err, pgx.ErrNoRows) {
		return AttendancePage{}, AsDomainError(ErrEventNotFound)
	} else if err != nil {
		return AttendancePage{}, err
	}

	var parsedCategory pgtype.UUID
	if categoryID != nil && strings.TrimSpace(*categoryID) != "" {
		categoryUUID, err := uuid.Parse(strings.TrimSpace(*categoryID))
		if err != nil {
			return AttendancePage{}, portalValidation("category_id", "invalid_format", "category_id must be a UUID.")
		}
		if _, err := s.queries.GetMarkCategoryForEvent(ctx, sqlc.GetMarkCategoryForEventParams{
			EventID:    dbUUID(parsedEvent),
			CategoryID: dbUUID(categoryUUID),
		}); errors.Is(err, pgx.ErrNoRows) {
			return AttendancePage{}, AsDomainError(ErrCategoryNotFound)
		} else if err != nil {
			return AttendancePage{}, err
		}
		parsedCategory = dbUUID(categoryUUID)
	}

	var parsedSlot pgtype.Int4
	if slot != nil {
		parsedSlot = pgtype.Int4{Int32: int32(*slot), Valid: true}
	}

	var parsedODDate pgtype.Date
	if odDate != nil && strings.TrimSpace(*odDate) != "" {
		trimmed := strings.TrimSpace(*odDate)
		day, parseErr := time.Parse(time.DateOnly, trimmed)
		if parseErr != nil || day.Format(time.DateOnly) != trimmed {
			return AttendancePage{}, portalValidation("od_date", "invalid_format", "od_date must be a valid YYYY-MM-DD date.")
		}
		parsedODDate = pgtype.Date{Time: day, Valid: true}
	}

	total, err := s.queries.CountAttendanceRecords(ctx, sqlc.CountAttendanceRecordsParams{
		EventID:    dbUUID(parsedEvent),
		CategoryID: parsedCategory,
		Slot:       parsedSlot,
		OdDate:     parsedODDate,
		ViewerID:   viewer,
	})
	if err != nil {
		return AttendancePage{}, err
	}
	rows, err := s.queries.ListAttendanceRecords(ctx, sqlc.ListAttendanceRecordsParams{
		EventID:    dbUUID(parsedEvent),
		CategoryID: parsedCategory,
		Slot:       parsedSlot,
		OdDate:     parsedODDate,
		ViewerID:   viewer,
		PageOffset: int64((page - 1) * pageSize),
		PageSize:   int64(pageSize),
	})
	if err != nil {
		return AttendancePage{}, err
	}
	data := make([]AttendanceRecord, 0, len(rows))
	for _, row := range rows {
		record, mapErr := attendanceRecordFromRow(row)
		if mapErr != nil {
			return AttendancePage{}, mapErr
		}
		data = append(data, record)
	}
	return AttendancePage{Data: data, Page: page, PageSize: pageSize, Total: int(total)}, nil
}

func assertTeacherOrAdmin(actor Actor) error {
	switch actor.Role {
	case "teacher", "admin":
		return nil
	default:
		return forbiddenPortal("Teacher or administrator access is required.")
	}
}

func forbiddenPortal(message string) domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: message}
}

func portalValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Portal mark validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func eventFromPortalRow(id pgtype.UUID, name string, eventDate, endDate pgtype.Date, status string, archivedAt, createdAt pgtype.Timestamptz, branchesJSON string) (Event, error) {
	// endDate is checked as strictly as eventDate: a zero value would make range
	// containment empty and close the slot gate on the event rather than fail.
	if !id.Valid || !eventDate.Valid || !endDate.Valid || !createdAt.Valid {
		return Event{}, errors.New("marking: portal event row contains invalid required values")
	}
	var raw []Branch
	if err := json.Unmarshal([]byte(branchesJSON), &raw); err != nil {
		return Event{}, err
	}
	branches := make([]Branch, 0, len(raw))
	for _, branch := range raw {
		branches = append(branches, Branch{ID: branch.ID, Code: branch.Code, Name: branch.Name})
	}
	var archived *time.Time
	if archivedAt.Valid {
		archived = &archivedAt.Time
	}
	return Event{
		EventID:    uuid.UUID(id.Bytes).String(),
		Name:       name,
		EventDate:  eventDate.Time,
		EndDate:    endDate.Time,
		Status:     status,
		Branches:   branches,
		CreatedAt:  createdAt.Time,
		ArchivedAt: archived,
	}, nil
}

func categoriesFromAdminRows(rows []sqlc.ListAdminPortalMarkCategoriesRow) ([]Category, error) {
	categories := make([]Category, 0, len(rows))
	for _, row := range rows {
		category, err := portalCategory(row.ID, row.EventID, row.Name, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func categoriesFromTeacherRows(rows []sqlc.ListTeacherPortalMarkCategoriesRow) ([]Category, error) {
	categories := make([]Category, 0, len(rows))
	for _, row := range rows {
		category, err := portalCategory(row.ID, row.EventID, row.Name, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func portalCategory(id, eventID pgtype.UUID, name string, createdAt pgtype.Timestamptz) (Category, error) {
	if !id.Valid || !eventID.Valid || !createdAt.Valid {
		return Category{}, errors.New("marking: portal category row contains invalid required values")
	}
	return Category{
		CategoryID: uuid.UUID(id.Bytes).String(),
		EventID:    uuid.UUID(eventID.Bytes).String(),
		Name:       name,
		CreatedAt:  createdAt.Time,
	}, nil
}

func attendanceRecordFromRow(row sqlc.ListAttendanceRecordsRow) (AttendanceRecord, error) {
	if !row.ID.Valid || !row.StudentID.Valid || !row.EventID.Valid || !row.CategoryID.Valid || !row.CategoryCreatedAt.Valid || !row.OdDate.Valid || !row.ServerReceivedAt.Valid {
		return AttendanceRecord{}, errors.New("marking: attendance row contains invalid required values")
	}

	var markedBy AttendanceActor
	if row.ActorUserID.Valid {
		userID := uuid.UUID(row.ActorUserID.Bytes).String()
		markedBy.UserID = &userID
	}
	if row.ActorEmail.Valid {
		email := row.ActorEmail.String
		markedBy.Email = &email
	}
	if row.ActorName.Valid {
		name := row.ActorName.String
		markedBy.Name = &name
	}

	return AttendanceRecord{
		RecordID: uuid.UUID(row.ID.Bytes).String(),
		Student: Student{
			StudentID:   uuid.UUID(row.StudentID.Bytes).String(),
			RollNo:      row.RollNo,
			DisplayName: row.StudentName,
			Branch:      row.Branch,
			Section:     row.Section,
			Batch:       row.Batch,
		},
		EventID: uuid.UUID(row.EventID.Bytes).String(),
		Category: Category{
			CategoryID: uuid.UUID(row.CategoryID.Bytes).String(),
			EventID:    uuid.UUID(row.EventID.Bytes).String(),
			Name:       row.CategoryName,
			CreatedAt:  row.CategoryCreatedAt.Time,
		},
		Slot:             int(row.Slot),
		ODDate:           row.OdDate.Time.Format(time.DateOnly),
		CaptureMethod:    row.CaptureMethod,
		MarkedBy:         markedBy,
		ActorRole:        row.ActorRole,
		ServerReceivedAt: row.ServerReceivedAt.Time,
	}, nil
}
