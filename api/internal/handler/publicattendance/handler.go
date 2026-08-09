// Package publicattendance is the HTTP edge of the UNAUTHENTICATED public
// attendance dashboard (Story 0.10, FR38-FR40).
//
// Everything these two handlers return is served to the open internet with no
// credential, so the mapping below is written as an allowlist: each contract
// struct is built field by field from the service's own display types, and none
// of those types has an email, an actor id, a reason note, a card token or a
// fingerprint to leak in the first place. There is no `interface{}`/map passthrough anywhere
// in this file for exactly that reason — a passthrough would publish whatever
// the query happened to select.
//
// Every route here is a READ. There is no POST in this package, and none may be
// added: a write reachable without authentication is an open door, not a
// feature.
package publicattendance

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// PublicSearchService is the actor-less roster search.
//
// The narrow interface is the point: it exposes PublicSearch and nothing else,
// so this handler cannot reach the authenticated Search — the one that takes an
// Actor and enforces assertMaySearch — even by accident.
type PublicSearchService interface {
	PublicSearch(ctx context.Context, query string) ([]svcstudent.SearchResult, error)
}

// RecordsService reads one student's public OD records.
type RecordsService interface {
	Records(ctx context.Context, studentID string) (publicattendance.RecordCollection, error)
}

// DayService reads one campus day's public attendance board.
type DayService interface {
	Day(ctx context.Context, filter publicattendance.DayFilter) (publicattendance.DayAttendance, error)
}

// RegisterPublicRoutes mounts the public reads.
//
// `group` MUST be a group built from the ROOT gin router (see newPublicGroup in
// cmd/api), never a child of the authenticated /api/v1 group. gin attaches
// middleware to the group value rather than to the path prefix, so mounting
// these on `api.Group("/public")` produces the identical URLs behind the auth
// middleware and 401s every student. A route-composition test is what proves
// which one happened.
// `boardGroup` carries the board's OWN rate-limit bucket, INSTEAD of the
// group's — pass nil to put the board on the same group as the rest.
//
// It exists because the group limiter is keyed on client IP and CAMPUS WIFI NATS
// EVERY STUDENT BEHIND ONE. The whole campus therefore shares a single bucket,
// and the board is the one endpoint the whole campus opens at once — at the
// group's default of 30/minute, roughly fifteen students with the page open
// exhaust it and everybody else gets a 429.
//
// Raising the GROUP limit instead would have loosened the roster search by the
// same amount, and that endpoint is the directory oracle the search's whole
// design is about. So the board gets a generous bucket of its own and the
// search keeps the strict one — the same shape the dispute write already uses,
// from the opposite direction.
//
// The board can afford it: it is one cached answer that is identical for every
// visitor, so a flood costs a map lookup rather than a query. It is a bound
// against a runaway client, not against a crowd.
func RegisterPublicRoutes(group, boardGroup gin.IRouter, search PublicSearchService, records RecordsService, day DayService) {
	group.GET("/students/search", SearchHandler(search))
	group.GET("/students/:student_id/od-records", RecordsHandler(records))
	// A separate GROUP, not an extra middleware on this one. gin runs a group's
	// middleware for every route registered on it, so adding a second limiter
	// here would leave the board bounded by the STRICTER of the two — which is
	// the group's, and is the whole problem. The board is registered on its own
	// group from the same root router, so the two buckets are genuinely
	// independent. A route-composition test asserts that rather than trusting
	// this comment.
	if boardGroup == nil {
		boardGroup = group
	}
	boardGroup.GET("/od-records", DayHandler(day))
}

// DayHandler answers the public attendance board for one campus day.
//
// The day, the slot and the category are the ONLY things a caller may vary.
// There is no page, no limit and no free-text parameter: this response is a
// roster for a day, and every knob the open internet can turn on a roster read
// is a knob that eventually gets turned all the way.
func DayHandler(svc DayService) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter := publicattendance.DayFilter{
			// Passed through as typed; the service owns the default (today on
			// campus) and the parse, so a non-HTTP caller gets the same rules.
			ODDate:     strings.TrimSpace(c.Query("od_date")),
			CategoryID: strings.TrimSpace(c.Query("category_id")),
		}

		if raw := strings.TrimSpace(c.Query("slot")); raw != "" {
			slot, err := strconv.Atoi(raw)
			if err != nil || slot < 1 || slot > 12 {
				fail(c, searchValidation("slot", "out_of_range", "slot must be between 1 and 12."))
				return
			}
			filter.Slot = slot
		}

		board, err := svc.Day(c.Request.Context(), filter)
		if err != nil {
			fail(c, err)
			return
		}

		rendered, convErr := dayAttendanceContract(board)
		if convErr != nil {
			fail(c, convErr)
			return
		}
		// `stale-while-revalidate` is what actually keeps a campus off this
		// endpoint. `max-age` alone only helps a student who refreshes within
		// the window; with SWR the browser RENDERS the cached copy instantly and
		// refreshes in the background, so a reopened tab costs no wait at all
		// and the request that does go out is not one anybody is waiting on.
		//
		// max-age stays SHORT deliberately. A long one would make the board lie
		// to a student who was just marked, and "the board says I am not marked"
		// is indistinguishable from a real missed mark — it produces disputes
		// for records that exist, on the one screen built to prevent exactly
		// that. Staleness is bounded by max-age; the load is absorbed by SWR
		// and by the server-side cache behind it.
		c.Header("Cache-Control", "public, max-age=30, stale-while-revalidate=300")
		// The board is a roster. Keeping it out of search indexes does not make
		// it private — it is a public URL and that is the decision — but an
		// indexed roster outlives the day it describes, and turns "who was out
		// on the 9th" into a permanent search result attached to a name.
		c.Header("X-Robots-Tag", "noindex, nofollow, noarchive")
		c.JSON(http.StatusOK, rendered)
	}
}

// SearchHandler answers the anonymous roster search.
func SearchHandler(svc PublicSearchService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Bounded at the edge as well as in the service. The service owns the
		// number (svcstudent.MinSearchRunes) and re-checks it regardless, so a
		// non-HTTP caller cannot skip the bound; checking here too means an
		// obviously-too-short query never costs a service call.
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			fail(c, searchValidation("q", "required", "Search query is required."))
			return
		}

		results, err := svc.PublicSearch(c.Request.Context(), query)
		if err != nil {
			fail(c, err)
			return
		}

		data := make([]contracts.PublicStudent, 0, len(results))
		for _, result := range results {
			rendered, convErr := publicStudentContract(result)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		// The roster changes only on an import, so a minute in the browser's own
		// cache costs nothing and keeps the type-ahead retries of a whole campus
		// off the API entirely.
		cacheFor(c, 60)
		c.JSON(http.StatusOK, contracts.PublicStudentSearchResults{Data: data})
	}
}

// RecordsHandler answers one student's public OD record view.
func RecordsHandler(svc RecordsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Validated at the edge so a malformed id is a typed 400 rather than a
		// database round trip, matching the posture the admin attendance handler
		// already uses for its path parameter.
		studentID := strings.TrimSpace(c.Param("student_id"))
		if _, err := uuid.Parse(studentID); err != nil {
			fail(c, searchValidation("student_id", "invalid_format", "student_id must be a UUID."))
			return
		}

		collection, err := svc.Records(c.Request.Context(), studentID)
		if err != nil {
			fail(c, err)
			return
		}

		rendered, convErr := recordCollectionContract(collection)
		if convErr != nil {
			fail(c, convErr)
			return
		}
		// Short on purpose, and shorter than the roster: this is the response a
		// student refreshes to find out whether a mark has landed, so it is the
		// one where staleness is felt as "the app is wrong".
		cacheFor(c, 30)
		c.JSON(http.StatusOK, rendered)
	}
}

// cacheFor lets the browser reuse a public response for a few seconds.
//
// `public` is correct here and would NOT be on any authenticated route: these
// two endpoints take no credential, the URL already contains the only thing that
// varies the answer, and nothing in the body is private to a session. It is at
// the handler rather than as blanket middleware so that a route added to this
// group later gets no caching until someone decides what its lifetime should be.
func cacheFor(c *gin.Context, seconds int) {
	if seconds <= 0 {
		return
	}
	c.Header("Cache-Control", "public, max-age="+strconv.Itoa(seconds))
}

// dayAttendanceContract builds the board field by field.
//
// An allowlist, like everything else in this file: nothing here is copied from a
// map or a database row wholesale, so the only way a new column reaches the open
// internet is if somebody writes the line that puts it there.
func dayAttendanceContract(board publicattendance.DayAttendance) (contracts.PublicDayAttendance, error) {
	odDate, err := parseDate(board.ODDate)
	if err != nil {
		return contracts.PublicDayAttendance{}, err
	}

	// Non-nil even when empty, for the same reason the day list is: a JSON
	// `null` would make the client guess whether it meant "nobody marked" or
	// "nothing loaded", and those are different answers.
	students := make([]contracts.PublicDayStudent, 0, len(board.Students))
	for _, student := range board.Students {
		marks := make([]contracts.PublicDayMark, 0, len(student.Marks))
		for _, mark := range student.Marks {
			categoryID, parseErr := uuid.Parse(mark.CategoryID)
			if parseErr != nil {
				return contracts.PublicDayAttendance{}, parseErr
			}
			marks = append(marks, contracts.PublicDayMark{
				Slot:         mark.Slot,
				CategoryId:   categoryID,
				CategoryName: mark.CategoryName,
				EventName:    mark.EventName,
			})
		}
		students = append(students, contracts.PublicDayStudent{
			RollNo:      student.RollNo,
			DisplayName: student.DisplayName,
			Marks:       marks,
		})
	}

	categories := make([]contracts.PublicEventCategory, 0, len(board.Categories))
	for _, category := range board.Categories {
		categoryID, parseErr := uuid.Parse(category.ID)
		if parseErr != nil {
			return contracts.PublicDayAttendance{}, parseErr
		}
		categories = append(categories, contracts.PublicEventCategory{Id: categoryID, Name: category.Name})
	}

	return contracts.PublicDayAttendance{
		OdDate:      odDate,
		Students:    students,
		Categories:  categories,
		Truncated:   board.Truncated,
		GeneratedAt: board.GeneratedAt,
	}, nil
}

func publicStudentContract(result svcstudent.SearchResult) (contracts.PublicStudent, error) {
	studentID, err := uuid.Parse(result.StudentID)
	if err != nil {
		return contracts.PublicStudent{}, err
	}
	return contracts.PublicStudent{
		StudentId:   studentID,
		RollNo:      result.RollNo,
		DisplayName: result.DisplayName,
		Branch:      result.Branch,
		Section:     result.Section,
		Batch:       result.Batch,
	}, nil
}

func recordCollectionContract(collection publicattendance.RecordCollection) (contracts.PublicODRecordCollection, error) {
	studentID, err := uuid.Parse(collection.Student.StudentID)
	if err != nil {
		return contracts.PublicODRecordCollection{}, err
	}

	// Non-nil even when empty: a JSON `null` for `days` would make the generated
	// client's array optional at runtime, and the screen would have to guess
	// whether null meant "no marks" or "nothing loaded".
	days := make([]contracts.PublicODDay, 0, len(collection.Days))
	for _, day := range collection.Days {
		odDate, parseErr := parseDate(day.ODDate)
		if parseErr != nil {
			return contracts.PublicODRecordCollection{}, parseErr
		}
		records := make([]contracts.PublicODRecord, 0, len(day.Records))
		for _, record := range day.Records {
			rendered, convErr := recordContract(record)
			if convErr != nil {
				return contracts.PublicODRecordCollection{}, convErr
			}
			records = append(records, rendered)
		}
		days = append(days, contracts.PublicODDay{OdDate: odDate, Records: records})
	}

	return contracts.PublicODRecordCollection{
		Student: contracts.PublicStudent{
			StudentId:   studentID,
			RollNo:      collection.Student.RollNo,
			DisplayName: collection.Student.DisplayName,
			Branch:      collection.Student.Branch,
			Section:     collection.Student.Section,
			Batch:       collection.Student.Batch,
		},
		Days:        days,
		GeneratedAt: collection.GeneratedAt,
	}, nil
}

func recordContract(record publicattendance.Record) (contracts.PublicODRecord, error) {
	recordID, err := uuid.Parse(record.RecordID)
	if err != nil {
		return contracts.PublicODRecord{}, err
	}
	eventID, err := uuid.Parse(record.EventID)
	if err != nil {
		return contracts.PublicODRecord{}, err
	}
	categoryID, err := uuid.Parse(record.CategoryID)
	if err != nil {
		return contracts.PublicODRecord{}, err
	}
	eventDate, err := parseDate(record.EventDate)
	if err != nil {
		return contracts.PublicODRecord{}, err
	}
	odDate, err := parseDate(record.ODDate)
	if err != nil {
		return contracts.PublicODRecord{}, err
	}
	return contracts.PublicODRecord{
		RecordId:     recordID,
		Slot:         record.Slot,
		EventId:      eventID,
		EventName:    record.EventName,
		EventDate:    eventDate,
		OdDate:       odDate,
		CategoryId:   categoryID,
		CategoryName: record.CategoryName,
		MarkedByName: record.MarkedByName,
		MarkedByRole: contracts.PublicODRecordMarkedByRole(record.MarkedByRole),
		MarkedAt:     record.MarkedAt,
	}, nil
}

func parseDate(value string) (openapi_types.Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return openapi_types.Date{}, err
	}
	return openapi_types.Date{Time: parsed}, nil
}

func searchValidation(field, code, message string) error {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Public dashboard request validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

// fail routes an error through the shared error middleware so a public refusal
// is the same typed envelope every other refusal in the API produces. No handler
// here writes an ad hoc JSON error.
func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
