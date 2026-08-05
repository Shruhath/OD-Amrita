// Package publicattendance is the HTTP edge of the UNAUTHENTICATED public
// attendance dashboard (Story 0.10, FR38-FR40).
//
// Everything these two handlers return is served to the open internet with no
// credential, so the mapping below is written as an allowlist: each contract
// struct is built field by field from the service's own display types, and
// neither service type has an email, an actor id, a card token or a fingerprint
// to leak in the first place. There is no `interface{}`/map passthrough anywhere
// in this file for exactly that reason — a passthrough would publish whatever
// the query happened to select.
//
// Both routes are READS. There is no POST in this package, and none may be
// added: a write reachable without authentication is an open door, not a
// feature.
package publicattendance

import (
	"context"
	"net/http"
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

// RegisterPublicRoutes mounts the two public reads.
//
// `group` MUST be a group built from the ROOT gin router (see newPublicGroup in
// cmd/api), never a child of the authenticated /api/v1 group. gin attaches
// middleware to the group value rather than to the path prefix, so mounting
// these on `api.Group("/public")` produces the identical URLs behind the auth
// middleware and 401s every student. A route-composition test is what proves
// which one happened.
func RegisterPublicRoutes(group gin.IRouter, search PublicSearchService, records RecordsService) {
	group.GET("/students/search", SearchHandler(search))
	group.GET("/students/:student_id/od-records", RecordsHandler(records))
}

// SearchHandler answers the anonymous roster prefix search.
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
		c.JSON(http.StatusOK, rendered)
	}
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
		eventDate, parseErr := parseDate(day.EventDate)
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
		days = append(days, contracts.PublicODDay{EventDate: eventDate, Records: records})
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
	return contracts.PublicODRecord{
		RecordId:     recordID,
		Slot:         record.Slot,
		EventId:      eventID,
		EventName:    record.EventName,
		EventDate:    eventDate,
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
