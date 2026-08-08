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

// maxRecordRows caps the underlying row read.
//
// A ceiling rather than a page parameter: pagination cannot be reconciled with
// day grouping without splitting a day across pages, while still bounding the
// response of an endpoint anyone can call.
//
// SIZED AGAINST THE RANGE, not against a single day. The old value of 500 was
// justified as "over forty event-days for one student — far beyond any realistic
// history", which was true while an event was one day. It stopped being true
// under Story 0.12a: an event may now span maxEventRangeDays (90) and a student
// can hold a mark in each of the 12 slots, so ONE event alone can produce 1080
// rows. At 500 the query — which orders by od_date DESC — would silently drop
// the EARLIEST days, and the screen whose entire purpose is never to read as "I
// was not marked" would do exactly that for day 1 onwards, with nothing on
// screen saying it had truncated.
//
// 1200 covers a full-length event outright and leaves headroom. Keep it at or
// above 90 * 12 if the range cap in service/event/service.go changes.
const maxRecordRows = 1200

// MaxPublicEventRows is exported so the integration suite can seed PAST the cap
// and assert it. It was unexported and the cap was therefore unasserted —
// deleting the LIMIT from ListPublicEvents left every test green.
const MaxPublicEventRows = 100

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
	RecordID  string
	EventID   string
	EventName string
	// EventDate is the EVENT's first day; ODDate is the day this record belongs
	// to. On a multi-day event they differ, and the dispute form builds its event
	// options from the former while the screen groups on the latter.
	EventDate    string
	ODDate       string
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

// Day groups one day's records.
type Day struct {
	// ODDate is YYYY-MM-DD — the day the OD belongs to, taken from the record.
	// Not the event's first day, which would collapse a thirty-day festival onto
	// one heading, and not the mark's receipt time, which would file a
	// next-morning correction under the wrong day.
	ODDate  string
	Records []Record
}

// RecordCollection is the whole public view of one student.
type RecordCollection struct {
	Student     Student
	Days        []Day
	GeneratedAt time.Time
}

type EventCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Event struct {
	EventID string
	Name    string
	// EventDate and EndDate bound the days the dispute form may offer.
	EventDate  string
	EndDate    string
	Categories []EventCategory
}

type EventCollection struct {
	Data []Event
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

func (s *Service) PublicEvents(ctx context.Context) (EventCollection, error) {
	rows, err := s.queries.ListPublicEvents(ctx, MaxPublicEventRows)
	if err != nil {
		return EventCollection{}, err
	}
	data := make([]Event, 0, len(rows))
	for _, row := range rows {
		var categories []EventCategory
		if err := json.Unmarshal([]byte(row.CategoriesJson), &categories); err != nil {
			return EventCollection{}, err
		}
		data = append(data, Event{
			EventID:    uuid.UUID(row.ID.Bytes).String(),
			Name:       row.Name,
			EventDate:  row.EventDate.Time.Format(time.DateOnly),
			EndDate:    row.EndDate.Time.Format(time.DateOnly),
			Categories: categories,
		})
	}
	return EventCollection{Data: data}, nil
}

// groupByDay folds the ordered rows into days, PRESERVING SQL order.
//
// A single pass that opens a new day whenever the date changes — not a map,
// which would lose the ordering the query established and force a re-sort here.
// The query already returns od_date DESC, slot ASC; re-deriving that order in
// Go (or, worse, in the browser) is how the two drift apart.
func groupByDay(rows []sqlc.ListPublicStudentODRecordsRow) []Day {
	days := make([]Day, 0, len(rows))
	for _, row := range rows {
		record := mapRecord(row)
		if n := len(days); n > 0 && days[n-1].ODDate == record.ODDate {
			days[n-1].Records = append(days[n-1].Records, record)
			continue
		}
		days = append(days, Day{ODDate: record.ODDate, Records: []Record{record}})
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
		ODDate:       row.OdDate.Time.Format(time.DateOnly),
		CategoryID:   uuid.UUID(row.CategoryID.Bytes).String(),
		CategoryName: row.CategoryName,
		Slot:         int(row.Slot),
		MarkedByName: markedByName,
		MarkedByRole: row.MarkedByRole,
		MarkedAt:     markedAt,
	}
}
