package odrecord

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

type AttendanceService interface {
	// The *string after *int is the optional od_date filter (YYYY-MM-DD).
	Attendance(context.Context, marking.Actor, string, *string, *int, *string, int, int) (marking.AttendancePage, error)
}

func RegisterAdminAttendanceRoutes(group gin.IRouter, svc AttendanceService) {
	group.GET("/events/:event_id/attendance", AttendanceHandler(svc))
}

// RegisterPortalAttendanceRoutes mounts the TEACHER-reachable sibling of the
// route above, on the authenticated group rather than the admin one.
//
// A separate registration with its own actor extractor, deliberately — and it is
// the reason the admin gate lives in the extractor rather than in the handler.
// The two routes differ ONLY in who may reach them; the service then scopes what
// each of them sees. Reusing the admin extractor here would 403 every teacher,
// and widening it would open the admin route to teachers.
func RegisterPortalAttendanceRoutes(group gin.IRouter, svc AttendanceService) {
	group.GET("/portal/mark/events/:event_id/attendance", attendanceHandler(svc, portalAttendanceActor))
}

func AttendanceHandler(svc AttendanceService) gin.HandlerFunc {
	return attendanceHandler(svc, adminAttendanceActor)
}

// attendanceHandler is the shared body. The actor extractor is the only seam, so
// the two routes cannot drift in their filters, their validation or their
// response shape — only in who is admitted.
func attendanceHandler(svc AttendanceService, actorOf func(*gin.Context) (marking.Actor, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorOf(c)
		if err != nil {
			fail(c, err)
			return
		}
		eventID := strings.TrimSpace(c.Param("event_id"))
		if _, parseErr := uuid.Parse(eventID); parseErr != nil {
			fail(c, readValidation("event_id", "invalid_format", "event_id must be a UUID."))
			return
		}

		var categoryID *string
		if raw := strings.TrimSpace(c.Query("category_id")); raw != "" {
			if _, parseErr := uuid.Parse(raw); parseErr != nil {
				fail(c, readValidation("category_id", "invalid_format", "category_id must be a UUID."))
				return
			}
			categoryID = &raw
		}

		slot, err := attendanceSlot(c)
		if err != nil {
			fail(c, err)
			return
		}

		// Optional: absent means the whole range, which is what every caller
		// written before the event became a range already asks for. The format is
		// checked here so a typo is a 400 on the field rather than a filter that
		// silently matches nothing.
		var odDateFilter *string
		if raw := strings.TrimSpace(c.Query("od_date")); raw != "" {
			parsed, parseErr := time.Parse(time.DateOnly, raw)
			if parseErr != nil || parsed.Format(time.DateOnly) != raw {
				fail(c, readValidation("od_date", "invalid_format", "od_date must be a valid YYYY-MM-DD date."))
				return
			}
			odDateFilter = &raw
		}
		page, err := boundedQueryInt(c, "page", 1, 1, maxEventPage)
		if err != nil {
			fail(c, err)
			return
		}
		pageSize, err := boundedQueryInt(c, "page_size", 20, 1, maxEventPageSize)
		if err != nil {
			fail(c, err)
			return
		}

		pageData, err := svc.Attendance(c.Request.Context(), actor, eventID, categoryID, slot, odDateFilter, page, pageSize)
		if err != nil {
			fail(c, err)
			return
		}

		data := make([]contracts.AttendanceRecord, 0, len(pageData.Data))
		for _, record := range pageData.Data {
			rendered, convErr := attendanceRecordContract(record)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.AttendanceRecordCollection{
			Data:     data,
			Page:     pageData.Page,
			PageSize: pageData.PageSize,
			Total:    pageData.Total,
		})
	}
}

// portalAttendanceActor admits a teacher OR an admin. What each of them then
// SEES is decided by the service, which scopes a teacher to their own branches;
// this only decides who gets through the door.
func portalAttendanceActor(c *gin.Context) (marking.Actor, error) {
	actor, err := actorFromContext(c)
	if err != nil {
		return marking.Actor{}, err
	}
	if actor.Role != "admin" && actor.Role != "teacher" {
		return marking.Actor{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher or administrator access is required for attendance review."}
	}
	return marking.Actor{UserID: actor.UserID, Email: actor.Email, Role: actor.Role}, nil
}

func adminAttendanceActor(c *gin.Context) (marking.Actor, error) {
	actor, err := actorFromContext(c)
	if err != nil {
		return marking.Actor{}, err
	}
	if actor.Role != "admin" {
		return marking.Actor{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required for attendance review."}
	}
	return marking.Actor{UserID: actor.UserID, Email: actor.Email, Role: actor.Role}, nil
}

func attendanceSlot(c *gin.Context) (*int, error) {
	raw := strings.TrimSpace(c.Query("slot"))
	if raw == "" {
		return nil, nil
	}
	value, err := boundedQueryInt(c, "slot", 1, 1, 12)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func attendanceRecordContract(record marking.AttendanceRecord) (contracts.AttendanceRecord, error) {
	recordID, err := uuid.Parse(record.RecordID)
	if err != nil {
		return contracts.AttendanceRecord{}, err
	}
	eventID, err := uuid.Parse(record.EventID)
	if err != nil {
		return contracts.AttendanceRecord{}, err
	}
	categoryID, err := uuid.Parse(record.Category.CategoryID)
	if err != nil {
		return contracts.AttendanceRecord{}, err
	}
	studentID, err := uuid.Parse(record.Student.StudentID)
	if err != nil {
		return contracts.AttendanceRecord{}, err
	}
	var userID *openapi_types.UUID
	if record.MarkedBy.UserID != nil {
		parsed, parseErr := uuid.Parse(*record.MarkedBy.UserID)
		if parseErr != nil {
			return contracts.AttendanceRecord{}, parseErr
		}
		value := openapi_types.UUID(parsed)
		userID = &value
	}
	var email *openapi_types.Email
	if record.MarkedBy.Email != nil {
		value := openapi_types.Email(*record.MarkedBy.Email)
		email = &value
	}
	var name *string
	if record.MarkedBy.Name != nil {
		value := *record.MarkedBy.Name
		name = &value
	}
	parsedODDate, err := time.Parse(time.DateOnly, record.ODDate)
	if err != nil {
		return contracts.AttendanceRecord{}, err
	}
	odDate := openapi_types.Date{Time: parsedODDate}
	return contracts.AttendanceRecord{
		RecordId: recordID,
		Student: contracts.MarkedStudent{
			StudentId:   studentID,
			RollNo:      record.Student.RollNo,
			DisplayName: record.Student.DisplayName,
			Branch:      record.Student.Branch,
			Section:     record.Student.Section,
			Batch:       record.Student.Batch,
		},
		EventId: eventID,
		Category: contracts.Category{
			Id:        categoryID,
			EventId:   eventID,
			Name:      record.Category.Name,
			CreatedAt: record.Category.CreatedAt,
		},
		Slot:          record.Slot,
		OdDate:        odDate,
		CaptureMethod: contracts.CaptureMethod(record.CaptureMethod),
		MarkedBy: contracts.AttendanceActor{
			UserId: userID,
			Email:  email,
			Name:   name,
		},
		ActorRole:        contracts.AttendanceRecordActorRole(record.ActorRole),
		ServerReceivedAt: record.ServerReceivedAt,
	}, nil
}
