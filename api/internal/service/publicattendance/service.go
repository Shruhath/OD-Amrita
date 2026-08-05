// Package publicattendance serves one student's OD records to a caller with no
// credential (Story 0.10, FR39).
//
// A SEPARATE PACKAGE ON PURPOSE. The obvious home would be service/marking,
// which already reads od_records for the admin attendance view — but that
// package is the coordinator scope seam (VisibleEvents/AssertCanMark), and every
// read in it is defined in terms of an actor's scope. This read has no actor at
// all. Putting an actor-less path inside the seam would make "which reads are
// scope-checked" a question you have to answer file by file instead of by
// package, which is exactly the property 0.6/0.7 built the seam to get.
//
// READ ONLY. Nothing here writes od_records and nothing goes through
// store.WithAudit: a public read has no actor to attribute and no state change
// to record, so an audit row here would be an unauthenticated write path. That
// invariant is asserted against real Postgres.
package publicattendance

import (
	"context"
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

// maxRecordRows caps the underlying row read.
//
// A ceiling rather than a page parameter: pagination cannot be reconciled with
// day grouping without splitting a day across pages, and at 12 slots a day this
// is over forty event-days for one student — far beyond any realistic history,
// while still bounding the response of an endpoint anyone can call.
const maxRecordRows = 500

// Student is the display identity the dashboard shows. No email: `students` has
// no such column, and the shape must stay unable to carry one.
type Student struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

// Record is one present OD row as a student sees it.
type Record struct {
	RecordID     string
	EventID      string
	EventName    string
	EventDate    string
	CategoryID   string
	CategoryName string
	Slot         int
	// MarkedByName is nil when the actor's user row is gone: actor_user_id is
	// ON DELETE SET NULL, so an off-boarded teacher must leave the record
	// readable with no name rather than delete it.
	MarkedByName *string
	MarkedByRole string
	MarkedAt     time.Time
}

// Day groups one event date's records.
type Day struct {
	// EventDate is YYYY-MM-DD — the calendar day the OD was for, taken from the
	// event, never from the mark's receipt time.
	EventDate string
	Records   []Record
}

// RecordCollection is the whole public view of one student.
type RecordCollection struct {
	Student     Student
	Days        []Day
	GeneratedAt time.Time
}

// Service reads the public attendance view.
type Service struct {
	queries *sqlc.Queries
	now     func() time.Time
}

// NewSQLCService binds the runtime pool.
func NewSQLCService(pool *pgxpool.Pool) *Service {
	return &Service{queries: sqlc.New(pool), now: time.Now}
}

// Records returns one student's present OD records grouped by event date.
//
// An unknown student and a student with no marks are DIFFERENT answers: the
// first is STUDENT_NOT_FOUND, the second is a successful read with an empty day
// list. Collapsing them would tell someone who mistyped their roll number that
// they were not marked, which is the most damaging wrong answer this page can
// give.
func (s *Service) Records(ctx context.Context, studentID string) (RecordCollection, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(studentID))
	if err != nil {
		return RecordCollection{}, domain.DomainError{
			Code:    contracts.ErrorCodeVALIDATIONFAILED,
			Message: "Public attendance lookup validation failed.",
			Details: []contracts.ValidationErrorDetail{{
				Field: "student_id", Code: "invalid_format", Message: "student_id must be a UUID.",
			}},
		}
	}
	id := pgtype.UUID{Bytes: parsed, Valid: true}

	// The student is looked up FIRST and through the existing GetMarkStudentByID
	// query — there is no second student-by-id query in this codebase and there
	// must not be. Resolving the student before the records read is also what
	// makes the empty-vs-unknown distinction possible at all.
	studentRow, err := s.queries.GetMarkStudentByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecordCollection{}, domain.DomainError{
			Code:    contracts.ErrorCodeSTUDENTNOTFOUND,
			Message: "No student matches that identifier.",
		}
	}
	if err != nil {
		return RecordCollection{}, err
	}

	rows, err := s.queries.ListPublicStudentODRecords(ctx, sqlc.ListPublicStudentODRecordsParams{
		StudentID: id,
		RowLimit:  maxRecordRows,
	})
	if err != nil {
		return RecordCollection{}, err
	}

	return RecordCollection{
		Student: Student{
			StudentID:   parsed.String(),
			RollNo:      studentRow.RollNo,
			DisplayName: studentRow.Name,
			Branch:      studentRow.Branch,
			Section:     studentRow.Section,
			Batch:       studentRow.Batch,
		},
		Days:        groupByDay(rows),
		GeneratedAt: s.now().UTC(),
	}, nil
}

// groupByDay folds the ordered rows into days, PRESERVING SQL order.
//
// A single pass that opens a new day whenever the date changes — not a map,
// which would lose the ordering the query established and force a re-sort here.
// The query already returns event_date DESC, slot ASC; re-deriving that order in
// Go (or, worse, in the browser) is how the two drift apart.
func groupByDay(rows []sqlc.ListPublicStudentODRecordsRow) []Day {
	days := make([]Day, 0, len(rows))
	for _, row := range rows {
		record := mapRecord(row)
		if n := len(days); n > 0 && days[n-1].EventDate == record.EventDate {
			days[n-1].Records = append(days[n-1].Records, record)
			continue
		}
		days = append(days, Day{EventDate: record.EventDate, Records: []Record{record}})
	}
	return days
}

func mapRecord(row sqlc.ListPublicStudentODRecordsRow) Record {
	var markedByName *string
	if row.MarkedByName.Valid {
		value := row.MarkedByName.String
		markedByName = &value
	}
	var markedAt time.Time
	if row.ServerReceivedAt.Valid {
		markedAt = row.ServerReceivedAt.Time
	}
	return Record{
		RecordID:     uuid.UUID(row.ID.Bytes).String(),
		EventID:      uuid.UUID(row.EventID.Bytes).String(),
		EventName:    row.EventName,
		EventDate:    row.EventDate.Time.Format(time.DateOnly),
		CategoryID:   uuid.UUID(row.CategoryID.Bytes).String(),
		CategoryName: row.CategoryName,
		Slot:         int(row.Slot),
		MarkedByName: markedByName,
		MarkedByRole: row.MarkedByRole,
		MarkedAt:     markedAt,
	}
}
