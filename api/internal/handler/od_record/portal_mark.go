package odrecord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

type PortalMarkService interface {
	Events(context.Context, marking.Actor) ([]marking.Event, error)
	Categories(context.Context, marking.Actor, string) ([]marking.Category, error)
}

type PortalBatchMarkService interface {
	MarkBatch(context.Context, odsvc.Actor, odsvc.BatchMarkInput) ([]odsvc.MarkResult, error)
}

func RegisterPortalMarkRoutes(group gin.IRouter, svc PortalMarkService) {
	group.GET("/portal/mark/events", PortalMarkEventsHandler(svc))
	group.GET("/portal/mark/events/:event_id/categories", PortalMarkCategoriesHandler(svc))
}

// RegisterPortalBatchMarkRoutes is deliberately separate from the read service:
// the production write service owns the transaction and scope checks.
func RegisterPortalBatchMarkRoutes(group gin.IRouter, svc PortalBatchMarkService) {
	group.POST("/portal/mark/batch", PortalBatchMarkHandler(svc))
}

func PortalBatchMarkHandler(svc PortalBatchMarkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMarkRequestBytes)
		var shape struct {
			EventID    *json.RawMessage `json:"event_id"`
			CategoryID *json.RawMessage `json:"category_id"`
			StudentIDs *json.RawMessage `json:"student_ids"`
			Slot       *json.RawMessage `json:"slot"`
			ODDate     *json.RawMessage `json:"od_date"`
			ReasonNote *json.RawMessage `json:"reason_note"`
		}
		if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
			fail(c, requestValidation("$", "invalid_json", "Request body must be valid JSON."))
			return
		}
		for _, field := range []struct {
			name  string
			value *json.RawMessage
		}{
			{"event_id", shape.EventID}, {"category_id", shape.CategoryID}, {"student_ids", shape.StudentIDs}, {"slot", shape.Slot}, {"od_date", shape.ODDate}, {"reason_note", shape.ReasonNote},
		} {
			if !rawProvided(field.value) {
				fail(c, requestValidation(field.name, "required", field.name+" is required."))
				return
			}
		}
		var body contracts.BatchManualMarkAttendanceRequest
		if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
			fail(c, requestValidation("$", "invalid_json", "Request body does not match the batch mark schema."))
			return
		}
		studentIDs := make([]string, len(body.StudentIds))
		for i, id := range body.StudentIds {
			studentIDs[i] = id.String()
		}
		results, err := svc.MarkBatch(c.Request.Context(), actor, odsvc.BatchMarkInput{EventID: body.EventId.String(), CategoryID: body.CategoryId.String(), StudentIDs: studentIDs, Slot: body.Slot, ODDate: body.OdDate.Time, ReasonNote: body.ReasonNote})
		var duplicate odsvc.BatchDuplicateError
		if errors.As(err, &duplicate) {
			students := make([]contracts.MarkedStudent, 0, len(duplicate.Students))
			for _, student := range duplicate.Students {
				id, parseErr := uuid.Parse(student.StudentID)
				if parseErr != nil {
					fail(c, parseErr)
					return
				}
				students = append(students, contracts.MarkedStudent{StudentId: id, RollNo: student.RollNo, DisplayName: student.DisplayName, Branch: student.Branch, Section: student.Section, Batch: student.Batch})
			}
			c.JSON(http.StatusConflict, contracts.BatchManualMarkConflict{Code: contracts.BatchManualMarkConflictCodeSCANDUPLICATE, Message: "Remove already-marked students and try again.", DuplicateStudents: students})
			return
		}
		if err != nil {
			fail(c, err)
			return
		}
		records := make([]contracts.MarkAttendanceResult, 0, len(results))
		for _, result := range results {
			rendered, convErr := resultContract(result)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			records = append(records, rendered)
		}
		c.JSON(http.StatusCreated, contracts.BatchManualMarkAttendanceResult{Records: records})
	}
}

func PortalMarkEventsHandler(svc PortalMarkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := portalMarkActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		page, pageSize, err := portalPagination(c)
		if err != nil {
			fail(c, err)
			return
		}
		events, err := svc.Events(c.Request.Context(), actor)
		if err != nil {
			fail(c, err)
			return
		}
		slice := pageWindow(len(events), page, pageSize)
		data := make([]contracts.Event, 0, slice.to-slice.from)
		for _, event := range events[slice.from:slice.to] {
			rendered, convErr := eventContract(event)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.EventCollection{Data: data, Page: page, PageSize: pageSize, Total: len(events)})
	}
}

func PortalMarkCategoriesHandler(svc PortalMarkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := portalMarkActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		eventID := strings.TrimSpace(c.Param("event_id"))
		if _, parseErr := uuid.Parse(eventID); parseErr != nil {
			fail(c, readValidation("event_id", "invalid_format", "event_id must be a UUID."))
			return
		}
		categories, err := svc.Categories(c.Request.Context(), actor, eventID)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.Category, 0, len(categories))
		for _, category := range categories {
			rendered, convErr := categoryContract(category)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.CategoryCollection{Data: data})
	}
}

func portalMarkActor(c *gin.Context) (marking.Actor, error) {
	actor, err := actorFromContext(c)
	if err != nil {
		return marking.Actor{}, err
	}
	if actor.Role != "teacher" && actor.Role != "admin" {
		return marking.Actor{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher or administrator access is required."}
	}
	return marking.Actor{UserID: actor.UserID, Email: actor.Email, Role: actor.Role}, nil
}

func portalPagination(c *gin.Context) (int, int, error) {
	page, err := boundedQueryInt(c, "page", 1, 1, maxEventPage)
	if err != nil {
		return 0, 0, err
	}
	pageSize, err := boundedQueryInt(c, "page_size", 20, 1, maxEventPageSize)
	if err != nil {
		return 0, 0, err
	}
	return page, pageSize, nil
}
