package dispute

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

const duplicateConstraint = "uq_disputes_open_student_event_date_slot"

type SQLCRepository struct {
	queries *sqlc.Queries
}

func NewSQLCRepository(pool *pgxpool.Pool) *SQLCRepository {
	return &SQLCRepository{queries: sqlc.New(pool)}
}

func NewSQLCService(pool *pgxpool.Pool, audited AuditStore, allowedDomain string) *Service {
	repo := NewSQLCRepository(pool)
	return NewService(repo, repo, audited, allowedDomain)
}

func (r *SQLCRepository) StudentByRollNo(ctx context.Context, rollNo string) (Student, error) {
	row, err := r.queries.GetStudentByRollNo(ctx, rollNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return Student{}, domain.DomainError{Code: contracts.ErrorCodeSTUDENTNOTFOUND, Message: "No such student."}
	}
	if err != nil {
		return Student{}, err
	}
	return Student{
		StudentID:   uuid.UUID(row.ID.Bytes).String(),
		RollNo:      row.RollNo,
		DisplayName: row.Name,
		Branch:      row.Branch,
		Section:     row.Section,
		Batch:       row.Batch,
	}, nil
}

func (r *SQLCRepository) Insert(ctx context.Context, tx pgx.Tx, write InsertWrite) (Record, error) {
	q := r.queries
	if tx != nil {
		q = sqlc.New(tx)
	}
	row, err := q.InsertDispute(ctx, sqlc.InsertDisputeParams{
		StudentID:              uuidParam(write.StudentID),
		EventID:                uuidParam(write.EventID),
		CategoryID:             uuidParam(write.CategoryID),
		OdDate:                 dateParam(write.ODDate),
		Slot:                   int32(write.Slot),
		SubmittedEmail:         write.SubmittedEmail,
		ClaimedCoordinatorName: textParam(write.ClaimedCoordinatorName),
		StudentNote:            textParam(write.StudentNote),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch {
			case pgErr.Code == "23505" && pgErr.ConstraintName == duplicateConstraint:
				return Record{}, domain.DomainError{Code: contracts.ErrorCodeDISPUTEALREADYOPEN, Message: "A dispute is already open for this slot."}
			case pgErr.Code == "23503" && pgErr.ConstraintName == "disputes_category_event_fk":
				return Record{}, domain.DomainError{Code: contracts.ErrorCodeCATEGORYNOTFOUND, Message: "Category not found for this event."}
			case pgErr.Code == "23503" && pgErr.ConstraintName == "disputes_student_id_fkey":
				// The roster row vanished between the identity lookup and the
				// insert. Telling the submitter their EVENT does not exist would
				// be a wrong answer to a race; 0.7's review settled that a
				// vanished-actor race is internal, not a caller-facing 404.
				return Record{}, domain.DomainError{Code: contracts.ErrorCodeINTERNAL, Message: "The submission could not be recorded. Please try again."}
			case pgErr.Code == "23503":
				return Record{}, domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event or referenced row not found."}
			case pgErr.Code == "23514" && pgErr.ConstraintName == "disputes_od_date_event_range_ck":
				return Record{}, validation("od_date", "out_of_range", "OD date must be within the event range.")
			case pgErr.Code == "23502":
				// A NOT NULL violation means a required value arrived as NULL —
				// caller input, not a server fault. The handler now rejects the
				// absent/nil forms at the edge, so this is the backstop rather
				// than the primary guard, and it must not surface as a 500.
				return Record{}, validation(strings.TrimSpace(pgErr.ColumnName), "required", "A required field was missing.")
			case pgErr.Code == "22021" || pgErr.Code == "22P05":
				// Invalid byte sequence / untranslatable character: a NUL or
				// other control byte in free text. Also caller input.
				return Record{}, validation("student_note", "invalid_characters", "Text fields must not contain control characters.")
			}
		}
		return Record{}, err
	}
	return Record{
		DisputeID:   uuid.UUID(row.ID.Bytes).String(),
		Reference:   reference(uuid.UUID(row.ID.Bytes).String()),
		Status:      "open",
		SubmittedAt: row.CreatedAt.Time,
		Student:     write.Student,
	}, nil
}

func (r *SQLCRepository) OpenForSlot(ctx context.Context, studentID, eventID, odDate string, slot int) (Record, error) {
	row, err := r.queries.GetOpenDisputeForSlot(ctx, sqlc.GetOpenDisputeForSlotParams{
		StudentID: uuidParam(studentID),
		EventID:   uuidParam(eventID),
		OdDate:    dateParam(odDate),
		Slot:      int32(slot),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, domain.DomainError{Code: contracts.ErrorCodeDISPUTENOTFOUND, Message: "No open dispute."}
	}
	if err != nil {
		return Record{}, err
	}
	id := uuid.UUID(row.ID.Bytes).String()
	return Record{DisputeID: id, Reference: reference(id), Status: "open", SubmittedAt: row.CreatedAt.Time}, nil
}

// pageOffset is the same overflow guard service/event/reader.go uses, and for
// the same reason: `(page-1)*pageSize` evaluated in `int` wraps NEGATIVE for a
// large page, and Postgres answers a negative OFFSET with an error, so a caller
// could turn `?page=9223372036854775807` into a 500. Counting first and
// rejecting an out-of-range page keeps the arithmetic in int64 and never asks
// the database for a page that cannot exist.
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

func (r *SQLCRepository) ListForAdmin(ctx context.Context, filter ListFilter) ([]Record, int, error) {
	filter = normalizeFilter(filter)
	total, err := r.queries.CountDisputesForAdmin(ctx, filter.Status)
	if err != nil {
		return nil, 0, err
	}
	offset, ok := pageOffset(total, filter.Page, filter.PageSize)
	if !ok {
		return []Record{}, int(total), nil
	}
	rows, err := r.queries.ListDisputesForAdmin(ctx, sqlc.ListDisputesForAdminParams{
		Status:     filter.Status,
		PageOffset: offset,
		PageSize:   int64(filter.PageSize),
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, recordFromAdmin(row))
	}
	return out, int(total), nil
}

func (r *SQLCRepository) ListForTeacher(ctx context.Context, actorUserID string, filter ListFilter) ([]Record, int, error) {
	filter = normalizeFilter(filter)
	total, err := r.queries.CountDisputesForTeacher(ctx, sqlc.CountDisputesForTeacherParams{
		TeacherUserID: uuidParam(actorUserID),
		Status:        filter.Status,
	})
	if err != nil {
		return nil, 0, err
	}
	offset, ok := pageOffset(total, filter.Page, filter.PageSize)
	if !ok {
		return []Record{}, int(total), nil
	}
	rows, err := r.queries.ListDisputesForTeacher(ctx, sqlc.ListDisputesForTeacherParams{
		TeacherUserID: uuidParam(actorUserID),
		Status:        filter.Status,
		PageOffset:    offset,
		PageSize:      int64(filter.PageSize),
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, recordFromTeacher(row))
	}
	return out, int(total), nil
}

func (r *SQLCRepository) ForResolution(ctx context.Context, tx pgx.Tx, actor Actor, disputeID string) (Record, error) {
	q := r.queries
	if tx != nil {
		q = sqlc.New(tx)
	}
	id := uuidParam(disputeID)
	if actor.Role == "teacher" {
		ok, err := q.TeacherCanResolveDispute(ctx, sqlc.TeacherCanResolveDisputeParams{
			TeacherUserID: uuidParam(actor.UserID),
			DisputeID:     id,
		})
		if err != nil {
			return Record{}, err
		}
		if !ok {
			return Record{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher may resolve only disputes in assigned branches."}
		}
	}
	row, err := q.GetDisputeByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, disputeNotFound()
	}
	if err != nil {
		return Record{}, err
	}
	return recordFromGet(row), nil
}

// LockedStatus uses GetDisputeForResolution — the `FOR UPDATE OF d` query the
// query file has always declared. It was generated and then never called, so the
// row lock the resolve path documents did not actually exist; the 409-vs-404
// answer was decided by a re-read after the transaction had already committed.
func (r *SQLCRepository) LockedStatus(ctx context.Context, tx pgx.Tx, disputeID string) (string, bool, error) {
	q := r.queries
	if tx != nil {
		q = sqlc.New(tx)
	}
	row, err := q.GetDisputeForResolution(ctx, uuidParam(disputeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Status, true, nil
}

func (r *SQLCRepository) Resolve(ctx context.Context, tx pgx.Tx, write ResolveWrite) (int64, error) {
	q := r.queries
	if tx != nil {
		q = sqlc.New(tx)
	}
	if write.Actor.Role == "teacher" {
		ok, err := q.TeacherCanResolveDispute(ctx, sqlc.TeacherCanResolveDisputeParams{
			TeacherUserID: uuidParam(write.Actor.UserID),
			DisputeID:     uuidParam(write.DisputeID),
		})
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher may resolve only disputes in assigned branches."}
		}
	}
	return q.ResolveDispute(ctx, sqlc.ResolveDisputeParams{
		ResolutionNote:   pgtype.Text{String: write.Note, Valid: true},
		ResolvedByUserID: uuidParam(write.Actor.UserID),
		ResolvedAt:       pgtype.Timestamptz{Time: write.ResolvedAt, Valid: true},
		DisputeID:        uuidParam(write.DisputeID),
	})
}

func uuidParam(value string) pgtype.UUID {
	parsed, _ := uuid.Parse(value)
	return pgtype.UUID{Bytes: parsed, Valid: parsed != uuid.Nil}
}

func dateParam(value string) pgtype.Date {
	parsed, _ := time.Parse(time.DateOnly, value)
	return pgtype.Date{Time: parsed, Valid: !parsed.IsZero()}
}

func textParam(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

func uuidPtr(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	v := uuid.UUID(value.Bytes).String()
	return &v
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time
	return &v
}

func recordCommon(
	id pgtype.UUID,
	submittedEmail string,
	eventID pgtype.UUID,
	eventName string,
	eventDate pgtype.Date,
	categoryID pgtype.UUID,
	categoryName string,
	odDate pgtype.Date,
	slot int32,
	claimed pgtype.Text,
	note pgtype.Text,
	status string,
	createdAt pgtype.Timestamptz,
	resolution pgtype.Text,
	resolvedBy pgtype.UUID,
	resolvedAt pgtype.Timestamptz,
	studentID pgtype.UUID,
	rollNo, studentName, branch, section, batch string,
) Record {
	disputeID := uuid.UUID(id.Bytes).String()
	return Record{
		DisputeID:              disputeID,
		Reference:              reference(disputeID),
		SubmittedEmail:         submittedEmail,
		EventID:                uuid.UUID(eventID.Bytes).String(),
		EventName:              eventName,
		EventDate:              eventDate.Time.Format(time.DateOnly),
		CategoryID:             uuid.UUID(categoryID.Bytes).String(),
		CategoryName:           categoryName,
		ODDate:                 odDate.Time.Format(time.DateOnly),
		Slot:                   int(slot),
		ClaimedCoordinatorName: textPtr(claimed),
		StudentNote:            textPtr(note),
		Status:                 status,
		SubmittedAt:            createdAt.Time,
		ResolutionNote:         textPtr(resolution),
		ResolvedByUserID:       uuidPtr(resolvedBy),
		ResolvedAt:             timePtr(resolvedAt),
		Student: Student{
			StudentID:   uuid.UUID(studentID.Bytes).String(),
			RollNo:      rollNo,
			DisplayName: studentName,
			Branch:      branch,
			Section:     section,
			Batch:       batch,
		},
	}
}

func recordFromAdmin(row sqlc.ListDisputesForAdminRow) Record {
	return recordCommon(row.ID, row.SubmittedEmail, row.EventID, row.EventName, row.EventDate, row.CategoryID, row.CategoryName, row.OdDate, row.Slot, row.ClaimedCoordinatorName, row.StudentNote, row.Status, row.CreatedAt, row.ResolutionNote, row.ResolvedByUserID, row.ResolvedAt, row.StudentID, row.RollNo, row.StudentName, row.Branch, row.Section, row.Batch)
}

func recordFromTeacher(row sqlc.ListDisputesForTeacherRow) Record {
	return recordCommon(row.ID, row.SubmittedEmail, row.EventID, row.EventName, row.EventDate, row.CategoryID, row.CategoryName, row.OdDate, row.Slot, row.ClaimedCoordinatorName, row.StudentNote, row.Status, row.CreatedAt, row.ResolutionNote, row.ResolvedByUserID, row.ResolvedAt, row.StudentID, row.RollNo, row.StudentName, row.Branch, row.Section, row.Batch)
}

func recordFromGet(row sqlc.GetDisputeByIDRow) Record {
	return recordCommon(row.ID, row.SubmittedEmail, row.EventID, row.EventName, row.EventDate, row.CategoryID, row.CategoryName, row.OdDate, row.Slot, row.ClaimedCoordinatorName, row.StudentNote, row.Status, row.CreatedAt, row.ResolutionNote, row.ResolvedByUserID, row.ResolvedAt, row.StudentID, row.RollNo, row.StudentName, row.Branch, row.Section, row.Batch)
}
